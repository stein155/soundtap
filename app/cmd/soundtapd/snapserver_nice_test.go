// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"slices"
	"testing"
)

// snapserver runs below the Mixer and aplay (a pop on an ST20 multiroom leader, 2026-11-24).
func TestSnapserverRunsNiced(t *testing.T) {
	if got, want := snapserverArgs("/run/soundtap/snapserver.conf"),
		[]string{"-n", mediaNice, "snapserver", "-c", "/run/soundtap/snapserver.conf"}; !slices.Equal(got, want) {
		t.Fatalf("snapserver command %v, want %v", got, want)
	}
}
