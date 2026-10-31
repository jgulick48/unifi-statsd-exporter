package snmp

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/jgulick48/unifi-statsd-exporter/internal/config"
)

func freePort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	return pc.LocalAddr().(*net.UDPAddr).Port
}

type harness struct {
	port   int
	traps  chan Trap
	cancel context.CancelFunc
	done   chan error
}

func start(t *testing.T, community string) *harness {
	t.Helper()
	port := freePort(t)
	h := &harness{port: port, traps: make(chan Trap, 8), done: make(chan error, 1)}
	l := NewListener(config.SNMPTraps{Enabled: true, ListenAddress: "127.0.0.1:" + strconv.Itoa(port), Community: community},
		func(tr Trap) { h.traps <- tr })
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.done <- l.Run(ctx) }()
	// Wait until the port is bound by probing with a bind attempt.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pc, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			break
		}
		pc.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return")
		}
	})
	return h
}

func (h *harness) client(t *testing.T, v gosnmp.SnmpVersion, community string) *gosnmp.GoSNMP {
	t.Helper()
	g := &gosnmp.GoSNMP{Target: "127.0.0.1", Port: uint16(h.port), Version: v, Community: community, Timeout: time.Second}
	if err := g.Connect(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Conn.Close() })
	return g
}

func (h *harness) expect(t *testing.T) Trap {
	t.Helper()
	select {
	case tr := <-h.traps:
		return tr
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for trap")
		return Trap{}
	}
}

