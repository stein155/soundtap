// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package counters_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/counters"
)

func TestMatchesCountsNeedlesAcrossWrites(t *testing.T) {
	t.Parallel()

	var (
		out   bytes.Buffer
		count atomic.Uint64
	)

	writer := counters.Matches(&out, "underrun!!!", &count)

	parts := []string{
		"underrun!!! (at least 3.1 ms long)\n",
		"ok\nunder", "run!!! (at least 1 ms long)\nunderrun!!! underrun!!!\n",
		"overrun!!! is not one\n", "underrun", "!", "!!\n",
	}
	for _, part := range parts {
		_, err := writer.Write([]byte(part))
		if err != nil {
			t.Fatal(err)
		}
	}

	if got := count.Load(); got != 5 {
		t.Errorf("counted %d underruns, want 5", got)
	}

	var want bytes.Buffer
	for _, part := range parts {
		want.WriteString(part)
	}

	if out.String() != want.String() {
		t.Errorf("passed on %q, want %q", out.String(), want.String())
	}
}

func TestCountStartCountsPerBoot(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "run", "soundtapd-starts")
	for want := range uint64(3) {
		got, err := counters.CountStart(path)
		if err != nil || got != want {
			t.Fatalf("start %d: %d restarts, err %v", want+1, got, err)
		}
	}
	// A garbled file is a first start, not a failure: the count only informs.
	err := os.WriteFile(path, []byte("x\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got, err := counters.CountStart(path)
	if err != nil || got != 0 {
		t.Errorf("after a garbled file: %d, %v", got, err)
	}
}

//nolint:paralleltest // resets and reads the process-wide counts other tests could move
func TestDaemonCountsAndSnapshot(t *testing.T) {
	counters.Reset()
	t.Cleanup(counters.Reset)

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { counters.DaemonRestarted("librespot") })
	}

	wg.Wait()
	counters.DaemonRestarted("shairport-sync")

	restedAt := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	counters.DaemonCrashLooped("librespot", 20, "exit status 1", restedAt)
	counters.DaemonCrashLooped("librespot", 20, "exit status 2", restedAt.Add(time.Minute))
	counters.BufferUnderruns.Add(2)
	counters.BCOFlashFailures.Add(1)

	snap := counters.Take()
	if snap.DaemonRestarts["librespot"] != 50 || snap.DaemonRestarts["shairport-sync"] != 1 {
		t.Errorf("daemon restarts %v", snap.DaemonRestarts)
	}

	if snap.BufferUnderruns != 2 || snap.BCOFlashFailures != 1 || snap.ALSAUnderruns != 0 {
		t.Errorf("snapshot %+v", snap)
	}

	loop := counters.CrashLoops()["librespot"]
	if loop.Count != 2 || loop.Err != "exit status 2" || loop.Runs != 20 || !loop.At.Equal(restedAt.Add(time.Minute)) {
		t.Errorf("crash loop %+v", loop)
	}
	// The maps handed out are copies: a caller cannot change the counts.
	snap.DaemonRestarts["librespot"] = 0
	if counters.DaemonRestarts()["librespot"] != 50 {
		t.Error("the snapshot shares its map with the counters")
	}
}
