// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package icy

import (
	"testing"
	"unicode/utf8"
)

// FuzzParseStreamTitle feeds it a radio station's metadata block: whatever the station
// sends, the title shown is valid UTF-8. `go test` runs the seeds;
// `go test -fuzz=FuzzParseStreamTitle ./internal/icy` searches further.
func FuzzParseStreamTitle(f *testing.F) {
	for _, seed := range []string{
		"StreamTitle='Artist - Title';StreamUrl='http://x';",
		"StreamTitle='Guns N\x92 Roses - Sweet Child O' Mine'\x00\x00",
		"StreamTitle='commercial-in';",
		"StreamTitle='",
		"StreamTitle=''';",
		"\xff\xfeStreamTitle='\xe9t\xe9';",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, meta string) {
		if title := parseStreamTitle(meta); !utf8.ValidString(title) {
			t.Fatalf("title %q from %q is not UTF-8", title, meta)
		}
	})
}
