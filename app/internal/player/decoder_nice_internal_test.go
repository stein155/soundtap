// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package player

import (
	"slices"
	"testing"
)

// The decoder runs below the Mixer and aplay (a pop on the ST20, 2026-11-24): nice execs it
// with its own arguments unchanged.
func TestTheDecoderRunsNiced(t *testing.T) {
	t.Parallel()

	got := decoderArgs("/usr/bin/ffmpeg", []string{"-i", "pipe:0"})
	if want := []string{"-n", DecoderNice, "/usr/bin/ffmpeg", "-i", "pipe:0"}; !slices.Equal(got, want) {
		t.Fatalf("decoder command %v, want %v", got, want)
	}

	if DecoderNice == "0" || DecoderNice == "" {
		t.Fatal("the decoder is not niced")
	}
}
