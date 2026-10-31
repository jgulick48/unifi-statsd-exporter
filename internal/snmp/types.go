// Package snmp receives SNMP v1/v2c traps from UniFi devices.
package snmp

import "time"

type Trap struct {
	// Source is the IP address the trap was received from.
	Source string
	// OID is the snmpTrapOID (v2c) or the enterprise/generic/specific
	// equivalent (v1), in dotted form without a leading dot.
	OID string
	// Name is a short human-readable name for well-known OIDs (e.g.
	// "linkDown", "pethPsePortOnOffNotification"), or the OID otherwise.
	Name string
	// Variables maps varbind OIDs to their string-formatted values.
	Variables map[string]string
	Received  time.Time
}

// Handler is called for every accepted trap.
type Handler func(Trap)
