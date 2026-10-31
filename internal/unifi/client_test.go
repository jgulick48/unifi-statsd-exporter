package unifi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jgulick48/unifi-statsd-exporter/internal/config"
)

const (
	statPath    = "/proxy/network/api/s/default/stat/device"
	staPath     = "/proxy/network/api/s/default/stat/sta"
	numAdopted  = 7
	udmMAC      = "aa:bb:cc:00:00:01"
	otherSwitch = "de:ad:be:ef:00:00"
)

func staFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/stat_sta.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/stat_device.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTestClient(t *testing.T, srv *httptest.Server, mod func(*config.UniFi)) *Client {
	t.Helper()
	cfg := config.UniFi{URL: srv.URL + "/", Site: "default", InsecureSkipVerify: true, Timeout: 5 * time.Second}
	mod(&cfg)
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAPIKey(t *testing.T) {
	var logins int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			atomic.AddInt32(&logins, 1)
		}
		if r.Header.Get("X-API-KEY") != "secret" || r.Header.Get("Accept") != "application/json" {
			http.Error(w, "bad", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case statPath:
			w.Write(fixture(t))
		case staPath:
			w.Write(staFixture(t))
		default:
			http.Error(w, "bad", http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv, func(c *config.UniFi) { c.APIKey = "secret" })
	devs, err := c.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != numAdopted || logins != 0 {
		t.Fatalf("devs=%d logins=%d", len(devs), logins)
	}
	if err := c.LastClientsError(); err != nil {
		t.Fatalf("clients error: %v", err)
	}
	if got := devs[0].Ports[0].Connected; got != "AP Office" {
		t.Fatalf("connection info missing, got %q", got)
	}
}

func TestLoginCookieAndCSRF(t *testing.T) {
	var logins int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			atomic.AddInt32(&logins, 1)
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["username"] != "u" || body["password"] != "p" || body["remember"] != true {
				http.Error(w, "bad", http.StatusBadRequest)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: "jwt", Path: "/"})
			w.Header().Set("X-Csrf-Token", "csrf1")
			w.Write([]byte("{}"))
		case statPath, staPath:
			ck, err := r.Cookie("TOKEN")
			if err != nil || ck.Value != "jwt" || r.Header.Get("X-Csrf-Token") != "csrf1" {
				http.Error(w, "unauth", http.StatusUnauthorized)
				return
			}
			if r.URL.Path == staPath {
				w.Write(staFixture(t))
				return
			}
			w.Write(fixture(t))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv, func(c *config.UniFi) { c.Username, c.Password = "u", "p" })
	for i := 0; i < 2; i++ {
		if _, err := c.Devices(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := c.LastClientsError(); err != nil {
			t.Fatalf("clients error: %v", err)
		}
	}
	if logins != 1 {
		t.Fatalf("logins=%d, want 1", logins)
	}
}

func TestReloginOn401(t *testing.T) {
	var logins, data int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			n := atomic.AddInt32(&logins, 1)
			w.Header().Set("X-Csrf-Token", "csrf"+string(rune('0'+n)))
			w.Write([]byte("{}"))
		case statPath:
			n := atomic.AddInt32(&data, 1)
			if n == 1 {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			if r.Header.Get("X-Csrf-Token") != "csrf2" {
				t.Errorf("csrf = %q", r.Header.Get("X-Csrf-Token"))
			}
			w.Write(fixture(t))
		case staPath:
			if r.Header.Get("X-Csrf-Token") != "csrf2" {
				t.Errorf("sta csrf = %q", r.Header.Get("X-Csrf-Token"))
			}
			w.Write(staFixture(t))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv, func(c *config.UniFi) { c.Username, c.Password = "u", "p" })
	if _, err := c.Devices(context.Background()); err != nil {
		t.Fatal(err)
	}
	if logins != 2 || data != 2 {
		t.Fatalf("logins=%d data=%d", logins, data)
	}
}

func TestPersistent401OnlyRetriesOnce(t *testing.T) {
	var logins, data int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			atomic.AddInt32(&logins, 1)
			w.Write([]byte("{}"))
			return
		}
		atomic.AddInt32(&data, 1)
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := newTestClient(t, srv, func(c *config.UniFi) { c.Username, c.Password = "u", "p" })
	if _, err := c.Devices(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if logins != 2 || data != 2 {
		t.Fatalf("logins=%d data=%d", logins, data)
	}
}

func TestMetaError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"meta":{"rc":"error","msg":"api.err.NoSiteContext"},"data":[]}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, func(c *config.UniFi) { c.APIKey = "k" })
	_, err := c.Devices(context.Background())
	if err == nil || !strings.Contains(err.Error(), "api.err.NoSiteContext") {
		t.Fatalf("err = %v", err)
	}
}

