package snmp

import "strings"

const (
	oidSnmpTrapOID = "1.3.6.1.6.3.1.1.4.1.0"
	oidSysUpTime   = "1.3.6.1.2.1.1.3.0"
	oidUbntPrefix  = "1.3.6.1.4.1.41112"
)

var trapNames = map[string]string{
	"1.3.6.1.6.3.1.1.5.1": "coldStart",
	"1.3.6.1.6.3.1.1.5.2": "warmStart",
	"1.3.6.1.6.3.1.1.5.3": "linkDown",
	"1.3.6.1.6.3.1.1.5.4": "linkUp",
	"1.3.6.1.6.3.1.1.5.5": "authenticationFailure",
	"1.3.6.1.2.1.105.0.1": "pethPsePortOnOffNotification",
	"1.3.6.1.2.1.105.0.2": "pethMainPowerUsageOnNotification",
	"1.3.6.1.2.1.105.0.3": "pethMainPowerUsageOffNotification",
}

// varbindPrefixes maps well-known varbind column OIDs to readable key prefixes.
var varbindPrefixes = []struct{ oid, name string }{
	{"1.3.6.1.2.1.2.2.1.1", "ifIndex"},
	{"1.3.6.1.2.1.2.2.1.2", "ifDescr"},
	{"1.3.6.1.2.1.31.1.1.1.1", "ifName"},
	{"1.3.6.1.2.1.105.1.1.1.6", "pethPsePortDetectionStatus"},
}

// normalizeOID strips any leading dot.
func normalizeOID(oid string) string {
	return strings.TrimPrefix(oid, ".")
}

// TrapName resolves a trap OID to a human-readable name, or returns the OID
// itself when unknown.
func TrapName(oid string) string {
	oid = normalizeOID(oid)
	if n, ok := trapNames[oid]; ok {
		return n
	}
	if rest, ok := strings.CutPrefix(oid, oidUbntPrefix+"."); ok {
		return "ubnt." + rest
	}
	if oid == oidUbntPrefix {
		return "ubnt"
	}
	return oid
}

// variableKey returns the map key for a varbind OID: a readable name for
// well-known columns, otherwise the normalized OID.
func variableKey(oid string) string {
	oid = normalizeOID(oid)
	for _, p := range varbindPrefixes {
		if rest, ok := strings.CutPrefix(oid, p.oid+"."); ok {
			return p.name + "." + rest
		}
	}
	return oid
}
