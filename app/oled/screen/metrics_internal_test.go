// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package screen

import (
	"strconv"
	"testing"
)

// number must read the metrics exactly as strconv does, or text moves by a pixel.
func TestNumberMatchesStrconv(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"0", "-3", "17", "4.5625", "-0.1234", "7.0003", "1000", "12.9999"} {
		want, _ := strconv.ParseFloat(s, 64)
		if got := number([]byte(s)); got != want {
			t.Errorf("number(%q) = %v, want %v", s, got, want)
		}
	}

	if hex([]byte("1f60a")) != 0x1f60a {
		t.Error("hex")
	}
}
