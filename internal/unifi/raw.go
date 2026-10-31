package unifi

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// flexFloat decodes a JSON number, numeric string, empty string or null.
// Set is false when the value was absent, null, empty or unparseable; one
// odd field (e.g. "N/A") must not fail the whole device list.
type flexFloat struct {
	Val float64
	Set bool
}

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	*f = flexFloat{}
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
		if s == "" {
			return nil
		}
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		*f = flexFloat{Val: v, Set: true}
	}
	return nil
}

// Ptr returns a pointer to the value, or nil when absent.
func (f flexFloat) Ptr() *float64 {
	if !f.Set {
		return nil
	}
	v := f.Val
	return &v
}

// Scaled returns a pointer to the value multiplied by m, or nil when absent.
func (f flexFloat) Scaled(m float64) *float64 {
	if !f.Set {
		return nil
	}
	v := f.Val * m
	return &v
}

// flexBool decodes true/false, 0/1, "true"/"false"/"1"/"0", "" and null.
// Unparseable values are treated as absent.
type flexBool struct {
	Val bool
	Set bool
}

func (f *flexBool) UnmarshalJSON(b []byte) error {
	*f = flexBool{}
	s := strings.ToLower(strings.TrimSpace(string(b)))
	if s == "null" || s == "" {
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = strings.ToLower(strings.TrimSpace(str))
		if s == "" {
			return nil
		}
	}
	switch s {
	case "true", "yes", "on":
		*f = flexBool{Val: true, Set: true}
		return nil
	case "false", "no", "off":
		*f = flexBool{Val: false, Set: true}
		return nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		*f = flexBool{Val: n != 0, Set: true}
	}
	return nil
}

// flexString decodes a string, or a number rendered as a string.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if string(b) == "null" {
		*f = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	*f = flexString(b)
	return nil
}

type rawResponse struct {
	Meta struct {
		RC  string `json:"rc"`
		Msg string `json:"msg"`
	} `json:"meta"`
	Data json.RawMessage `json:"data"`
}

type rawDevice struct {
	MAC     string    `json:"mac"`
	Name    string    `json:"name"`
	Model   string    `json:"model"`
	Type    string    `json:"type"`
	State   flexFloat `json:"state"`
	Adopted flexBool  `json:"adopted"`

	TotalMaxPower  flexFloat `json:"total_max_power"`
	TotalUsedPower flexFloat `json:"total_used_power"`

	OutletACPower  flexFloat `json:"outlet_ac_power_consumption"`
	OutletACBudget flexFloat `json:"outlet_ac_power_budget"`

	Uplink rawUplink `json:"uplink"`

	PortTable   []rawPort   `json:"port_table"`
	OutletTable []rawOutlet `json:"outlet_table"`
}

type rawUplink struct {
	Type             string    `json:"type"`
	UplinkMAC        string    `json:"uplink_mac"`
	UplinkRemotePort flexFloat `json:"uplink_remote_port"`
}

// rawClient is one currently connected client from stat/sta.
type rawClient struct {
	MAC      string    `json:"mac"`
	Name     string    `json:"name"`
	Hostname string    `json:"hostname"`
	IsWired  flexBool  `json:"is_wired"`
	SwMAC    string    `json:"sw_mac"`
	SwPort   flexFloat `json:"sw_port"`
}

// displayName prefers the user alias, then the hostname, then the MAC.
func (c rawClient) displayName() string {
	for _, s := range []string{c.Name, c.Hostname, c.MAC} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

type rawPort struct {
	PortIdx    flexFloat  `json:"port_idx"`
	Name       string     `json:"name"`
	Up         flexBool   `json:"up"`
	PortPoE    flexBool   `json:"port_poe"`
	PoEEnable  flexBool   `json:"poe_enable"`
	PoEGood    flexBool   `json:"poe_good"`
	PoEMode    flexString `json:"poe_mode"`
	PoEClass   flexString `json:"poe_class"`
	PoEPower   flexFloat  `json:"poe_power"`
	PoEVoltage flexFloat  `json:"poe_voltage"`
	PoECurrent flexFloat  `json:"poe_current"` // milliamps
}

type rawOutlet struct {
	Index       flexFloat `json:"index"`
	Name        string    `json:"name"`
	RelayState  flexBool  `json:"relay_state"`
	Power       flexFloat `json:"outlet_power"`
	Voltage     flexFloat `json:"outlet_voltage"`
	Current     flexFloat `json:"outlet_current"` // amps
	PowerFactor flexFloat `json:"outlet_power_factor"`
}

// toDevice maps a raw device; ok is false for unadopted devices.
func (r rawDevice) toDevice() (Device, bool) {
	if r.Adopted.Set && !r.Adopted.Val {
		return Device{}, false
	}
	d := Device{
		MAC:               r.MAC,
		Name:              r.Name,
		Model:             r.Model,
		Type:              r.Type,
		Online:            r.State.Set && r.State.Val == 1,
		PoEBudgetWatts:    r.TotalMaxPower.Ptr(),
		PoEUsedWatts:      r.TotalUsedPower.Ptr(),
		OutletPowerWatts:  r.OutletACPower.Ptr(),
		OutletBudgetWatts: r.OutletACBudget.Ptr(),
	}
	if d.Name == "" {
		d.Name = d.MAC
	}
	for _, p := range r.PortTable {
		if !p.PortPoE.Val {
			continue
		}
		port := PoEPort{
			Index:        int(p.PortIdx.Val),
			Name:         p.Name,
			Up:           p.Up.Val,
			PoEEnabled:   p.PoEEnable.Val,
			PoEGood:      p.PoEGood.Val,
			PoEMode:      string(p.PoEMode),
			PoEClass:     string(p.PoEClass),
			PowerWatts:   p.PoEPower.Val,
			VoltageVolts: p.PoEVoltage.Val,
			CurrentAmps:  p.PoECurrent.Val / 1000,
		}
		d.Ports = append(d.Ports, port)
	}
	for _, o := range r.OutletTable {
		d.Outlets = append(d.Outlets, Outlet{
			Index:        int(o.Index.Val),
			Name:         o.Name,
			RelayOn:      o.RelayState.Val,
			PowerWatts:   o.Power.Ptr(),
			VoltageVolts: o.Voltage.Ptr(),
			CurrentAmps:  o.Current.Ptr(),
			PowerFactor:  o.PowerFactor.Ptr(),
		})
	}
	return d, true
}
