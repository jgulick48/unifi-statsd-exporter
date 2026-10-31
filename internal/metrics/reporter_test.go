package metrics

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"

	"github.com/jgulick48/unifi-statsd-exporter/internal/config"
	"github.com/jgulick48/unifi-statsd-exporter/internal/snmp"
	"github.com/jgulick48/unifi-statsd-exporter/internal/unifi"
)

type call struct {
	kind  string
	name  string
	value float64
	tags  []string
}

type fakeClient struct {
	statsd.NoOpClient
	calls  []call
	events []*statsd.Event
}

func (f *fakeClient) Gauge(name string, v float64, tags []string, _ float64) error {
	f.calls = append(f.calls, call{"gauge", name, v, tags})
	return nil
}
func (f *fakeClient) Count(name string, v int64, tags []string, _ float64) error {
	f.calls = append(f.calls, call{"count", name, float64(v), tags})
	return nil
}
func (f *fakeClient) Timing(name string, d time.Duration, tags []string, _ float64) error {
	f.calls = append(f.calls, call{"timing", name, float64(d.Milliseconds()), tags})
	return nil
}
func (f *fakeClient) Event(e *statsd.Event) error {
	f.events = append(f.events, e)
	return nil
}

func (f *fakeClient) find(t *testing.T, name string, tagSubs ...string) call {
	t.Helper()
	for _, c := range f.calls {
		if c.name != name {
			continue
		}
		ok := true
		for _, s := range tagSubs {
			found := false
			for _, tg := range c.tags {
				if tg == s {
					found = true
				}
			}
			ok = ok && found
		}
		if ok {
			return c
		}
	}
	t.Fatalf("no call %q with tags %v; calls: %+v", name, tagSubs, f.calls)
	return call{}
}

func (f *fakeClient) has(name string) bool {
	for _, c := range f.calls {
		if c.name == name {
			return true
		}
	}
	return false
}

func fp(v float64) *float64 { return &v }

func switchDevice() unifi.Device {
	return unifi.Device{
		MAC: "aa:bb:cc:dd:ee:ff", Name: "sw,1", Model: "USW", Type: "usw", Online: true,
		PoEBudgetWatts: fp(150), PoEUsedWatts: fp(30),
		Ports: []unifi.PoEPort{
			{Index: 1, Name: "AP", Up: true, PoEEnabled: true, PoEGood: true, PoEMode: "auto", PoEClass: "Class 4", PowerWatts: 12.5, VoltageVolts: 53, CurrentAmps: 0.23},
			{Index: 2, Name: "Empty", PoEEnabled: true},
		},
	}
}

func TestSwitch(t *testing.T) {
	f := &fakeClient{}
	NewWithClient(f, "default").ReportDevices([]unifi.Device{switchDevice()})

	base := []string{"site:default", "device:sw_1", "mac:aa:bb:cc:dd:ee:ff", "model:USW", "device_type:usw"}
	if c := f.find(t, "device.online", base...); c.value != 1 {
		t.Errorf("online = %v", c.value)
	}
	if c := f.find(t, "device.poe.budget_watts", base...); c.value != 150 {
		t.Errorf("budget = %v", c.value)
	}
	if c := f.find(t, "device.poe.used_watts"); c.value != 30 {
		t.Errorf("used = %v", c.value)
	}
	if c := f.find(t, "device.poe.ports_active"); c.value != 1 {
		t.Errorf("active = %v", c.value)
	}
	p := f.find(t, "poe.power_watts", append(base, "port:1", "port_name:AP", "poe_mode:auto", "poe_class:Class 4")...)
	if p.value != 12.5 {
		t.Errorf("power = %v", p.value)
	}
	if c := f.find(t, "poe.good", "port:1"); c.value != 1 {
		t.Errorf("good = %v", c.value)
	}
	if c := f.find(t, "port.up", "port:2"); c.value != 0 {
		t.Errorf("up = %v", c.value)
	}
	// Port 2 has no mode/class tags.
	for _, c := range f.calls {
		if c.name == "poe.enabled" {
			for _, tg := range c.tags {
				if tg == "port:2" && (len(c.tags) != len(base)+2) {
					t.Errorf("port 2 tags = %v", c.tags)
				}
			}
		}
	}
	if f.has("device.outlet.power_watts") || f.has("pdu.outlet.relay_on") {
		t.Error("unexpected outlet metrics")
	}
	if c := f.find(t, "site.poe.used_watts", "site:default"); c.value != 30 || len(c.tags) != 1 {
		t.Errorf("site poe = %+v", c)
	}
	if c := f.find(t, "site.devices"); c.value != 1 {
		t.Errorf("devices = %v", c.value)
	}
}

