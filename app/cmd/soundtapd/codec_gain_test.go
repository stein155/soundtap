// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/audio"
)

func TestPCMRunningRequiresALSAPlayingState(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "status")

	err := os.WriteFile(path, []byte("state: PREPARED\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if pcmRunning(path) {
		t.Fatal("prepared PCM reported a running hardware clock")
	}

	err = os.WriteFile(path, []byte("state: RUNNING\n"), 0o600)
	if err != nil {
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

	err := os.WriteFile(path, []byte("state: RUNNING\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("AUDIO_DEV", "alsa:plughw:0,0")
	t.Setenv("AUDIO_CLOCK_STATUS", path)

	var idle audio.Health // the counter never moves

	health := func() audio.Health { return idle }

	ctx, cancel := context.WithTimeout(context.Background(), 2e9)
	defer cancel()

	if !waitForDACClock(ctx, health, slog.New(slog.DiscardHandler)) {
		t.Fatal("a running ALSA PCM was not taken as a clocked DAC")
	}
}

// Without the SPI device the pass must go over GPIO: the loader would fail every time
// and leave the amplifier muted (ST20, 2026-10-08).
func TestGainPassUsesBitbangWithoutSPI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		tools codecTools
		want  string
	}{
		{codecTools{spi: false, bitbang: true, blind: false}, aicBitbang + " --volume"},
		{codecTools{spi: false, bitbang: true, blind: true}, aicBitbang + " --volume"},
		{codecTools{spi: true, bitbang: true, blind: false}, aicLoader + " --volume"},
		{codecTools{spi: true, bitbang: false, blind: true}, aicLoader + " --volume-blind"},
		{codecTools{spi: false, bitbang: false, blind: false}, aicLoader + " --volume"},
	}
	for _, tc := range cases {
		if got := strings.Join(gainPassArgs(tc.tools), " "); got != tc.want {
			t.Errorf("gainPassArgs(%+v) = %q, want %q", tc.tools, got, tc.want)
		}
	}
}

// Words 0 and 1 are stock's two tables side by side: the gain in Q22 and the same level in
// dB times 65536. Stock's volume 50 is 01c0a9 e0c000 (-31.25 dB) and volume 10 0007d9 bd9c28
// (-66.39 dB); unity is 400000 000000 and silence 000000 800000.
func TestCodecGainHex(t *testing.T) {
	t.Parallel()

	for gain, want := range map[float64]string{
		1: "400000000000", 2: "400000000000", 0.5: "200000f9faba",
		0: "000000800000", -1: "000000800000", math.Pow(10, -31.25/20): "01c0a9e0c000",
	} {
		if got := codecGainHex(gain); got != want {
			t.Fatalf("codecGainHex(%g) = %s, want %s", gain, got, want)
		}
	}

	got := codecGainHex(audio.VolumeGain(10))
	hi, _ := strconv.ParseUint(got[:6], 16, 32)

	lo, _ := strconv.ParseUint(got[6:], 16, 32)
	if hi < 0x7d8 || hi > 0x7da || lo < 0xbd9c27 || lo > 0xbd9c29 {
		t.Fatalf("volume 10 = %s, want stock's 0007d9 bd9c28 within one", got)
	}
	// Word 1 only ever rises with the volume: it is the level, not the gain's low bits.
	prev := int32(-1 << 23)

	for v := 1; v <= 100; v++ {
		w, _ := strconv.ParseUint(codecGainHex(audio.VolumeGain(v))[6:], 16, 32)

		db := int32(w<<8) >> 8
		if db < prev {
			t.Fatalf("volume %d: dB word %d below volume %d's %d", v, db, v-1, prev)
		}

		prev = db
	}
}

// Stock parity: the first change lands at once, the next no sooner than 250 ms
// later, and only the newest value of what came in between is written.
//
//nolint:paralleltest // changes package-wide state other tests read: pumpRest, syscall.Setpriority
func TestCodecVolumeWritesAtMostEveryGap(t *testing.T) {
	out := filepath.Join(t.TempDir(), "writes")

	ctx, cancel := context.WithCancel(t.Context())

	volume := newCodecVolume(ctx, []string{"sh", "-c", `echo "$1" >> ` + out, "sh"}, nil, func() {}, slog.Default())
	defer stopCodecVolume(t, cancel, volume)

	lines := func() []string {
		b, _ := os.ReadFile(out)

		return strings.Fields(string(b))
	}

	volume.SetGain(0.1)
	// Wait on the writes, not a clock: a busy machine can take longer than the gap
	// to start the first one.
	waitFor(t, "the first write", func() bool { return len(lines()) > 0 })
	volume.SetGain(0.2)
	volume.SetGain(0.3)
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
//
//nolint:paralleltest // swaps package variables other tests read: codecWriteRetry
func TestCodecVolumeWriteTriesAgainBeforeThePCM(t *testing.T) {
	defer func(d time.Duration) { codecWriteRetry = d }(codecWriteRetry)

	codecWriteRetry = time.Millisecond
	dir := t.TempDir()
	tries := filepath.Join(dir, "tries")
	// Fails the first time, as the ST20's bit-banged write did after an underrun.
	flaky := []string{"sh", "-c", `echo "$1" >> ` + tries + `; [ "$(wc -l < ` + tries + `)" -gt 1 ]`, "sh"}

	quiet := slog.New(slog.DiscardHandler)

	err := writeCodecGainTries(context.Background(), flaky, 1, codecWriteTries, nil, quiet)
	if err != nil {
		t.Fatalf("a write that fails once: %v", err)
	}

	written, _ := os.ReadFile(tries)
	if got := strings.Fields(string(written)); len(got) != 2 || got[1] != codecGainHex(1) {
		t.Fatalf("writes: %v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	failed := make(chan struct{}, codecWriteTries)
	refused := filepath.Join(dir, "refused")

	volume := newCodecVolume(ctx, []string{"sh", "-c", `echo "$1" >> ` + refused + `; exit 1`, "sh"}, nil,
		func() { failed <- struct{}{} }, quiet)
	defer stopCodecVolume(t, cancel, volume)

	volume.SetGain(0.2)

	select {
	case <-failed:
	case <-time.After(10 * time.Second):
		t.Fatal("a write that always fails never went back to the PCM")
	}

	written, _ = os.ReadFile(refused)
	if got := strings.Fields(string(written)); len(got) != codecWriteTries {
		t.Fatalf("tries before the PCM: %v, want %d", got, codecWriteTries)
	}

	if len(failed) != 0 {
		t.Fatal("failed called more than once")
	}
}

// A codec write runs below the Mixer's priority, with the gain as its last argument, and
// says what failed when it fails.
//
//nolint:paralleltest // changes package-wide state other tests read: syscall.Setpriority
func TestCodecWriteRunsNiced(t *testing.T) {
	dir := t.TempDir()
	pidFile, release, out := filepath.Join(dir, "pid"), filepath.Join(dir, "release"), filepath.Join(dir, "out")

	// The child is reniced after it starts and then runs at nice 10, which a loaded
	// machine starves: a child that ran ps in a loop itself outlived the write's timeout
	// (2026-11-28, two busy loops per core). It only says who it is and waits; the test
	// reads its nice from outside, waiting for the renice, and then lets it finish.
	err := syscall.Mkfifo(release, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	// Opened read-write so neither end blocks on the other; a line releases the child.
	fifo, err := os.OpenFile(release, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}

	defer fifo.Close()

	// The gain is the last argument, $3.
	script := `echo $$ > "$0.tmp" && mv "$0.tmp" "$0"; read go < "$1"; echo "$3" > "$2"`
	done := make(chan error, 1)

	go func() {
		done <- writeCodecGain(t.Context(), []string{"sh", "-c", script, pidFile, release, out}, 1)
	}()

	pid := waitForChildPID(t, pidFile, done)

	// A test run already below nice 10 (nice -n 19) cannot raise the child's priority,
	// so the test's own nice is the floor.
	want := max(codecWriteNice, processNice(t, os.Getpid()))
	got := processNice(t, pid)

	for deadline := time.Now().Add(codecWriteTimeout / 2); got != want && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)

		got = processNice(t, pid)
	}

	_, err = fifo.WriteString("go\n")
	if err != nil {
		t.Fatal(err)
	}

	err = <-done
	if err != nil {
		t.Fatal(err)
	}

	gain, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	if got != want || strings.TrimSpace(string(gain)) != codecGainHex(1) {
		t.Errorf("the write ran at nice %d with %q, want nice %d and gain %s", got, gain, want, codecGainHex(1))
	}

	err = writeCodecGain(t.Context(), []string{"sh", "-c", `echo no clock >&2; exit 3`}, 1)
	if err == nil || !strings.Contains(err.Error(), "no clock") {
		t.Errorf("failed write: %v, want its output", err)
	}
}

// waitForChildPID returns the pid a codec write's child wrote to path, failing if the write
// ends first or the child never gets that far.
func waitForChildPID(t *testing.T, path string, done <-chan error) int {
	t.Helper()

	for deadline := time.Now().Add(codecWriteTimeout); time.Now().Before(deadline); {
		select {
		case err := <-done:
			t.Fatalf("the write ended before its child started: %v", err)
		case <-time.After(10 * time.Millisecond):
		}

		written, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		pid, err := strconv.Atoi(strings.TrimSpace(string(written)))
		if err != nil {
			t.Fatalf("child pid %q: %v", written, err)
		}

		return pid
	}

	t.Fatal("the codec write's child never started")

	return 0
}

// processNice is a process's nice value as ps reports it.
func processNice(t *testing.T, pid int) int {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "ps", "-o", "nice=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatalf("ps for %d: %v", pid, err)
	}

	nice, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("nice of %d: %q: %v", pid, out, err)
	}

	return nice
}

// stopCodecVolume cancels a codec volume's writer and waits for it to stop, so that it
// cannot outlive its test and read a package variable the next test swaps: one still
// retrying a write after its test ended raced on codecWriteRetry (2026-11-28, -race under
// load).
func stopCodecVolume(t *testing.T, cancel context.CancelFunc, volume *codecVolume) {
	t.Helper()
	cancel()

	select {
	case <-volume.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the codec volume writer did not stop")
	}
}