func TestHTTPError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := newTestClient(t, srv, func(c *config.UniFi) { c.APIKey = "k" })
	if _, err := c.Devices(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestMapping(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, func(c *config.UniFi) { c.APIKey = "k" })
	devs, err := c.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != numAdopted {
		t.Fatalf("got %d devices, want %d (unadopted excluded)", len(devs), numAdopted)
	}

	udm := devs[0]
	if udm.Type != "udm" || !udm.Online || udm.Name != "UDM-SE" {
		t.Fatalf("udm: %+v", udm)
	}
	if udm.PoEBudgetWatts == nil || *udm.PoEBudgetWatts != 180 || udm.PoEUsedWatts == nil || *udm.PoEUsedWatts != 35.5 {
		t.Fatalf("udm poe totals: %v %v", udm.PoEBudgetWatts, udm.PoEUsedWatts)
	}
	if udm.OutletPowerWatts != nil {
		t.Fatal("outlet power should be nil")
	}
	if len(udm.Ports) != 6 {
		t.Fatalf("ports = %d, want 6 (non-PoE excluded)", len(udm.Ports))
	}
	p := udm.Ports[0]
	if p.Index != 2 || !p.Up || !p.PoEEnabled || !p.PoEGood || p.PoEClass != "Class 4" || p.PoEMode != "auto" ||
		p.PowerWatts != 3.52 || p.VoltageVolts != 52.10 || p.CurrentAmps != 0.0675 {
		t.Fatalf("port: %+v", p)
	}
	if q := udm.Ports[1]; q.Up || !q.PoEEnabled || q.PoEGood {
		t.Fatalf("port 3: %+v", q)
	}

	usw := devs[1]
	if usw.Name != usw.MAC {
		t.Fatalf("name fallback: %q", usw.Name)
	}
	if usw.PoEUsedWatts != nil {
		t.Fatal("used power should be nil when absent")
	}
	if len(usw.Ports) != 1 || usw.Ports[0].CurrentAmps != 0.15 {
		t.Fatalf("usw ports: %+v", usw.Ports)
	}

	pdu := devs[2]
	if pdu.Online {
		t.Fatal("pdu should be offline")
	}
	if pdu.OutletPowerWatts == nil || *pdu.OutletPowerWatts != 120.5 || pdu.OutletBudgetWatts == nil || *pdu.OutletBudgetWatts != 1440 {
		t.Fatalf("pdu totals")
	}
	if len(pdu.Outlets) != 2 {
		t.Fatalf("outlets: %+v", pdu.Outlets)
	}
	o := pdu.Outlets[0]
	if !o.RelayOn || o.PowerWatts == nil || *o.PowerWatts != 60.2 || *o.VoltageVolts != 120.1 || *o.CurrentAmps != 0.5 || *o.PowerFactor != 0.98 {
		t.Fatalf("outlet 1: %+v", o)
	}
	o2 := pdu.Outlets[1]
	if o2.RelayOn || o2.PowerWatts != nil || o2.VoltageVolts != nil || o2.CurrentAmps != nil || o2.PowerFactor != nil {
		t.Fatalf("outlet 2: %+v", o2)
	}
}

func TestFlexTypes(t *testing.T) {
	var v struct {
		A, B, C, D, E flexFloat
		F, G, H, I, J flexBool
	}
	in := `{"A":1.5,"B":"2.5","C":"","D":null,"F":1,"G":"true","H":"0","I":false,"J":""}`
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	if !v.A.Set || v.A.Val != 1.5 || v.B.Val != 2.5 || v.C.Set || v.D.Set || v.E.Set {
		t.Fatalf("floats: %+v", v)
	}
	if !v.F.Val || !v.G.Val || v.H.Val || !v.H.Set || v.I.Val || v.J.Set {
		t.Fatalf("bools: %+v", v)
	}
}

