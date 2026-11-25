// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bco

import (
	"context"
	"strings"
	"testing"
)

func TestParseStatusReadsTheAssociation(t *testing.T) {
	status := parseStatus("bssid=00:11:22:33:44:55\nfreq=2437\nssid=HomeNet\nwpa_state=COMPLETED\nip_address=192.168.1.42\n")
	if !status.Linked {
		t.Error("COMPLETED means linked")
	}

	if status.SSID != "HomeNet" || status.BSSID != "00:11:22:33:44:55" || status.IP != "192.168.1.42" {
		t.Errorf("unexpected status: %+v", status)
	}

	if status.Freq != "2437" {
		t.Errorf("freq not read: %+v", status)
	}
}

func TestParseStatusKeepsSSIDDespiteTrailingNoise(t *testing.T) {
	status := parseStatus("ssid=Home\nwpa_state=COMPLETED\n<async link event>\n>")
	if status.SSID != "Home" {
		t.Fatalf("SSID = %q, want structured value despite trailing noise", status.SSID)
	}
}

func TestParseStatusOnlyLinksOnCompleted(t *testing.T) {
	// Anything short of COMPLETED must not be reported as a working association,
	// otherwise Connect would persist a profile that never actually joined.
	for _, state := range []string{"SCANNING", "ASSOCIATING", "DISCONNECTED", "4WAY_HANDSHAKE", "INACTIVE"} {
		if parseStatus("wpa_state=" + state).Linked {
			t.Errorf("%s must not count as linked", state)
		}
	}
}

func TestCurrentSSIDRequiresLiveAssociation(t *testing.T) {
	ssid, err := currentSSID(Status{State: "COMPLETED", SSID: " HomeNet ", Linked: true})
	if err != nil || ssid != "HomeNet" {
		t.Fatalf("linked SSID = %q, %v", ssid, err)
	}

	for _, state := range []string{"SCANNING", "ASSOCIATING", "DISCONNECTED"} {
		if ssid, err := currentSSID(Status{State: state, SSID: "stale-profile"}); err == nil || ssid != "" {
			t.Errorf("%s returned stale SSID %q, %v", state, ssid, err)
		}
	}

	if _, err := currentSSID(Status{State: "COMPLETED", Linked: true}); err == nil {
		t.Error("linked status without SSID was accepted")
	}
}

func TestCleanShellOutputDropsEchoAndRedraws(t *testing.T) {
	// The sdsShell repaints the input line after every byte, so \b lines are echo.
	raw := "wpa STATUS\r\nwpa\bwpa \bwpa S\r\nwpa_state=COMPLETED\r\n"

	got := cleanShellOutput(raw, "wpa STATUS")
	if got != "wpa_state=COMPLETED" {
		t.Errorf("want only the payload line, got %q", got)
	}
}

func TestNetworkIDReadsTheBareNumber(t *testing.T) {
	id, err := networkID("3")
	if err != nil || id != 3 {
		t.Fatalf("want id 3, got %d (%v)", id, err)
	}

	if _, err := networkID("FAIL"); err == nil {
		t.Error("a refusal must not be read as a network id")
	}

	if _, err := networkID(""); err == nil {
		t.Error("an empty reply must not be read as a network id")
	}
}

func TestValidSSIDRejectsUnquotableNames(t *testing.T) {
	// Connect wraps the SSID in quotes, so anything that could break out of the
	// quoting has to be refused before it reaches the shell.
	for _, bad := range []string{"", strings.Repeat("x", 33), `boks"net`, `boks\net`, "boks\nnet", "boks\x01net"} {
		err := validSSID(bad)
		if err == nil {
			t.Errorf("ssid %q must be rejected", bad)
		}
	}

	for _, good := range []string{"HomeNet", "Bose ST 20", "café-net", strings.Repeat("x", 32)} {
		err := validSSID(good)
		if err != nil {
			t.Errorf("ssid %q must be accepted: %v", good, err)
		}
	}
}

func TestValidPSKAcceptsOpenAndBoundedPassphrases(t *testing.T) {
	err := validPSK("")
	if err != nil {
		t.Errorf("an empty passphrase means an open network: %v", err)
	}

	for _, bad := range []string{"kort", strings.Repeat("x", 64), "geheim\n123", `geheim"123`} {
		err := validPSK(bad)
		if err == nil {
			t.Errorf("passphrase %q must be rejected", bad)
		}
	}

	for _, good := range []string{"geheim123", strings.Repeat("x", 63)} {
		err := validPSK(good)
		if err != nil {
			t.Errorf("passphrase %q must be accepted: %v", good, err)
		}
	}
}

func TestConnectValidatesBeforeTouchingTheRadio(t *testing.T) {
	cases := []struct{ name, ssid, psk string }{
		{"empty ssid", "", "geheim123"},
		{"ssid with a quote", `boks"net`, "geheim123"},
		{"short passphrase", "HomeNet", "kort"},
		{"passphrase with newline", "HomeNet", "geheim\n123"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The device does not exist: reaching it would be a failure of ordering,
			// because validation has to happen before the port is opened.
			err := Connect(context.Background(), "/nonexistent-tty", c.ssid, c.psk)
			if err == nil {
				t.Fatal("want a validation error")
			}
		})
	}
}

func TestFirstLineBoundsErrorText(t *testing.T) {
	if got := firstLine("  FAIL-BUSY\nnoise\n"); got != "FAIL-BUSY" {
		t.Errorf("want the first meaningful line, got %q", got)
	}

	if got := firstLine(strings.Repeat("x", 300)); len(got) != 120 {
		t.Errorf("a long line must be truncated to 120, got %d", len(got))
	}
}

// TestWPAPSKMatchesIEEEVector pins the derivation against IEEE 802.11i H.4.
func TestWPAPSKMatchesIEEEVector(t *testing.T) {
	if got := wpaPSK("password", "IEEE"); got != "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e" {
		t.Fatalf("wpaPSK = %s", got)
	}
}

func TestStaleNetworkIDs(t *testing.T) {
	list := "network id / ssid / bssid / flags\n0\tHomeNet\tany\t[CURRENT][DISABLED]\n1\tHomeNet\tany\t\n2\tOther\tany\t[DISABLED]"
	if got := staleNetworkIDs(list, "HomeNet", 1); len(got) != 1 || got[0] != 0 {
		t.Fatalf("staleNetworkIDs = %v", got)
	}
}

// A FAIL reply is a refusal for SET_NETWORK and SAVE_CONFIG alike; Connect used to
// report a join as saved when SAVE_CONFIG said FAIL.
func TestRefusedReadsFail(t *testing.T) {
	for out, want := range map[string]bool{"OK": false, "FAIL": true, "fail\r\n": true, "": false} {
		if got := refused(out); got != want {
			t.Errorf("refused(%q) = %v, want %v", out, got, want)
		}
	}
}

func TestRawKeyGoesThroughAsIs(t *testing.T) {
	key := strings.Repeat("aB", 32)

	err := validPSK(key)
	if err != nil {
		t.Errorf("raw key refused: %v", err)
	}

	if got := wpaPSK(key, "Home"); got != key {
		t.Errorf("raw key derived again: %q", got)
	}

	for _, s := range []string{strings.Repeat("a", 63), strings.Repeat("g", 64), strings.Repeat("a", 65)} {
		if IsHexPSK(s) {
			t.Errorf("%q counted as a raw key", s)
		}
	}
}
