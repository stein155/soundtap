// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
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

	if network.SSID != "Home Net" || network.Dbm != -55 || network.LinkMbps != 72 || network.MHz != 2437 ||
		network.Signal == "" {
		t.Fatalf("network = %+v", network)
	}
}

// The ST20's wlan0 is the soundtap_bco driver's: its station info carries the signal and
// wpa_supplicant knows the channel, but the BCO reports no transmit rate, so SIGNAL_POLL has no
// LINKSPEED. The rate is then unknown and left out of the JSON (linkMbps is optional in the API,
// ADR 0108), while the signal and channel stay.
func TestNetworkInfoWithoutLinkRateLeavesItOut(t *testing.T) {
	t.Setenv("SOUNDTAP_WIFI_MODE", "scm")
	fakeSupplicant(t, func(cmd string) string {
		switch cmd {
		case "STATUS":
			return "bssid=aa:bb:cc:dd:ee:ff\nssid=Keuken\nid=0\nmode=station\nwpa_state=COMPLETED\n"
		case "SIGNAL_POLL":
			return "RSSI=-66\nNOISE=9999\nFREQUENCY=2437\n"
		}

		return ""
	})

	sp := New()
	sp.IP = "192.168.1.42"

	network, err := sp.NetworkInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if network.Dbm != -66 || network.MHz != 2437 || network.LinkMbps != 0 || network.Signal == "" {
		t.Fatalf("network = %+v", network)
	}

	body, err := json.Marshal(network)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(body), "linkMbps") || !strings.Contains(string(body), `"dbm":-66`) {
		t.Fatalf("JSON = %s; want dbm and no linkMbps", body)
	}
}

func TestNetworkInfoReportsSetupAP(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "setup-ap")

	err := os.WriteFile(marker, nil, 0o600)
	if err != nil {
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
	t.Parallel()

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
	t.Parallel()

	for _, state := range []string{"SCANNING", "ASSOCIATING", "4WAY_HANDSHAKE", "DISCONNECTED", ""} {
		out := "ssid=HomeNet\nmode=station\nwpa_state=" + state + "\n"
		if ssid, ok := parseWpaStatus(out); ok {
			t.Errorf("wpa_state=%q reported as associated with %q", state, ssid)
		}
	}
}

func TestParseWpaStatusHandlesGarbage(t *testing.T) {
	t.Parallel()

	for _, out := range []string{"", "Failed to connect to non-global ctrl_ifname\n", "no equals signs here\n"} {
		if _, ok := parseWpaStatus(out); ok {
			t.Errorf("%q reported as associated", out)
		}
	}
}

func TestParseWpaStatusRejectsAPInterface(t *testing.T) {
	t.Parallel()

	out := "ssid=SoundTap-Setup\nmode=AP\nwpa_state=COMPLETED\n"
	if ssid, ok := parseWpaStatus(out); ok {
		t.Fatalf("AP interface reported as station association with %q", ssid)
	}
}

func TestParseWifiSignalAcceptsNegativeDBMOnly(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		out  string
		want string
	}{
		{"RSSI=-78\nLINKSPEED=54\n", "fair"},
		{"RSSI=73\n", ""}, // percentage is not dBm
		{"RSSI=13\n", ""}, // positive AP-style nonsense
		{"RSSI=-120\n", ""},
		{"RSSI=UNKNOWN\n", ""},
	} {
		if got := parseWifiSignal(test.out).signal; got != test.want {
			t.Errorf("parseWifiSignal(%q) = %q, want %q", test.out, got, test.want)
		}
	}
}

func TestParseWifiSignalReadsTheLink(t *testing.T) {
	t.Parallel()

	got := parseWifiSignal("RSSI=-58\nLINKSPEED=433\nNOISE=9999\nFREQUENCY=5180\n")
	if want := (wifiSignal{signal: dbmToken(-58), dbm: -58, mbps: 433, mhz: 5180}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	if got := parseWifiSignal("RSSI=13\nLINKSPEED=54\n"); got != (wifiSignal{signal: "", dbm: 0, mbps: 0, mhz: 0}) {
		t.Fatalf("an implausible RSSI kept %+v", got)
	}
}

//nolint:paralleltest // it points wpaCtrlPath at a missing socket.
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
	t.Parallel()

	ssid, ok := parseWpaStatus("mode=station\nwpa_state=COMPLETED\nssid= Home \n")
	if !ok || ssid != " Home " {
		t.Fatalf("got %q %v, want \" Home \"", ssid, ok)
	}
}

// An ST20 with its cable in reports Ethernet and the wired address, even with Wi-Fi up.
//
//nolint:paralleltest // it replaces wiredIP.
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

// An ST20 on its cable with the setup AP running beside it is on the LAN: the settings
// page said "Connected to" the AP's name with the AP's address.
func TestNetworkInfoPrefersTheCableOverTheSetupAP(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "setup-ap")

	err := os.WriteFile(marker, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("SOUNDTAP_SETUP_AP_MARKER", marker)

	wired := wiredIP
	wiredIP = func() string { return "192.0.2.109" }

	t.Cleanup(func() { wiredIP = wired })

	network, err := New().NetworkInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if network.Type != "ethernet" || network.IP != "192.0.2.109" || network.SSID != "" {
		t.Fatalf("network = %+v, want ethernet on 192.0.2.109", network)
	}
}

// The speaker runs on its cable only with carrier and an IPv4 lease on eth0.
func TestWiredIPNeedsCarrierAndAddress(t *testing.T) {
	t.Parallel()

	addr4 := &net.IPNet{IP: net.IPv4(192, 0, 2, 109), Mask: net.CIDRMask(24, 32)}

	addr6 := &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}
	for _, test := range []struct {
		name    string
		carrier bool
		addrs   []net.Addr
		want    string
	}{
		{"carrier and lease", true, []net.Addr{addr6, addr4}, "192.0.2.109"},
		{"carrier, no lease yet", true, []net.Addr{addr6}, ""},
		{"carrier, no addresses", true, nil, ""},
		{"address left after the cable went", false, []net.Addr{addr4}, ""},
	} {
		if got := WiredIP(test.carrier, test.addrs); got != test.want {
			t.Errorf("%s: WiredIP = %q, want %q", test.name, got, test.want)
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
