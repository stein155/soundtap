// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/wpactrl/wpactrltest"
)

// fakeSupplicant points wpaCtrlPath at a fake control socket answering with handle.
func fakeSupplicant(t *testing.T, handle func(string) string) {
	t.Helper()
	srv := wpactrltest.Listen(t, wpactrltest.ShortDir(t), stationIface, handle)
	old := wpaCtrlPath
	wpaCtrlPath = srv.Path
	t.Cleanup(func() { wpaCtrlPath = old })
}

// The live link comes from wpa_supplicant's control socket: the network and its signal.
func TestNetworkInfoReadsTheLiveLink(t *testing.T) {
	t.Setenv("SOUNDTAP_WIFI_MODE", "scm")
	fakeSupplicant(t, func(cmd string) string {
		switch cmd {
		case "STATUS":
			return "bssid=aa:bb:cc:dd:ee:ff\nssid=Home Net\nid=0\nmode=station\nwpa_state=COMPLETED\n"
		case "SIGNAL_POLL":
			return "RSSI=-55\nLINKSPEED=72\nNOISE=9999\nFREQUENCY=2437\n"
		}
		return ""
	})
	sp := New()
	sp.IP = "192.168.1.42"
	network, err := sp.NetworkInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if network.SSID != "Home Net" || network.Dbm != -55 || network.LinkMbps != 72 || network.MHz != 2437 || network.Signal == "" {
		t.Fatalf("network = %+v", network)
	}
}

func TestNetworkInfoReportsSetupAP(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "setup-ap")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOUNDTAP_SETUP_AP_MARKER", marker)
	network, err := New().NetworkInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if network.SSID != "SoundTap-Setup" || network.IP != "192.168.42.1" {
		t.Fatalf("setup network = %+v", network)
	}
}

func TestParseWpaStatusReportsTheAssociatedSSID(t *testing.T) {
	out := "bssid=aa:bb:cc:dd:ee:ff\nfreq=2412\nssid=HomeNet\nid=0\nmode=station\n" +
		"pairwise_cipher=CCMP\nkey_mgmt=WPA2-PSK\nwpa_state=COMPLETED\nip_address=192.0.2.135\n"
	ssid, ok := parseWpaStatus(out)
	if !ok || ssid != "HomeNet" {
		t.Fatalf("got %q, %v; want HomeNet, true", ssid, ok)
	}
}

// A speaker mid-handshake is not on that network yet. The UI showing it as connected is
// what made a failed join look like a successful one.
func TestParseWpaStatusRejectsAnIncompleteAssociation(t *testing.T) {
	for _, state := range []string{"SCANNING", "ASSOCIATING", "4WAY_HANDSHAKE", "DISCONNECTED", ""} {
		out := "ssid=HomeNet\nmode=station\nwpa_state=" + state + "\n"
		if ssid, ok := parseWpaStatus(out); ok {
			t.Errorf("wpa_state=%q reported as associated with %q", state, ssid)
		}
	}
}

func TestParseWpaStatusHandlesGarbage(t *testing.T) {
	for _, out := range []string{"", "Failed to connect to non-global ctrl_ifname\n", "no equals signs here\n"} {
		if _, ok := parseWpaStatus(out); ok {
			t.Errorf("%q reported as associated", out)
		}
	}
}

func TestParseWpaStatusRejectsAPInterface(t *testing.T) {
	out := "ssid=SoundTap-Setup\nmode=AP\nwpa_state=COMPLETED\n"
	if ssid, ok := parseWpaStatus(out); ok {
		t.Fatalf("AP interface reported as station association with %q", ssid)
	}
}

func TestParseWifiSignalAcceptsNegativeDBMOnly(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want string
	}{
		{"RSSI=-78\nLINKSPEED=54\n", "fair"},
		{"RSSI=73\n", ""}, // percentage is not dBm
		{"RSSI=13\n", ""}, // positive AP-style nonsense
		{"RSSI=-120\n", ""},
		{"RSSI=UNKNOWN\n", ""},
	} {
		if got := parseWifiSignal(tc.out).signal; got != tc.want {
			t.Errorf("parseWifiSignal(%q) = %q, want %q", tc.out, got, tc.want)
		}
	}
}

func TestParseWifiSignalReadsTheLink(t *testing.T) {
	got := parseWifiSignal("RSSI=-58\nLINKSPEED=433\nNOISE=9999\nFREQUENCY=5180\n")
	if want := (wifiSignal{signal: dbmToken(-58), dbm: -58, mbps: 433, mhz: 5180}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got := parseWifiSignal("RSSI=13\nLINKSPEED=54\n"); got != (wifiSignal{}) {
		t.Fatalf("an implausible RSSI kept %+v", got)
	}
}

func TestNetworkInfoHasUnknownSignalWithoutLiveAssociation(t *testing.T) {
	old := wpaCtrlPath
	wpaCtrlPath = filepath.Join(t.TempDir(), "none") // no wpa_supplicant
	t.Cleanup(func() { wpaCtrlPath = old })
	sp := New()
	sp.IP = "192.168.1.42" // maintainIP sets it on a speaker with an address
	network, err := sp.NetworkInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if network.Signal != "" {
		t.Fatalf("signal without live station association = %q, want unknown", network.Signal)
	}
}

func TestParseWpaStatusKeepsSSIDSpaces(t *testing.T) {
	ssid, ok := parseWpaStatus("mode=station\nwpa_state=COMPLETED\nssid= Home \n")
	if !ok || ssid != " Home " {
		t.Fatalf("got %q %v, want \" Home \"", ssid, ok)
	}
}

// An ST20 with its cable in reports Ethernet and the wired address, even with Wi-Fi up.
func TestNetworkInfoPrefersTheCable(t *testing.T) {
	wired := wiredIP
	wiredIP = func() string { return "192.0.2.109" }
	t.Cleanup(func() { wiredIP = wired })
	sp := New()
	sp.IP = "192.0.2.129"
	network, err := sp.NetworkInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if network.Type != "ethernet" || network.IP != "192.0.2.109" {
		t.Fatalf("network = %+v, want ethernet on 192.0.2.109", network)
	}
}

// The speaker runs on its cable only with carrier and an IPv4 lease on eth0.
func TestWiredIPNeedsCarrierAndAddress(t *testing.T) {
	v4 := &net.IPNet{IP: net.IPv4(192, 0, 2, 109), Mask: net.CIDRMask(24, 32)}
	v6 := &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}
	for _, c := range []struct {
		name    string
		carrier bool
		addrs   []net.Addr
		want    string
	}{
		{"carrier and lease", true, []net.Addr{v6, v4}, "192.0.2.109"},
		{"carrier, no lease yet", true, []net.Addr{v6}, ""},
		{"carrier, no addresses", true, nil, ""},
		{"address left after the cable went", false, []net.Addr{v4}, ""},
	} {
		if got := WiredIP(c.carrier, c.addrs); got != c.want {
			t.Errorf("%s: WiredIP = %q, want %q", c.name, got, c.want)
		}
	}
}

// Off the speaker the host's own eth0 never counts as the speaker's cable.
func TestEth0OffTheSpeaker(t *testing.T) {
	t.Setenv("SOUNDTAP_FIRMWARE", "")
	if carrier, addrs := Eth0(); carrier || addrs != nil {
		t.Fatalf("Eth0() = %v %v off the speaker", carrier, addrs)
	}
}
