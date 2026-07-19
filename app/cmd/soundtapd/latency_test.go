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
	dir := t.TempDir()
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
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
	ms := time.Millisecond
	if got := leaderDelayBlocks(chainSample{sink: 585 * ms, backlog: 270 * ms}); got != 34 {
		t.Fatalf("leader delay = %d blocks, want 34 (685 ms)", got)
	}
	if got := leaderDelayBlocks(chainSample{sink: 1100 * ms}); got != 0 {
		t.Fatalf("a chain longer than the buffer cannot be delayed further, got %d", got)
	}
	m := meanSample([]chainSample{{sink: 100 * ms}, {sink: 300 * ms, upstream: 40 * ms}})
	if m.sink != 200*ms || m.upstream != 20*ms {
		t.Fatalf("mean = %+v", m)
	}
}

// FadeOut waits for afterMixer: the delay blocks plus the DAC ring, nothing upstream.
func TestAfterMixerCountsDelayBlocksAndTheRing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AUDIO_CLOCK_STATUS", filepath.Join(dir, "status"))
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte("delay       : 46875\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hw_params"), []byte("rate: 46875 (46875/1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := afterMixer(3), 3*blockDuration+time.Second; got != want {
		t.Fatalf("afterMixer(3) = %v, want %v", got, want)
	}
}

// A follower is heard once its chain settled and the leader read it, not before.
func TestFollowerHoldWaitsForSettledChainAndLeaderRead(t *testing.T) {
	ms := time.Millisecond
	var h followerHold
	if h.window(400*ms, 0); h.settled {
		t.Fatal("the first window with audio compares against the idle chain")
	}
	if h.window(405*ms, 400*ms); !h.settled {
		t.Fatal("5 ms of movement is settled")
	}
	if h.window(430*ms, 405*ms); h.settled {
		t.Fatal("25 ms of movement is not settled")
	}
	for range followerHoldMax - 4 {
		if h.window(500*ms, 430*ms) {
			t.Fatal("gave up early")
		}
	}
	if !h.window(600*ms, 500*ms) {
		t.Fatal("a chain that never settles is played after followerHoldMax windows")
	}

	var r chainReport
	r.publish(405 * ms)
	if r.applied(time.Now().Add(time.Hour)) {
		t.Fatal("never read")
	}
	r.Read()
	now := time.Now()
	if r.applied(now) {
		t.Fatal("read just now: the latency may not be set yet")
	}
	if !r.applied(now.Add(latencyApplied)) {
		t.Fatal("read latencyApplied ago")
	}
	r.publish(412 * ms)
	if !r.applied(now.Add(latencyApplied)) {
		t.Fatal("7 ms off what the leader read needs no new correction")
	}
	r.publish(440 * ms)
	if r.applied(now.Add(latencyApplied)) {
		t.Fatal("35 ms off what the leader read is not set yet")
	}
}
