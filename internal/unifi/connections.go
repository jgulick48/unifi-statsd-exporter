package unifi

import (
	"sort"
	"strings"
)

type portKey struct {
	mac  string
	port int
}

type portDevice struct {
	name   string
	online bool
}

// annotateConnections fills in the Connected fields of each device's PoE
// ports. raws supplies the wired uplink of every device (including ones that
// are not PoE switches); clients supplies the wired clients per switch port.
func annotateConnections(raws []rawDevice, clients []rawClient, devices []Device) {
	byPort := map[portKey][]portDevice{}
	for _, r := range raws {
		if r.Adopted.Set && !r.Adopted.Val {
			continue
		}
		u := r.Uplink
		if u.Type != "wire" || u.UplinkMAC == "" || !u.UplinkRemotePort.Set {
			continue
		}
		name := r.Name
		if name == "" {
			name = r.MAC
		}
		k := portKey{strings.ToLower(u.UplinkMAC), int(u.UplinkRemotePort.Val)}
		byPort[k] = append(byPort[k], portDevice{name: name, online: r.State.Set && r.State.Val == 1})
	}

	clientsByPort := map[portKey][]string{}
	for _, c := range clients {
		if !c.IsWired.Val || c.SwMAC == "" || !c.SwPort.Set {
			continue
		}
		k := portKey{strings.ToLower(c.SwMAC), int(c.SwPort.Val)}
		clientsByPort[k] = append(clientsByPort[k], c.displayName())
	}

	for i := range devices {
		mac := strings.ToLower(devices[i].MAC)
		for j := range devices[i].Ports {
			p := &devices[i].Ports[j]
			k := portKey{mac, p.Index}
			p.Connected, p.ConnectedType, p.ConnectedClients = connectionFor(byPort[k], clientsByPort[k])
		}
	}
}

// connectionFor applies the precedence rules to one port.
func connectionFor(devs []portDevice, clientNames []string) (name, typ string, clients int) {
	clients = len(clientNames)
	sort.Slice(devs, func(a, b int) bool { return devs[a].name < devs[b].name })
	for _, d := range devs {
		if d.online {
			return d.name, "device", clients
		}
	}
	switch {
	case clients == 1:
		return clientNames[0], "client", clients
	case clients > 1:
		return "multiple clients", "multiple", clients
	case len(devs) > 0:
		return devs[0].name + " (offline)", "offline_device", clients
	}
	return "", "", clients
}
