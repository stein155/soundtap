// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stein155/soundtap/app/internal/web"
)

func TestDevicesEndpointListsSelfAndPeers(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	srv.SetDevice(web.Device{ID: "SN-other", Name: "Keuken", IP: "192.168.1.5", Port: 80})
	rec := do(t, srv.Handler(), "GET", "/api/devices", "")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Devices []struct {
			ID       string `json:"id"`
			Self     bool   `json:"self"`
			Firmware string `json:"firmware"`
			Online   bool   `json:"online"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var sawPeer, sawSelf bool
	for _, d := range body.Devices {
		if d.ID == "SN-other" {
			sawPeer = true
			if !d.Online {
				t.Fatal("a speaker that never said goodbye is reported offline")
			}
		}
		if d.Self {
			sawSelf = true
			if d.Firmware != "test" {
				t.Fatalf("this speaker reports firmware %q, want its own version", d.Firmware)
			}
		}
	}
	if !sawPeer || !sawSelf {
		t.Fatalf("devices = %s, want this speaker and its peer", rec.Body.String())
	}
}

// Peers proxy to the port listed here; on the ST20, 80 is the BCO's web server.
func TestDevicesEndpointReportsTheAdvertisedPort(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	srv.ReconfigureForTest(func(o *web.Options) { o.Speaker.Port = 8080 })
	rec := do(t, srv.Handler(), "GET", "/api/devices", "")
	var body struct {
		Devices []struct {
			Self bool `json:"self"`
			Port int  `json:"port"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, d := range body.Devices {
		if d.Self && d.Port != 8080 {
			t.Fatalf("self port = %d, want 8080", d.Port)
		}
	}
}

// Any LAN host can announce ids; the registry keeps a fixed number.
func TestDeviceRegistryIsBounded(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	for i := range 200 {
		srv.SetDevice(web.Device{ID: fmt.Sprintf("SN-%d", i), IP: fmt.Sprintf("192.168.1.%d", i+1), Port: 80})
	}
	rec := do(t, srv.Handler(), "GET", "/api/devices", "")
	var body struct {
		Devices []json.RawMessage `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if n := len(body.Devices); n > 65 {
		t.Fatalf("%d devices listed, want at most 64 peers and this speaker", n)
	}
}
