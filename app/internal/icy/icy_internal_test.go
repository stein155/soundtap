// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package icy

import "testing"

func TestParseStreamTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, meta, want string
	}{
		{
			"typical",
			"StreamTitle='Charles & Eddie - Would I Lie To You';StreamUrl='';",
			"Charles & Eddie - Would I Lie To You",
		},
		{"trailing nuls trimmed", "StreamTitle='Song';\x00\x00\x00", "Song"},
		{"no semicolon terminator", "StreamTitle='Song'", "Song"},
		{
			"apostrophe, no terminator",
			"StreamTitle='Guns N Roses - Sweet Child O' Mine'\x00\x00",
			"Guns N Roses - Sweet Child O' Mine",
		},
		{"apostrophe, terminated", "StreamTitle='Sweet Child O' Mine';StreamUrl='';", "Sweet Child O' Mine"},
		{"empty value", "StreamTitle='';StreamUrl='';", ""},
		{"absent", "StreamUrl='x';", ""},
		{"latin-1 é", "StreamTitle='Andr\xe9 Hazes Jr';", "André Hazes Jr"},
		{"windows-1252 em dash", "StreamTitle='A \x96 B';", "A – B"},
		{"utf-8 kept as-is", "StreamTitle='André';", "André"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := parseStreamTitle(tt.meta); got != tt.want {
				t.Errorf("parseStreamTitle(%q) = %q, want %q", tt.meta, got, tt.want)
			}
		})
	}
}

func TestParseStreamTitleDropsAdMarker(t *testing.T) {
	t.Parallel()

	if got := parseStreamTitle("StreamTitle='commercial-in';StreamUrl='';"); got != "" {
		t.Fatalf("ad marker shown as track: %q", got)
	}

	if got := parseStreamTitle("StreamTitle='Commercials - The Band';"); got != "Commercials - The Band" {
		t.Fatalf("real track dropped: %q", got)
	}
}