func TestV2cTrap(t *testing.T) {
	h := start(t, "public")
	g := h.client(t, gosnmp.Version2c, "public")
	_, err := g.SendTrap(gosnmp.SnmpTrap{Variables: []gosnmp.SnmpPDU{
		{Name: ".1.3.6.1.2.1.1.3.0", Type: gosnmp.TimeTicks, Value: uint32(100)},
		{Name: ".1.3.6.1.6.3.1.1.4.1.0", Type: gosnmp.ObjectIdentifier, Value: ".1.3.6.1.6.3.1.1.5.3"},
		{Name: ".1.3.6.1.2.1.2.2.1.1.12", Type: gosnmp.Integer, Value: 12},
		{Name: ".1.3.6.1.2.1.2.2.1.2.12", Type: gosnmp.OctetString, Value: "eth12"},
		{Name: ".1.3.6.1.4.1.41112.1.1", Type: gosnmp.OctetString, Value: []byte{0xff, 0x00}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tr := h.expect(t)
	if tr.OID != "1.3.6.1.6.3.1.1.5.3" || tr.Name != "linkDown" || tr.Source != "127.0.0.1" || tr.Received.IsZero() {
		t.Fatalf("bad trap: %+v", tr)
	}
	want := map[string]string{"ifIndex.12": "12", "ifDescr.12": "eth12", "1.3.6.1.4.1.41112.1.1": "ff00"}
	if len(tr.Variables) != len(want) {
		t.Fatalf("variables = %v", tr.Variables)
	}
	for k, v := range want {
		if tr.Variables[k] != v {
			t.Errorf("var %s = %q, want %q (all: %v)", k, tr.Variables[k], v, tr.Variables)
		}
	}
}

func TestV1Trap(t *testing.T) {
	h := start(t, "")
	g := h.client(t, gosnmp.Version1, "anything")
	_, err := g.SendTrap(gosnmp.SnmpTrap{
		Variables:    []gosnmp.SnmpPDU{{Name: ".1.3.6.1.2.1.2.2.1.1.3", Type: gosnmp.Integer, Value: 3}},
		Enterprise:   ".1.3.6.1.4.1.41112",
		AgentAddress: "127.0.0.1",
		GenericTrap:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	tr := h.expect(t)
	if tr.OID != "1.3.6.1.6.3.1.1.5.3" || tr.Name != "linkDown" || tr.Variables["ifIndex.3"] != "3" {
		t.Fatalf("bad trap: %+v", tr)
	}

	_, err = g.SendTrap(gosnmp.SnmpTrap{
		Variables:    []gosnmp.SnmpPDU{{Name: ".1.3.6.1.2.1.2.2.1.1.3", Type: gosnmp.Integer, Value: 3}},
		Enterprise:   ".1.3.6.1.4.1.41112",
		AgentAddress: "127.0.0.1",
		GenericTrap:  6,
		SpecificTrap: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	tr = h.expect(t)
	if tr.OID != "1.3.6.1.4.1.41112.0.7" || tr.Name != "ubnt.0.7" {
		t.Fatalf("bad enterprise trap: %+v", tr)
	}
}

func TestCommunityMismatchDropped(t *testing.T) {
	h := start(t, "secret")
	bad := h.client(t, gosnmp.Version2c, "wrong")
	good := h.client(t, gosnmp.Version2c, "secret")
	vars := []gosnmp.SnmpPDU{{Name: ".1.3.6.1.6.3.1.1.4.1.0", Type: gosnmp.ObjectIdentifier, Value: ".1.3.6.1.6.3.1.1.5.4"}}
	if _, err := bad.SendTrap(gosnmp.SnmpTrap{Variables: vars}); err != nil {
		t.Fatal(err)
	}
	// UDP on loopback is ordered per socket pair; send the good one after
	// and confirm only it arrives.
	time.Sleep(100 * time.Millisecond)
	if _, err := good.SendTrap(gosnmp.SnmpTrap{Variables: vars}); err != nil {
		t.Fatal(err)
	}
	tr := h.expect(t)
	if tr.Name != "linkUp" {
		t.Fatalf("unexpected trap: %+v", tr)
	}
	select {
	case extra := <-h.traps:
		t.Fatalf("mismatched community was not dropped: %+v", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestHandlerPanicRecovered(t *testing.T) {
	port := freePort(t)
	got := make(chan Trap, 2)
	n := 0
	l := NewListener(config.SNMPTraps{ListenAddress: "127.0.0.1:" + strconv.Itoa(port)}, func(tr Trap) {
		n++
		if n == 1 {
			panic("boom")
		}
		got <- tr
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.Run(ctx) }()
	defer func() { cancel(); <-done }()
	g := &gosnmp.GoSNMP{Target: "127.0.0.1", Port: uint16(port), Version: gosnmp.Version2c, Community: "x", Timeout: time.Second}
	if err := g.Connect(); err != nil {
		t.Fatal(err)
	}
	defer g.Conn.Close()
	vars := []gosnmp.SnmpPDU{{Name: ".1.3.6.1.6.3.1.1.4.1.0", Type: gosnmp.ObjectIdentifier, Value: ".1.3.6.1.6.3.1.1.5.1"}}
	deadline := time.After(3 * time.Second)
	for {
		g.SendTrap(gosnmp.SnmpTrap{Variables: vars})
		select {
		case <-got:
			return
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("listener did not survive handler panic")
		}
	}
}

func TestRunCancelReturnsNil(t *testing.T) {
	l := NewListener(config.SNMPTraps{ListenAddress: "127.0.0.1:" + strconv.Itoa(freePort(t))}, func(Trap) {})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- l.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunCancelledBeforeStart(t *testing.T) {
	l := NewListener(config.SNMPTraps{ListenAddress: "127.0.0.1:" + strconv.Itoa(freePort(t))}, func(Trap) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRunBindError(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	l := NewListener(config.SNMPTraps{ListenAddress: pc.LocalAddr().String()}, func(Trap) {})
	done := make(chan error, 1)
	go func() { done <- l.Run(context.Background()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected bind error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run hung on bind error")
	}
}

func TestTrapName(t *testing.T) {
	cases := map[string]string{
		"1.3.6.1.6.3.1.1.5.1":       "coldStart",
		".1.3.6.1.6.3.1.1.5.5":      "authenticationFailure",
		"1.3.6.1.2.1.105.0.1":       "pethPsePortOnOffNotification",
		"1.3.6.1.2.1.105.0.2":       "pethMainPowerUsageOnNotification",
		"1.3.6.1.2.1.105.0.3":       "pethMainPowerUsageOffNotification",
		"1.3.6.1.4.1.41112.1.6.3.1": "ubnt.1.6.3.1",
		"1.3.6.1.4.1.411120.1":      "1.3.6.1.4.1.411120.1",
		"1.2.3":                     "1.2.3",
	}
	for in, want := range cases {
		if got := TrapName(in); got != want {
			t.Errorf("TrapName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVariableKey(t *testing.T) {
	cases := map[string]string{
		".1.3.6.1.2.1.2.2.1.1.5":      "ifIndex.5",
		"1.3.6.1.2.1.2.2.1.2.5":       "ifDescr.5",
		"1.3.6.1.2.1.31.1.1.1.1.9":    "ifName.9",
		"1.3.6.1.2.1.105.1.1.1.6.1.4": "pethPsePortDetectionStatus.1.4",
		"1.3.6.1.2.1.2.2.1.10.5":      "1.3.6.1.2.1.2.2.1.10.5",
		".1.3.6.1.2.1.2.2.1.1":        "1.3.6.1.2.1.2.2.1.1",
	}
	for in, want := range cases {
		if got := variableKey(in); got != want {
			t.Errorf("variableKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		pdu  gosnmp.SnmpPDU
		want string
	}{
		{gosnmp.SnmpPDU{Type: gosnmp.OctetString, Value: []byte("hello")}, "hello"},
		{gosnmp.SnmpPDU{Type: gosnmp.OctetString, Value: []byte{0xde, 0xad, 0xbe, 0xef}}, "deadbeef"},
		{gosnmp.SnmpPDU{Type: gosnmp.OctetString, Value: []byte{0x00, 0x1a}}, "001a"},
		{gosnmp.SnmpPDU{Type: gosnmp.Integer, Value: -4}, "-4"},
		{gosnmp.SnmpPDU{Type: gosnmp.Counter32, Value: uint(7)}, "7"},
		{gosnmp.SnmpPDU{Type: gosnmp.Counter64, Value: uint64(1) << 40}, "1099511627776"},
		{gosnmp.SnmpPDU{Type: gosnmp.TimeTicks, Value: uint32(55)}, "55"},
		{gosnmp.SnmpPDU{Type: gosnmp.ObjectIdentifier, Value: ".1.3.6.1"}, "1.3.6.1"},
		{gosnmp.SnmpPDU{Type: gosnmp.IPAddress, Value: "10.0.0.1"}, "10.0.0.1"},
		{gosnmp.SnmpPDU{Type: gosnmp.Null, Value: nil}, ""},
		{gosnmp.SnmpPDU{Type: gosnmp.OpaqueFloat, Value: float32(1.5)}, "1.5"},
	}
	for _, c := range cases {
		if got := formatValue(c.pdu); got != c.want {
			t.Errorf("formatValue(%v) = %q, want %q", c.pdu.Value, got, c.want)
		}
	}
}
