// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import "testing"

// A peer's redirect stays on the peer, and nowhere else.
func TestPeerLocation(t *testing.T) {
	for loc, want := range map[string]string{
		"/?setup=wifi":                    "/api/devices/SN-peer/?setup=wifi",
		"http://192.168.1.9:80/api/x?a=1": "/api/devices/SN-peer/api/x?a=1",
		"next":                            "next",
		"http://evil.example/":            "",
		"//evil.example/x":                "",
		"https://192.168.1.9:80/":         "",
		"javascript:alert(1)":             "",
	} {
		if got := peerLocation(loc, "SN-peer", "192.168.1.9:80"); got != want {
			t.Errorf("peerLocation(%q) = %q, want %q", loc, got, want)
		}
	}
}