func TestConnections(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case statPath:
			w.Write(fixture(t))
		case staPath:
			w.Write(staFixture(t))
		}
	}))
	defer srv.Close()
	c := newTestClient(t, srv, func(c *config.UniFi) { c.APIKey = "k" })
	devs, err := c.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	udm, usw := devs[0], devs[1]

	cases := []struct {
		name    string
		port    PoEPort
		want    string
		wantTyp string
		clients int
	}{
		{"online AP wins, wireless client ignored", udm.Ports[0], "AP Office", "device", 0},
		{"single client, name trimmed", udm.Ports[1], "Driveway Cam", "client", 1},
		{"many clients behind unmanaged switch", udm.Ports[2], "multiple clients", "multiple", 11},
		{"client beats offline AP", udm.Ports[3], "Laptop", "client", 1},
		{"offline AP alone", udm.Ports[4], "AP Old Garage (offline)", "offline_device", 0},
		{"wireless uplink ignored", udm.Ports[5], "", "", 0},
		{"switch MAC case-insensitive, hostname fallback", usw.Ports[0], "nas", "client", 1},
	}
	for _, tc := range cases {
		if tc.port.Connected != tc.want || tc.port.ConnectedType != tc.wantTyp || tc.port.ConnectedClients != tc.clients {
			t.Errorf("%s: port %d = %q/%q/%d, want %q/%q/%d", tc.name, tc.port.Index,
				tc.port.Connected, tc.port.ConnectedType, tc.port.ConnectedClients, tc.want, tc.wantTyp, tc.clients)
		}
	}
}

func TestClientsFailureKeepsDevices(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == staPath {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Write(fixture(t))
	}))
	defer srv.Close()
	c := newTestClient(t, srv, func(c *config.UniFi) { c.APIKey = "k" })
	devs, err := c.Devices(context.Background())
	if err != nil {
		t.Fatalf("devices must not fail: %v", err)
	}
	if len(devs) != numAdopted {
		t.Fatalf("devices = %d", len(devs))
	}
	if c.LastClientsError() == nil {
		t.Fatal("expected LastClientsError")
	}
	for _, p := range devs[0].Ports {
		if p.Connected != "" || p.ConnectedType != "" || p.ConnectedClients != 0 {
			t.Fatalf("port %d annotated despite failure: %+v", p.Index, p)
		}
	}
}

func TestAnnotateDeterministicAndEdgeCases(t *testing.T) {
	raws := []rawDevice{
		{MAC: "b", Name: "Zeta", State: flexFloat{1, true}, Uplink: rawUplink{"wire", udmMAC, flexFloat{1, true}}},
		{MAC: "c", Name: "Alpha", State: flexFloat{1, true}, Uplink: rawUplink{"wire", udmMAC, flexFloat{1, true}}},
		{MAC: "d", Name: "", State: flexFloat{0, true}, Uplink: rawUplink{"wire", udmMAC, flexFloat{2, true}}},
	}
	clients := []rawClient{
		{MAC: "m1", IsWired: flexBool{true, true}, SwMAC: otherSwitch, SwPort: flexFloat{1, true}},
		{MAC: "m2", Name: " ", Hostname: "", IsWired: flexBool{true, true}, SwMAC: udmMAC, SwPort: flexFloat{3, true}},
	}
	devs := []Device{{MAC: udmMAC, Ports: []PoEPort{{Index: 1}, {Index: 2}, {Index: 3}}}}
	annotateConnections(raws, clients, devs)
	got := devs[0].Ports
	if got[0].Connected != "Alpha" || got[0].ConnectedType != "device" {
		t.Errorf("port 1: %+v", got[0])
	}
	if got[1].Connected != "d (offline)" || got[1].ConnectedType != "offline_device" {
		t.Errorf("port 2: %+v", got[1])
	}
	if got[2].Connected != "m2" || got[2].ConnectedType != "client" {
		t.Errorf("port 3: %+v", got[2])
	}
}