func TestPDUAndTotalsFallback(t *testing.T) {
	pdu := unifi.Device{
		MAC: "11:22:33:44:55:66", Name: "pdu", Model: "USPPDUP", Type: "usp", Online: true,
		Outlets: []unifi.Outlet{
			{Index: 1, Name: "NAS", RelayOn: true, PowerWatts: fp(40), VoltageVolts: fp(120), CurrentAmps: fp(0.33), PowerFactor: fp(0.9)},
			{Index: 2, Name: "Dumb", RelayOn: false},
			{Index: 3, Name: "Router", RelayOn: true, PowerWatts: fp(10)},
		},
	}
	sw := unifi.Device{Name: "nometer", Ports: []unifi.PoEPort{{Index: 1, PowerWatts: 5}, {Index: 2, PowerWatts: 7}}}
	f := &fakeClient{}
	NewWithClient(f, "s").ReportDevices([]unifi.Device{pdu, sw, switchDevice()})

	if c := f.find(t, "pdu.outlet.power_watts", "outlet:1", "outlet_name:NAS"); c.value != 40 {
		t.Errorf("outlet power = %v", c.value)
	}
	f.find(t, "pdu.outlet.voltage_volts", "outlet:1")
	f.find(t, "pdu.outlet.current_amps", "outlet:1")
	f.find(t, "pdu.outlet.power_factor", "outlet:1")
	if c := f.find(t, "pdu.outlet.relay_on", "outlet:2"); c.value != 0 {
		t.Errorf("relay = %v", c.value)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c.name, "pdu.outlet.") && c.name != "pdu.outlet.relay_on" {
			for _, tg := range c.tags {
				if tg == "outlet:2" {
					t.Errorf("unmetered outlet emitted %s", c.name)
				}
			}
		}
	}
	// outlet 3: only power.
	for _, n := range []string{"pdu.outlet.voltage_volts", "pdu.outlet.current_amps", "pdu.outlet.power_factor"} {
		for _, c := range f.calls {
			if c.name == n {
				for _, tg := range c.tags {
					if tg == "outlet:3" {
						t.Errorf("outlet 3 emitted %s", n)
					}
				}
			}
		}
	}
	// PDU has no OutletPowerWatts: fallback to 40+10.
	if c := f.find(t, "site.outlet.power_watts"); c.value != 50 {
		t.Errorf("site outlet = %v", c.value)
	}
	// nometer: 12 (ports) + switchDevice: 30 (reported).
	if c := f.find(t, "site.poe.used_watts"); c.value != 42 {
		t.Errorf("site poe = %v", c.value)
	}
	if c := f.find(t, "site.devices"); c.value != 3 {
		t.Errorf("site devices = %v", c.value)
	}

	// Reported outlet total takes precedence.
	pdu.OutletPowerWatts = fp(99)
	pdu.OutletBudgetWatts = fp(1000)
	f = &fakeClient{}
	NewWithClient(f, "s").ReportDevices([]unifi.Device{pdu})
	if c := f.find(t, "site.outlet.power_watts"); c.value != 99 {
		t.Errorf("site outlet = %v", c.value)
	}
	f.find(t, "device.outlet.power_watts", "site:s")
	f.find(t, "device.outlet.budget_watts")
}

func TestPoll(t *testing.T) {
	f := &fakeClient{}
	r := NewWithClient(f, "default")
	r.ReportPoll(1500*time.Millisecond, nil)
	r.ReportPoll(time.Second, errTest{})
	if c := f.find(t, "collector.poll_duration_ms", "site:default"); c.kind != "timing" || c.value != 1500 {
		t.Errorf("timing = %+v", c)
	}
	if c := f.find(t, "collector.poll", "result:success", "site:default"); c.value != 1 || c.kind != "count" {
		t.Errorf("success = %+v", c)
	}
	f.find(t, "collector.poll", "result:error", "site:default")
}

type errTest struct{}

func (errTest) Error() string { return "boom" }

func TestTrap(t *testing.T) {
	f := &fakeClient{}
	r := NewWithClient(f, "default")
	r.ReportTrap(snmp.Trap{Source: "10.0.0.2", OID: "1.3.6.1.6.3.1.1.5.3", Name: "linkDown",
		Variables: map[string]string{"2.0": "b", "1.0": "a"}})
	r.ReportTrap(snmp.Trap{Source: "10.0.0.3", OID: "1.2.3", Name: "coldStart"})

	f.find(t, "snmp.trap", "source:10.0.0.2", "trap:linkDown", "oid:1.3.6.1.6.3.1.1.5.3")
	if len(f.events) != 2 {
		t.Fatalf("events = %d", len(f.events))
	}
	e := f.events[0]
	if e.Title != "UniFi SNMP trap: linkDown from 10.0.0.2" || e.Text != "1.0=a\n2.0=b" {
		t.Errorf("event = %+v", e)
	}
	if e.AlertType != statsd.Warning || e.SourceTypeName != "snmp" || len(e.Tags) != 3 {
		t.Errorf("event = %+v", e)
	}
	if f.events[1].AlertType != statsd.Info {
		t.Errorf("alert = %v", f.events[1].AlertType)
	}
}

func TestEndToEndUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	r, client, err := New(config.Statsd{Address: pc.LocalAddr().String(), Namespace: "unifi.", Tags: []string{"env:test"}}, "default")
	if err != nil {
		t.Fatal(err)
	}
	r.ReportDevices([]unifi.Device{switchDevice()})
	if err := client.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	var all strings.Builder
	buf := make([]byte, 65535)
	for {
		_ = pc.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			break
		}
		all.Write(buf[:n])
	}
	got := all.String()
	var line string
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "unifi.poe.power_watts:12.5|g|#") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no poe.power_watts line in:\n%s", got)
	}
	for _, want := range []string{"site:default", "device:sw_1", "mac:aa:bb:cc:dd:ee:ff", "port:1", "port_name:AP", "poe_mode:auto", "env:test"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q missing %q", line, want)
		}
	}
}
