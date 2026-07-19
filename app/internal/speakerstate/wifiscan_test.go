// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speakerstate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseScanResults(t *testing.T) {
	out := "bssid / frequency / signal level / flags / ssid\n" +
		"aa:bb:cc:dd:ee:ff\t2412\t-45\t[WPA2-PSK-CCMP][ESS]\tHomeNet\n" +
		"11:22:33:44:55:66\t5180\t-82\t[ESS]\tOpenCafe\n" +
		"22:33:44:55:66:77\t2462\t-90\t[ESS]\tHomeNet\n" + // weaker duplicate must not replace strongest
		"77:88:99:aa:bb:cc\t2437\t-60\t[WPA2-PSK]\t\n" // hidden ssid -> dropped

	nets := parseScanResults(out)
	if len(nets) != 2 {
		t.Fatalf("got %d networks, want 2 (hidden dropped): %+v", len(nets), nets)
	}
	if nets[0].SSID != "HomeNet" || !nets[0].Secure || nets[0].Signal != "excellent" {
		t.Fatalf("HomeNet parsed wrong: %+v", nets[0])
	}
	if nets[1].SSID != "OpenCafe" || nets[1].Secure {
		t.Fatalf("OpenCafe should be open: %+v", nets[1])
	}
}

func TestSetWifiValidatesAndPropagatesPersistenceFailure(t *testing.T) {
	sp := New()
	sp.OnWifiProfile(func(string, string) error { return errors.New("disk full") })
	if err := sp.SetWifi(context.Background(), "Home", "wpa_or_wpa2", "password1"); err == nil {
		t.Fatal("SetWifi ignored persistence failure")
	}
	for _, tc := range []struct{ ssid, security, password string }{
		{"Home\nnetwork={}", "wpa_or_wpa2", "password1"},
		{"Home", "wpa_or_wpa2", "short"},
		{"Home", "wep", "password1"},
		{"Home", "none", "unexpected"},
	} {
		if err := New().SetWifi(context.Background(), tc.ssid, tc.security, tc.password); err == nil {
			t.Fatalf("SetWifi(%q, %q) accepted invalid input", tc.ssid, tc.security)
		}
	}
}

func TestParseScanResultsEmpty(t *testing.T) {
	if got := parseScanResults(""); len(got) != 0 {
		t.Fatalf("empty scan should yield no networks, got %+v", got)
	}
}

func TestParseIWScan(t *testing.T) {
	out := `BSS aa:bb:cc:dd:ee:ff(on wlan0)
	signal: -72.00 dBm
	capability: ESS Privacy (0x0411)
	SSID: HomeNet
	RSN:
BSS 11:22:33:44:55:66(on wlan0)
	signal: -41.00 dBm
	capability: ESS (0x0401)
	SSID: Cafe
BSS 77:88:99:aa:bb:cc(on wlan0)
	signal: -35.00 dBm
	SSID: HomeNet
	RSN:
`
	nets := parseIWScan(out)
	if len(nets) != 2 || nets[0].SSID != "HomeNet" || !nets[0].Secure || nets[0].Signal != "excellent" {
		t.Fatalf("iw scan parsed wrong: %+v", nets)
	}
	if nets[1].SSID != "Cafe" || nets[1].Secure {
		t.Fatalf("open network parsed wrong: %+v", nets[1])
	}
}

func TestSetWifiUsesByteLimitsAndPreservesSSIDSpaces(t *testing.T) {
	sp := New()
	var saved string
	sp.OnWifiProfile(func(ssid, _ string) error { saved = ssid; return nil })
	if err := sp.SetWifi(context.Background(), " Home ", "wpa_or_wpa2", "password1"); err != nil || saved != " Home " {
		t.Fatalf("legal SSID spaces not preserved: saved=%q err=%v", saved, err)
	}
	if err := sp.SetWifi(context.Background(), "Home", "wpa_or_wpa2", "éééééééééééééééééééééééééééééééé"); err == nil {
		t.Fatal("multibyte password over 63 octets accepted")
	}
}

