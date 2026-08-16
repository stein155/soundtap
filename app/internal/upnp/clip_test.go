// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package upnp

import (
	"strings"
	"testing"
	"unsafe"
)

// A short field trimmed out of a huge parsed string must not keep that string reachable.
func TestClipCopiesShortValues(t *testing.T) {
	big := "  artist" + strings.Repeat(" ", 1<<20)
	short := strings.TrimSpace(big)
	got := clip(short)
	if got != "artist" {
		t.Fatalf("clip = %q", got)
	}
	if unsafe.StringData(got) == unsafe.StringData(short) {
		t.Error("clip returned a slice of the parsed string instead of a copy")
	}
}
