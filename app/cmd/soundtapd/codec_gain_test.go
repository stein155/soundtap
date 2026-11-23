// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
)

func TestPCMRunningRequiresALSAPlayingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status")
	if err := os.WriteFile(path, []byte("state: PREPARED\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pcmRunning(path) {
		t.Fatal("prepared PCM reported a running hardware clock")
	}
	if err := os.WriteFile(path, []byte("state: RUNNING\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !pcmRunning(path) {
		t.Fatal("running PCM did not report a hardware clock")
	}
}

// On mainline the DAC is ALSA behind AUDIO_DEV=alsa:...; its RUNNING state, not the
// Mixer's byte counter (which may feed a FIFO), says the codec is clocked.
func TestWaitForDACClockUsesALSAOnMainline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status")
	if err := os.WriteFile(path, []byte("state: RUNNING\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AUDIO_DEV", "alsa:plughw:0,0")
	t.Setenv("AUDIO_CLOCK_STATUS", path)
	health := func() audio.Health { return audio.Health{} } // the counter never moves
	ctx, cancel := context.WithTimeout(context.Background(), 2e9)
	defer cancel()
	if !waitForDACClock(ctx, health, slog.New(slog.NewTextHandler(io.Discard, nil))) {
		t.Fatal("a running ALSA PCM was not taken as a clocked DAC")
	}
}

// Without the SPI device the pass must go over GPIO: the loader would fail every time
// and leave the amplifier muted (ST20, 2026-10-08).
func TestGainPassUsesBitbangWithoutSPI(t *testing.T) {
	cases := []struct {
		spi, bitbang, blind bool
		want                string
	}{
		{false, true, false, aicBitbang + " --volume"},
		{false, true, true, aicBitbang + " --volume"},
		{true, true, false, aicLoader + " --volume"},
		{true, false, true, aicLoader + " --volume-blind"},
		{false, false, false, aicLoader + " --volume"},
	}
	for _, c := range cases {
		if got := strings.Join(gainPassArgs(c.spi, c.bitbang, c.blind), " "); got != c.want {
			t.Errorf("gainPassArgs(%v,%v,%v) = %q, want %q", c.spi, c.bitbang, c.blind, got, c.want)
		}
	}
}

// The coefficient is the 48-bit pair stock writes as words 0 and 1, unity 0x400000000000;
// stock's volume 50 is 0x01c0a9e0c000, -31.25 dB.
func TestCodecGainHex(t *testing.T) {
	for gain, want := range map[float64]string{1: "400000000000", 2: "400000000000", 0.5: "200000000000", 0: "000000000000", -1: "000000000000"} {
		if got := codecGainHex(gain); got != want {
			t.Fatalf("codecGainHex(%g) = %s, want %s", gain, got, want)
		}
	}
	if got := codecGainHex(math.Pow(10, -31.25/20)); got[:6] != "01c0a9" {
		t.Fatalf("-31.25 dB = %s, want stock's high word 01c0a9", got)
	}
}

// Stock parity: the first change lands at once, the next no sooner than 250 ms
// later, and only the newest value of what came in between is written.
func TestCodecVolumeWritesAtMostEveryGap(t *testing.T) {
	out := filepath.Join(t.TempDir(), "writes")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCodecVolume(ctx, []string{"sh", "-c", `echo "$1" >> ` + out, "sh"}, nil, func() {}, slog.Default())
	lines := func() []string {
		b, _ := os.ReadFile(out)
		return strings.Fields(string(b))
	}
	c.SetGain(0.1)
	// Wait on the writes, not a clock: a busy machine can take longer than the gap
	// to start the first one.
	waitFor(t, "the first write", func() bool { return len(lines()) > 0 })
	c.SetGain(0.2)
	c.SetGain(0.3)
	time.Sleep(50 * time.Millisecond)
	if got := lines(); len(got) != 1 || got[0] != codecGainHex(0.1) {
		t.Fatalf("within the gap: %v", got)
	}
	waitFor(t, "the write after the gap", func() bool { return len(lines()) > 1 })
	if got := lines(); len(got) != 2 || got[1] != codecGainHex(0.3) {
		t.Fatalf("after the gap: %v", got)
	}
}

// A write whose flip timed out, as one does when an underrun stops the DAC clock, is tried
// again: the volume stays in the codec. Only a write that fails every try goes back to the
// PCM, once.
func TestCodecVolumeWriteTriesAgainBeforeThePCM(t *testing.T) {
	defer func(d time.Duration) { codecWriteRetry = d }(codecWriteRetry)
	codecWriteRetry = time.Millisecond
	dir := t.TempDir()
	tries := filepath.Join(dir, "tries")
	// Fails the first time, as the ST20's bit-banged write did after an underrun.
	flaky := []string{"sh", "-c", `echo "$1" >> ` + tries + `; [ "$(wc -l < ` + tries + `)" -gt 1 ]`, "sh"}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := writeCodecGainTries(context.Background(), flaky, 1, codecWriteTries, nil, quiet); err != nil {
		t.Fatalf("a write that fails once: %v", err)
	}
	b, _ := os.ReadFile(tries)
	if got := strings.Fields(string(b)); len(got) != 2 || got[1] != codecGainHex(1) {
		t.Fatalf("writes: %v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failed := make(chan struct{}, codecWriteTries)
	refused := filepath.Join(dir, "refused")
	c := newCodecVolume(ctx, []string{"sh", "-c", `echo "$1" >> ` + refused + `; exit 1`, "sh"}, nil,
		func() { failed <- struct{}{} }, quiet)
	c.SetGain(0.2)
	select {
	case <-failed:
	case <-time.After(10 * time.Second):
		t.Fatal("a write that always fails never went back to the PCM")
	}
	b, _ = os.ReadFile(refused)
	if got := strings.Fields(string(b)); len(got) != codecWriteTries {
		t.Fatalf("tries before the PCM: %v, want %d", got, codecWriteTries)
	}
	if len(failed) != 0 {
		t.Fatal("failed called more than once")
	}
}

// A codec write runs below the Mixer's priority, with the gain as its last argument, and
// says what failed when it fails.
func TestCodecWriteRunsNiced(t *testing.T) {
	out := filepath.Join(t.TempDir(), "nice")
	script := `sleep 0.3; ps -o nice= -p $$ > "$0"; echo "$1" >> "$0"`
	if err := writeCodecGain(t.Context(), []string{"sh", "-c", script, out}, 1); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(b))
	if len(fields) != 2 || fields[0] != strconv.Itoa(codecWriteNice) || fields[1] != codecGainHex(1) {
		t.Errorf("the write ran as %q, want nice %d and gain %s", fields, codecWriteNice, codecGainHex(1))
	}
	err = writeCodecGain(t.Context(), []string{"sh", "-c", `echo no clock >&2; exit 3`}, 1)
	if err == nil || !strings.Contains(err.Error(), "no clock") {
		t.Errorf("failed write: %v, want its output", err)
	}
}
