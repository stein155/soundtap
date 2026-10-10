// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"slices"
	"testing"
)

func TestInstallProgressStopsShortOfDone(t *testing.T) {
	var heard []int
	p := &installProgress{total: 200, report: func(pct int) { heard = append(heard, pct) }}
	for _, n := range []int{1, 1, 98, 100} {
		if _, err := p.Write(make([]byte, n)); err != nil {
			t.Fatal(err)
		}
	}
	if want := []int{1, 50, 99}; !slices.Equal(heard, want) {
		t.Fatalf("heard %v, want %v", heard, want)
	}
}

func TestInstallProgressWithoutALengthStaysSilent(t *testing.T) {
	p := &installProgress{report: func(int) { t.Error("no length, no percentage") }}
	if _, err := p.Write(make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
}
