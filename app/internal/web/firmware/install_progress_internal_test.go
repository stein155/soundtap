// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"slices"
	"strings"
	"testing"
)

func TestInstallProgressStopsShortOfDone(t *testing.T) {
	t.Parallel()

	var heard []int

	progress := &installProgress{total: 200, read: 0, last: 0, report: func(pct int) { heard = append(heard, pct) }}
	for _, n := range []int{1, 1, 98, 100} {
		_, err := progress.Write(make([]byte, n))
		if err != nil {
			t.Fatal(err)
		}
	}

	if want := []int{1, 50, 99}; !slices.Equal(heard, want) {
		t.Fatalf("heard %v, want %v", heard, want)
	}
}

func TestInstallProgressWithoutALengthStaysSilent(t *testing.T) {
	t.Parallel()

	progress := &installProgress{total: 0, read: 0, last: 0, report: func(int) { t.Error("no length, no percentage") }}

	_, err := progress.Write(make([]byte, 10))
	if err != nil {
		t.Fatal(err)
	}
}

// Progress shows an install's percentage while it runs with a known size, and nothing
// before, after, or without a size; the panel hears the percentage either way.
func TestProgressOnlyWhileAnInstallRunsWithASize(t *testing.T) {
	t.Parallel()

	handler := installServer(t, newFakeFlash())
	if _, ok := handler.Progress(); ok {
		t.Fatal("progress before any install")
	}

	var during []bool

	handler.SetProgressFn(func(pct int) {
		got, ok := handler.Progress()
		during = append(during, ok && got == pct)
	})

	if rec := post(t, handler, []byte(strings.Repeat("not firmware", 200))); rec.Code < 400 {
		t.Fatalf("rubbish accepted: %d", rec.Code)
	}

	if len(during) == 0 || slices.Contains(during, false) {
		t.Errorf("Progress during the install = %v, want the percentage each time", during)
	}

	if _, ok := handler.Progress(); ok {
		t.Error("progress after the install ended")
	}

	heard := false

	handler.reportProgress(0)(0)
	handler.SetProgressFn(func(int) { heard = true })
	handler.reportProgress(0)(0)

	if _, ok := handler.Progress(); ok || !heard {
		t.Errorf("without a size: Progress ok=%v, panel heard=%v; want no percentage and the panel told", ok, heard)
	}
}
