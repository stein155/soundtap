// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package counters

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMatchesCountsNeedlesAcrossWrites(t *testing.T) {
	var (
		out bytes.Buffer
		c   atomic.Uint64
	)

	w := Matches(&out, "underrun!!!", &c)

	parts := []string{
		"underrun!!! (at least 3.1 ms long)\n",
		"ok\nunder", "run!!! (at least 1 ms long)\nunderrun!!! underrun!!!\n",
		"overrun!!! is not one\n", "underrun", "!", "!!\n",
	}
	for _, part := range parts {
		if _, err := w.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}

	if got := c.Load(); got != 5 {
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
	path := filepath.Join(t.TempDir(), "run", "soundtapd-starts")
	for want := range uint64(3) {
		got, err := CountStart(path)
		if err != nil || got != want {
			t.Fatalf("start %d: %d restarts, err %v", want+1, got, err)
		}
	}
	// A garbled file is a first start, not a failure: the count only informs.
	err := os.WriteFile(path, []byte("x\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if got, err := CountStart(path); err != nil || got != 0 {
		t.Errorf("after a garbled file: %d, %v", got, err)
	}
}

func TestDaemonCountsAndSnapshot(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() { DaemonRestarted("librespot") })
	}

	wg.Wait()
	DaemonRestarted("shairport-sync")

	at := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	DaemonCrashLooped("librespot", 20, "exit status 1", at)
	DaemonCrashLooped("librespot", 20, "exit status 2", at.Add(time.Minute))
	BufferUnderruns.Add(2)
	BCOFlashFailures.Add(1)

	s := Take()
	if s.DaemonRestarts["librespot"] != 50 || s.DaemonRestarts["shairport-sync"] != 1 {
		t.Errorf("daemon restarts %v", s.DaemonRestarts)
	}

	if s.BufferUnderruns != 2 || s.BCOFlashFailures != 1 || s.ALSAUnderruns != 0 {
		t.Errorf("snapshot %+v", s)
	}

	loop := CrashLoops()["librespot"]
	if loop.Count != 2 || loop.Err != "exit status 2" || loop.Runs != 20 || !loop.At.Equal(at.Add(time.Minute)) {
		t.Errorf("crash loop %+v", loop)
	}
	// The maps handed out are copies: a caller cannot change the counts.
	s.DaemonRestarts["librespot"] = 0
	if DaemonRestarts()["librespot"] != 50 {
		t.Error("the snapshot shares its map with the counters")
	}
}
