// Package metrics converts UniFi device state and SNMP traps into DogStatsD
// metrics. Metric names are emitted without the namespace; the statsd client
// applies it.
package metrics

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"

	"github.com/jgulick48/unifi-statsd-exporter/internal/config"
	"github.com/jgulick48/unifi-statsd-exporter/internal/snmp"
	"github.com/jgulick48/unifi-statsd-exporter/internal/unifi"
)

// Reporter emits metrics through a DogStatsD client.
type Reporter struct {
	client statsd.ClientInterface
	site   string
}

// New creates a Reporter with a real statsd client. The caller owns the
// returned client and must Close it.
func New(cfg config.Statsd, site string) (*Reporter, statsd.ClientInterface, error) {
	client, err := statsd.New(cfg.Address,
		statsd.WithNamespace(cfg.Namespace),
		statsd.WithTags(cfg.Tags),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating statsd client: %w", err)
	}
	return NewWithClient(client, site), client, nil
}

// NewWithClient creates a Reporter using the supplied client.
func NewWithClient(client statsd.ClientInterface, site string) *Reporter {
	return &Reporter{client: client, site: site}
}

var tagReplacer = strings.NewReplacer(",", "_", "|", "_", "#", "_", "\n", "_", "\r", "_")

// sanitize makes a tag value safe for the DogStatsD wire format. Colons are
// kept: Telegraf splits datadog tags on the first colon only, so MACs survive.
func sanitize(v string) string {
	return strings.TrimSpace(tagReplacer.Replace(v))
}

func tag(k, v string) string { return k + ":" + sanitize(v) }

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (r *Reporter) gauge(name string, v float64, tags []string) {
	if err := r.client.Gauge(name, v, tags, 1); err != nil {
		log.Printf("metrics: sending gauge %s: %v", name, err)
	}
}

func extend(base []string, extra ...string) []string {
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	return append(out, extra...)
}

// ReportDevices emits per-device, per-port, per-outlet and site total gauges.
func (r *Reporter) ReportDevices(devices []unifi.Device) {
	var sitePoE, siteOutlet float64
	for _, d := range devices {
		tags := []string{
			tag("site", r.site),
			tag("device", d.Name),
			tag("mac", d.MAC),
			tag("model", d.Model),
			tag("device_type", d.Type),
		}
		r.gauge("device.online", b2f(d.Online), tags)
		if d.PoEBudgetWatts != nil {
			r.gauge("device.poe.budget_watts", *d.PoEBudgetWatts, tags)
		}
		if d.PoEUsedWatts != nil {
			r.gauge("device.poe.used_watts", *d.PoEUsedWatts, tags)
		}
		if d.OutletPowerWatts != nil {
			r.gauge("device.outlet.power_watts", *d.OutletPowerWatts, tags)
		}
		if d.OutletBudgetWatts != nil {
			r.gauge("device.outlet.budget_watts", *d.OutletBudgetWatts, tags)
		}

		var portSum float64
		active := 0
		for _, p := range d.Ports {
			portSum += p.PowerWatts
			if p.PoEGood {
				active++
			}
			pt := extend(tags, "port:"+strconv.Itoa(p.Index))
			if n := sanitize(p.Name); n != "" {
				pt = append(pt, "port_name:"+n)
			}
			if m := sanitize(p.PoEMode); m != "" {
				pt = append(pt, "poe_mode:"+m)
			}
			if c := sanitize(p.PoEClass); c != "" {
				pt = append(pt, "poe_class:"+c)
			}
			r.gauge("poe.power_watts", p.PowerWatts, pt)
			r.gauge("poe.voltage_volts", p.VoltageVolts, pt)
			r.gauge("poe.current_amps", p.CurrentAmps, pt)
			r.gauge("poe.enabled", b2f(p.PoEEnabled), pt)
			r.gauge("poe.good", b2f(p.PoEGood), pt)
			r.gauge("port.up", b2f(p.Up), pt)
		}
		if len(d.Ports) > 0 {
			r.gauge("device.poe.ports_active", float64(active), tags)
		}
		if d.PoEUsedWatts != nil {
			sitePoE += *d.PoEUsedWatts
		} else {
			sitePoE += portSum
		}

		var outletSum float64
		for _, o := range d.Outlets {
			if o.PowerWatts != nil {
				outletSum += *o.PowerWatts
			}
			ot := extend(tags, "outlet:"+strconv.Itoa(o.Index))
			if n := sanitize(o.Name); n != "" {
				ot = append(ot, "outlet_name:"+n)
			}
			r.gauge("pdu.outlet.relay_on", b2f(o.RelayOn), ot)
			if o.PowerWatts != nil {
				r.gauge("pdu.outlet.power_watts", *o.PowerWatts, ot)
			}
			if o.VoltageVolts != nil {
				r.gauge("pdu.outlet.voltage_volts", *o.VoltageVolts, ot)
			}
			if o.CurrentAmps != nil {
				r.gauge("pdu.outlet.current_amps", *o.CurrentAmps, ot)
			}
			if o.PowerFactor != nil {
				r.gauge("pdu.outlet.power_factor", *o.PowerFactor, ot)
			}
		}
		if d.OutletPowerWatts != nil {
			siteOutlet += *d.OutletPowerWatts
		} else {
			siteOutlet += outletSum
		}
	}
	st := []string{tag("site", r.site)}
	r.gauge("site.poe.used_watts", sitePoE, st)
	r.gauge("site.outlet.power_watts", siteOutlet, st)
	r.gauge("site.devices", float64(len(devices)), st)
}

// ReportPoll records the duration and outcome of one poll cycle.
func (r *Reporter) ReportPoll(d time.Duration, err error) {
	tags := []string{tag("site", r.site)}
	if e := r.client.Timing("collector.poll_duration_ms", d, tags, 1); e != nil {
		log.Printf("metrics: sending poll timing: %v", e)
	}
	result := "success"
	if err != nil {
		result = "error"
	}
	if e := r.client.Count("collector.poll", 1, extend(tags, "result:"+result), 1); e != nil {
		log.Printf("metrics: sending poll count: %v", e)
	}
}

// ReportTrap emits a counter and an event for one SNMP trap.
func (r *Reporter) ReportTrap(t snmp.Trap) {
	tags := []string{tag("source", t.Source), tag("trap", t.Name), tag("oid", t.OID)}
	if err := r.client.Count("snmp.trap", 1, tags, 1); err != nil {
		log.Printf("metrics: sending trap count: %v", err)
	}

	keys := make([]string, 0, len(t.Variables))
	for k := range t.Variables {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+t.Variables[k])
	}

	ev := statsd.NewEvent(fmt.Sprintf("UniFi SNMP trap: %s from %s", t.Name, t.Source), strings.Join(lines, "\n"))
	ev.AlertType = statsd.Info
	switch t.Name {
	case "linkDown", "pethPsePortOnOffNotification", "pethMainPowerUsageOffNotification":
		ev.AlertType = statsd.Warning
	}
	ev.SourceTypeName = "snmp"
	ev.Tags = tags
	if err := r.client.Event(ev); err != nil {
		log.Printf("metrics: sending trap event: %v", err)
	}
}
