// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
	"github.com/stein155/soundtap/app/internal/speakerstate"
)

// AUX under the mainline kernel is a capture into the bus while AUX is selected, and
// nothing once it is not.
func TestAuxCaptureFollowsTheSource(t *testing.T) {
	dir := t.TempDir()
	bus, pidFile := filepath.Join(dir, "bus"), filepath.Join(dir, "pid")
	if err := os.WriteFile(bus, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	saved := auxCaptureCmd
	auxCaptureCmd = func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", `echo $$ > "$0"; while :; do printf x; sleep 0.02; done`, pidFile)
	}
	t.Cleanup(func() { auxCaptureCmd = saved })
	size := func() int64 {
		fi, _ := os.Stat(bus)
		return fi.Size()
	}
	// A pause can only hide a capture here, never invent one: the checks that something
	// happened wait for it instead, so a busy machine does not fail them.
	grows := func() bool {
		before := size()
		time.Sleep(200 * time.Millisecond)
		return size() > before
	}

	sp := speakerstate.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runAuxCapture(ctx, sp, bus, slog.New(slog.DiscardHandler))
	if grows() {
		t.Fatal("captured before AUX was selected")
	}
	if err := sp.SetSource(ctx, sourceAux); err != nil {
		t.Fatal(err)
	}
	// The pid is written before the first byte, so a non-empty bus means it is there.
	waitFor(t, "a capture into the bus", func() bool { return size() > 0 })
	pidText, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidText)))
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.SetSource(ctx, sourceBluetooth); err != nil {
		t.Fatal(err)
	}
	// Gone, not just killed: once reaped it cannot write another byte.
	waitFor(t, "the capture to end", func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH })
	if grows() {
		t.Fatal("the capture kept writing after AUX was left")
	}
}

// AUX plays live on its own short buffer, a follower on the track-change buffer, the
// rest prebuffered.
func TestLiveBufferPerSource(t *testing.T) {
	for source, want := range map[string]time.Duration{
		sourceAux:       auxLiveBuffer,
		sourceMultiroom: audio.FollowerBuffer,
		"TUNEIN":        0,
		sourceStandby:   0,
	} {
		if got := liveBuffer(source); got != want {
			t.Errorf("liveBuffer(%q) = %v, want %v", source, got, want)
		}
	}
}

// AUX's worst case from the line input to the DAC stays where it was put, and the
// other sources keep the deep sink they were proven on.
func TestAuxBudget(t *testing.T) {
	if got := auxBudget(); got > 195*time.Millisecond {
		t.Errorf("AUX budget %v, want at most 195ms", got)
	}
	if defaultSinkTiming != (sinkTiming{320_000, 80_000, 8192}) {
		t.Errorf("default sink timing changed: %+v", defaultSinkTiming)
	}
}

// The ALSA sink reopens on the AUX timing while AUX plays and back afterwards, closing
// the old aplay and flushing each time.
func TestTimedSinkFollowsAux(t *testing.T) {
	t.Cleanup(func() { auxOwnsSink.Store(false) })
	var opened []sinkTiming
	closes, flushes := 0, 0
	s := &timedSink{log: quietLog(), timing: defaultSinkTiming, w: io.Discard,
		close: func() error { closes++; return nil },
		flush: func() { flushes++ },
		open: func(st sinkTiming) (io.Writer, func() error, error) {
			opened = append(opened, st)
			return io.Discard, func() error { closes++; return nil }, nil
		}}
	write := func() {
		t.Helper()
		if _, err := s.Write(make([]byte, 8)); err != nil {
			t.Fatal(err)
		}
	}
	write()
	auxOwnsSink.Store(true)
	write()
	write()
	auxOwnsSink.Store(false)
	write()
	if len(opened) != 2 || opened[0] != auxSinkTiming || opened[1] != defaultSinkTiming || closes != 2 || flushes != 2 {
		t.Fatalf("opened %+v, closes %d, flushes %d", opened, closes, flushes)
	}
	if err := s.Close(); err != nil || closes != 3 {
		t.Fatalf("close: %v, closes %d", err, closes)
	}
}
