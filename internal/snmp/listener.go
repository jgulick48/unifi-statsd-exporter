package snmp

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"

	"github.com/jgulick48/unifi-statsd-exporter/internal/config"
)

// Listener receives SNMP v1/v2c traps and passes them to a Handler.
type Listener struct {
	cfg     config.SNMPTraps
	handler Handler
}

// NewListener creates a Listener; call Run to start it.
func NewListener(cfg config.SNMPTraps, h Handler) *Listener {
	return &Listener{cfg: cfg, handler: h}
}

// Run listens on the configured UDP address until ctx is cancelled, then
// closes the socket and returns nil. It returns an error if binding fails.
func (l *Listener) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}

	// Equivalent to a copy of gosnmp.Default; built field by field because
	// GoSNMP contains a noCopy atomic and cannot be copied (go vet).
	d := gosnmp.Default
	params := &gosnmp.GoSNMP{
		Port:               d.Port,
		Transport:          d.Transport,
		Community:          d.Community,
		Version:            d.Version,
		Timeout:            d.Timeout,
		Retries:            d.Retries,
		ExponentialTimeout: d.ExponentialTimeout,
		MaxOids:            d.MaxOids,
	}
	tl := gosnmp.NewTrapListener()
	tl.Params = params
	tl.OnNewTrap = l.onTrap

	errCh := make(chan error, 1)
	go func() { errCh <- tl.Listen(l.cfg.ListenAddress) }()

	// Listen blocks and only signals Listening() after a successful bind. On
	// bind failure it returns an error without signalling. Close must only be
	// called after Listening() fires, otherwise it can hang the read loop.
	select {
	case err := <-errCh:
		if err == nil {
			err = fmt.Errorf("snmp trap listener on %s exited unexpectedly", l.cfg.ListenAddress)
		}
		return fmt.Errorf("snmp trap listen on %s: %w", l.cfg.ListenAddress, err)
	case <-tl.Listening():
	}

	log.Printf("snmp: listening for traps on %s", l.cfg.ListenAddress)

	select {
	case <-ctx.Done():
		tl.Close()
		<-errCh
		return nil
	case err := <-errCh:
		return fmt.Errorf("snmp trap listener stopped: %w", err)
	}
}

func (l *Listener) onTrap(p *gosnmp.SnmpPacket, addr *net.UDPAddr) {
	if p == nil || addr == nil {
		return
	}
	if l.cfg.Community != "" && p.Community != l.cfg.Community {
		log.Printf("snmp: dropping trap from %s: community mismatch", addr.IP)
		return
	}
	if p.Version != gosnmp.Version1 && p.Version != gosnmp.Version2c {
		return
	}
	trap := buildTrap(p, addr)
	if l.handler == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("snmp: panic in trap handler: %v", r)
		}
	}()
	l.handler(trap)
}

func buildTrap(p *gosnmp.SnmpPacket, addr *net.UDPAddr) Trap {
	t := Trap{
		Source:    addr.IP.String(),
		Variables: make(map[string]string, len(p.Variables)),
		Received:  time.Now(),
	}
	if p.Version == gosnmp.Version1 {
		t.OID = v1TrapOID(p)
	}
	for _, v := range p.Variables {
		name := normalizeOID(v.Name)
		if p.Version != gosnmp.Version1 {
			switch name {
			case oidSnmpTrapOID:
				t.OID = normalizeOID(formatValue(v))
				continue
			case oidSysUpTime:
				continue
			}
		}
		t.Variables[variableKey(name)] = formatValue(v)
	}
	t.Name = TrapName(t.OID)
	return t
}

func v1TrapOID(p *gosnmp.SnmpPacket) string {
	if p.GenericTrap >= 0 && p.GenericTrap <= 5 {
		return "1.3.6.1.6.3.1.1.5." + strconv.Itoa(p.GenericTrap+1)
	}
	return normalizeOID(p.Enterprise) + ".0." + strconv.Itoa(p.SpecificTrap)
}

// formatValue renders a varbind value as a string.
func formatValue(v gosnmp.SnmpPDU) string {
	switch val := v.Value.(type) {
	case nil:
		return ""
	case []byte:
		return formatOctets(val)
	case string:
		if v.Type == gosnmp.ObjectIdentifier {
			return normalizeOID(val)
		}
		return val
	default:
		return fmt.Sprint(val)
	}
}

func formatOctets(b []byte) string {
	if utf8.Valid(b) {
		printable := true
		for _, r := range string(b) {
			if !unicode.IsPrint(r) && r != '\n' && r != '\t' {
				printable = false
				break
			}
		}
		if printable {
			return string(b)
		}
	}
	return hex.EncodeToString(b)
}