func TestBCOWifiModeDoesNotSetSCMWifi(t *testing.T) {
	t.Setenv("SOUNDTAP_WIFI_MODE", "bco")
	sp := New()
	if err := sp.SetWifi(context.Background(), "ssid", "wpa_or_wpa2", "pw"); err == nil {
		t.Fatal("SetWifi in bco mode succeeded; want error")
	}
}

// In setup mode the scan comes from the Wi-Fi manager's cache at once: a live scan stops the BCO's
// beacons and outlasts the UI's 8 s deadline.
func TestScanWifiInSetupModeAnswersFromTheCache(t *testing.T) {
	dir := t.TempDir()
	for _, tool := range []string{"iw"} {
		if err := os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("SOUNDTAP_WIFI_MODE", "scm")
	defer func(m, c string) { setupAPMarker, scanCache = m, c }(setupAPMarker, scanCache)
	setupAPMarker, scanCache = filepath.Join(dir, "setup-ap"), filepath.Join(dir, "scan")
	if err := os.WriteFile(setupAPMarker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scanCache, []byte("BSS aa:bb:cc:dd:ee:ff(on wlan0)\n\tsignal: -50.00 dBm\n\tSSID: Home\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	nets, err := New().ScanWifi(ctx)
	if err != nil || len(nets) != 1 || nets[0].SSID != "Home" {
		t.Fatalf("setup-mode scan = %+v, %v; want the cached Home at once", nets, err)
	}
}

// fakeTools puts scripts on PATH under the given names.
func fakeTools(t *testing.T, tools map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, script := range tools {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":/usr/bin:/bin")
	t.Setenv("SOUNDTAP_WIFI_MODE", "scm")
}

const iwTwoNetworks = "printf 'BSS aa:bb:cc:dd:ee:01(on wlan0)\\n\\tsignal: -50.00 dBm\\n\\tSSID: Home\\n" +
	"BSS aa:bb:cc:dd:ee:02(on wlan0)\\n\\tsignal: -60.00 dBm\\n\\tSSID: Neighbour\\n'"

// After idle minutes wpa_supplicant's table holds only the current network; a survey must
// not stop at that.
func TestScanWifiSurveysInsteadOfReadingTheAgedTable(t *testing.T) {
	fakeTools(t, map[string]string{"iw": iwTwoNetworks})
	fakeSupplicant(t, func(cmd string) string {
		if cmd == "SCAN_RESULTS" {
			return "bssid / frequency / signal level / flags / ssid\naa:bb:cc:dd:ee:01\t2412\t-50\t[ESS]\tHome\n"
		}
		return ""
	})
	nets, err := New().ScanWifi(context.Background())
	if err != nil || len(nets) != 2 {
		t.Fatalf("scan = %+v, %v; want both networks", nets, err)
	}
}

func TestScanWifiWaitsForARunningScan(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "tried")
	fakeTools(t, map[string]string{
		"iw": "[ -e " + marker + " ] || { : > " + marker + "; echo 'command failed: Resource busy (-16)' >&2; exit 240; }\n" + iwTwoNetworks,
	})
	nets, err := New().ScanWifi(context.Background())
	if err != nil || len(nets) != 2 {
		t.Fatalf("scan = %+v, %v; want the retry's two networks", nets, err)
	}
}

// The ST20's driver finishes before iw listens, and iw then never returns.
func TestScanWifiReadsTheResultsWhenIwMissesTheEnd(t *testing.T) {
	defer func(d time.Duration) { iwScanWait = d }(iwScanWait)
	iwScanWait = 100 * time.Millisecond
	fakeTools(t, map[string]string{
		"iw": `[ "$4" = dump ] || exec sleep 30` + "\n" + iwTwoNetworks,
	})
	nets, err := New().ScanWifi(context.Background())
	if err != nil || len(nets) != 2 {
		t.Fatalf("scan = %+v, %v; want the two networks of the dump", nets, err)
	}
}

func TestScanWifiFallsBackToTheTable(t *testing.T) {
	fakeTools(t, map[string]string{
		"iw": "echo 'command failed: No such device (-19)' >&2; exit 237",
	})
	fakeSupplicant(t, func(cmd string) string {
		if cmd == "SCAN_RESULTS" {
			return "bssid / frequency / signal level / flags / ssid\naa:bb:cc:dd:ee:01\t2412\t-50\t[ESS]\tHome\n"
		}
		return ""
	})
	nets, err := New().ScanWifi(context.Background())
	if err != nil || len(nets) != 1 || nets[0].SSID != "Home" {
		t.Fatalf("scan = %+v, %v; want Home from the table", nets, err)
	}
}

// The pick list comes strongest first; a network without a measured level goes last.
func TestScansSortStrongestFirst(t *testing.T) {
	iw := "BSS 01(on wlan0)\n\tsignal: -80.00 dBm\n\tSSID: Far\n" +
		"BSS 02(on wlan0)\n\tsignal: 0.00 dBm\n\tSSID: Unknown\n" +
		"BSS 03(on wlan0)\n\tsignal: -50.00 dBm\n\tSSID: Near\n"
	got := parseIWScan(iw)
	if len(got) != 3 || got[0].SSID != "Near" || got[1].SSID != "Far" || got[2].SSID != "Unknown" {
		t.Fatalf("iw order = %+v; want Near, Far, Unknown", got)
	}
	wpa := "bssid / frequency / signal level / flags / ssid\n" +
		"aa\t2412\t-85\t[ESS]\tFar\n" +
		"bb\t2412\t-40\t[ESS]\tNear\n"
	if got := parseScanResults(wpa); len(got) != 2 || got[0].SSID != "Near" {
		t.Fatalf("wpa_cli order = %+v; want Near first", got)
	}
}

// SSID bytes are significant: " Home " and "Home" are two networks.
func TestScanParsersKeepSSIDSpaces(t *testing.T) {
	wpa := "bssid / frequency / signal level / flags / ssid\n" +
		"aa:aa:aa:aa:aa:01\t2412\t-40\t[ESS]\t Home \n" +
		"aa:aa:aa:aa:aa:02\t2412\t-50\t[ESS]\tHome\r\n"
	got := parseScanResults(wpa)
	if len(got) != 2 || got[0].SSID != " Home " || got[1].SSID != "Home" {
		t.Fatalf("wpa_cli scan = %+v", got)
	}
	// iw escapes a leading/trailing space (and backslash) as \xNN.
	iw := "BSS aa:aa:aa:aa:aa:01(on wlan0)\n\tsignal: -40.00 dBm\n\tSSID: \\x20Home\\x20\n" +
		"BSS aa:aa:aa:aa:aa:02(on wlan0)\n\tsignal: -50.00 dBm\n\tSSID: My Home\n"
	got = parseIWScan(iw)
	if len(got) != 2 || got[0].SSID != " Home " || got[1].SSID != "My Home" {
		t.Fatalf("iw scan = %+v", got)
	}
}

// The panel and the API name the network the Wi-Fi manager really hosts.
func TestSetupSSIDIsWhatTheSetupAPHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "default-name")
	t.Setenv("SOUNDTAP_SETUP_NAME", path)
	if got := SetupSSID(); got != "SoundTap-Setup" {
		t.Errorf("before a name is published: %q", got)
	}
	for _, tc := range []struct{ name, want string }{
		{"SoundTap ST20 A1B2C3\n", "SoundTap ST20 A1B2C3"},
		{strings.Repeat("x", 33) + "\n", "SoundTap-Setup"},
		{"\n", "SoundTap-Setup"},
	} {
		if err := os.WriteFile(path, []byte(tc.name), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := SetupSSID(); got != tc.want {
			t.Errorf("%q gave %q, want %q", tc.name, got, tc.want)
		}
	}
}
