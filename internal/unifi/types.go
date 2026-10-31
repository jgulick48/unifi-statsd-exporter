// Package unifi polls a UniFi OS console's Network API for device power data.
package unifi

// Device is the normalized view of one adopted UniFi device. Pointer fields
// are nil when the device does not report that value.
type Device struct {
	MAC   string
	Name  string
	Model string
	// Type is the UniFi device type, e.g. "usw", "uap", "udm", "usp".
	Type string
	// Online is true when the controller reports the device as connected.
	Online bool

	// PoEBudgetWatts is the total PoE power the device can supply.
	PoEBudgetWatts *float64
	// PoEUsedWatts is the total PoE power currently being supplied.
	PoEUsedWatts *float64

	// OutletPowerWatts is the total AC power drawn across all outlets (PDUs).
	OutletPowerWatts *float64
	// OutletBudgetWatts is the AC power budget across all outlets (PDUs).
	OutletBudgetWatts *float64

	// Ports holds PoE-capable ports only.
	Ports []PoEPort
	// Outlets holds PDU / smart power outlets.
	Outlets []Outlet
}

type PoEPort struct {
	Index int
	Name  string
	Up    bool
	// PoEEnabled is true when the port is configured to supply PoE.
	PoEEnabled bool
	// PoEGood is true when a powered device is negotiated and drawing power.
	PoEGood bool
	PoEMode string
	// PoEClass is the negotiated class reported by the switch, e.g. "Class 4".
	PoEClass     string
	PowerWatts   float64
	VoltageVolts float64
	CurrentAmps  float64
}

type Outlet struct {
	Index   int
	Name    string
	RelayOn bool
	// Pointer fields are nil for outlets without metering.
	PowerWatts   *float64
	VoltageVolts *float64
	CurrentAmps  *float64
	PowerFactor  *float64
}
