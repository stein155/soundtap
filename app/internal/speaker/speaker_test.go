// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package speaker

import (
	"strings"
	"testing"
)

func TestCheckPreset(t *testing.T) {
	err := CheckPreset("Radio 538", "https://cdn-profiles.tunein.com/s6712/images/logoq.png")
	if err != nil {
		t.Fatalf("ordinary preset refused: %v", err)
	}

	for _, bad := range []struct{ name, logo string }{
		{"  ", ""},
		{strings.Repeat("x", 65), ""},
		{"a\nb", ""},
		{"ok", strings.Repeat("x", 2049)},
		{"ok", "https://x/\x00"},
		{"\xff", ""},
	} {
		if CheckPreset(bad.name, bad.logo) == nil {
			t.Errorf("CheckPreset(%q, %d-byte logo) accepted", bad.name, len(bad.logo))
		}
	}
}
