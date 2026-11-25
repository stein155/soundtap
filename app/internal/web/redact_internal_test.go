// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import "testing"

// What the redactor must leave alone: addresses that say nothing about where the owner
// lives, and text that only looks like an address or a name.
func TestScrubKeepsWhatIsNotASecret(t *testing.T) {
	t.Parallel()

	red := newRedactorFrom([]secretLiteral{{value: "home", label: "<wifi-ssid-1>", word: true}})
	for _, line := range []string{
		"lease 192.168.1.42 gw 10.0.0.1 dns 172.16.0.1",
		"loopback 127.0.0.1 ::1 link fe80::1%wlan0 ula fd12:3456::1",
		"st20 tap 203.0.113.1 cgnat 100.64.0.1",
		"at 10:11:12 std::bad_alloc mac aa:bb:cc:dd:ee:ff",
		"homeDir /mnt/nv/soundtap homes",
	} {
		if got := red.scrub(line); got != line {
			t.Errorf("scrub(%q) = %q, want it unchanged", line, got)
		}
	}

	for line, want := range map[string]string{
		"joined home at 8.8.4.4":           "joined <wifi-ssid-1> at <public-ip>",
		"v6 2001:4860:4860::8888 up":       "v6 <public-ip> up",
		"ssid       : Somebody's Network":  "ssid       : <redacted>",
		`ssid="Somebody"`:                  "ssid=<redacted>",
		"PSK_HEX=00ff00ff":                 "PSK_HEX= <redacted>",
		"Associated with SSID 'Elsewhere'": "Associated with SSID '<redacted>'",
	} {
		if got := red.scrub(line); got != want {
			t.Errorf("scrub(%q) = %q, want %q", line, got, want)
		}
	}
}
