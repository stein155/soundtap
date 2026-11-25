// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAlsaDelayReadsTheRing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write := func(name, text string) {
		err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644)
		if err != nil {
			t.Fatal(err)
		}
	}
	write("status", "state: RUNNING\nhw_ptr      : 23912818\nappl_ptr    : 23925000\ndelay       : 14062\n")
	write("hw_params", "access: RW_INTERLEAVED\nrate: 46875 (46875/1)\nbuffer_size: 15000\n")

	if got := alsaDelay(dir); got != 299989333*time.Nanosecond {
		t.Fatalf("alsaDelay = %v, want 14062 frames at 46875 Hz", got)
	}

	write("status", "closed\n")

	if got := alsaDelay(dir); got != 0 {
		t.Fatalf("a closed card has no delay, got %v", got)
	}
}

// Numbers measured on the WLA leading the ST10 and ST20, 2026-10-09: 320 ms in aplay's
// pipe and 265 ms in the ring, 270 ms waiting for snapserver.
func TestLeaderDelayFillsUpToSnapcastsBuffer(t *testing.T) {
	t.Parallel()

	milli := time.Millisecond

	if got := leaderDelayBlocks(chainSample{sink: 585 * milli, upstream: 0, backlog: 270 * milli}); got != 34 {
		t.Fatalf("leader delay = %d blocks, want 34 (685 ms)", got)
	}

	if got := leaderDelayBlocks(chainSample{sink: 1100 * milli, upstream: 0, backlog: 0}); got != 0 {
		t.Fatalf("a chain longer than the buffer cannot be delayed further, got %d", got)
	}

	mean := meanSample([]chainSample{
		{sink: 100 * milli, upstream: 0, backlog: 0},
		{sink: 300 * milli, upstream: 40 * milli, backlog: 0},
	})
	if mean.sink != 200*milli || mean.upstream != 20*milli {
		t.Fatalf("mean = %+v", mean)
	}
}

// FadeOut waits for afterMixer: the delay blocks plus the DAC ring, nothing upstream.
func TestAfterMixerCountsDelayBlocksAndTheRing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AUDIO_CLOCK_STATUS", filepath.Join(dir, "status"))

	err := os.WriteFile(filepath.Join(dir, "status"), []byte("delay       : 46875\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "hw_params"), []byte("rate: 46875 (46875/1)\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := afterMixer(3), 3*blockDuration+time.Second; got != want {
		t.Fatalf("afterMixer(3) = %v, want %v", got, want)
	}
}

// A follower is heard once its chain settled and the leader read it, not before.
func TestFollowerHoldWaitsForSettledChainAndLeaderRead(t *testing.T) {
	t.Parallel()

	milli := time.Millisecond

	var hold followerHold

	hold.window(400*milli, 0)

	if hold.settled {
		t.Fatal("the first window with audio compares against the idle chain")
	}

	hold.window(405*milli, 400*milli)

	if !hold.settled {
		t.Fatal("5 ms of movement is settled")
	}

	hold.window(430*milli, 405*milli)

	if hold.settled {
		t.Fatal("25 ms of movement is not settled")
	}

	for range followerHoldMax - 4 {
		if hold.window(500*milli, 430*milli) {
			t.Fatal("gave up early")
		}
	}

	if !hold.window(600*milli, 500*milli) {
		t.Fatal("a chain that never settles is played after followerHoldMax windows")
	}

	var report chainReport
	report.publish(405 * milli)

	if report.applied(time.Now().Add(time.Hour)) {
		t.Fatal("never read")
	}

	report.Read()

	now := time.Now()
	if report.applied(now) {
		t.Fatal("read just now: the latency may not be set yet")
	}

	if !report.applied(now.Add(latencyApplied)) {
		t.Fatal("read latencyApplied ago")
	}

	report.publish(412 * milli)

	if !report.applied(now.Add(latencyApplied)) {
		t.Fatal("7 ms off what the leader read needs no new correction")
	}

	report.publish(440 * milli)

	if report.applied(now.Add(latencyApplied)) {
		t.Fatal("35 ms off what the leader read is not set yet")
	}
}
