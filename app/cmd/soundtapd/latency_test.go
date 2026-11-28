// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"math/rand/v2"
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

// A follower asks for its chain less what snapclient's file player already covers: half
// of its 50 ms write, which the meter's mean counts but the write's first frame does not
// wait for, and the 10 ms it plays early by itself.
func TestFollowerLatencyLeavesOutSnapclientsOwnShare(t *testing.T) {
	t.Parallel()

	milli := time.Millisecond

	if got := followerLatency(chainSample{sink: 300 * milli, upstream: 100 * milli, backlog: 0}); got != 365*milli {
		t.Fatalf("followerLatency = %v, want 400 ms less 25 and 10", got)
	}

	if got := followerLatency(chainSample{sink: 0, upstream: 20 * milli, backlog: 0}); got != 0 {
		t.Fatalf("an idle chain asks for no latency, got %v", got)
	}
}

// The leader's residual is the exact target less the whole blocks it holds: positive when
// it plays early. With it, leader and follower sound at the same moment to the ms.
func TestLeaderTrimIsWhatTheBlocksMiss(t *testing.T) {
	t.Parallel()

	milli := time.Millisecond
	sample := chainSample{sink: 585 * milli, upstream: 0, backlog: 268 * milli} // target 683 ms

	have := leaderDelayBlocks(sample) // 34 blocks, 680 ms
	if got := leaderTrim(sample, have); got != 3*milli {
		t.Fatalf("leaderTrim = %v at %d blocks, want 3 ms", got, have)
	}
	// One block off is under leaderDelayStep, so a leader at 33 blocks does not move.
	if got := leaderTrim(sample, 33); got != 23*milli {
		t.Fatalf("leaderTrim = %v at 33 blocks, want 23 ms", got)
	}

	// A chunk snapserver reads at t sounds on the leader at t - backlog + delay + sink,
	// and on a follower at t + buffer - latency + chain, where latency = chain + trim.
	const chain = 400 * time.Millisecond

	leader := -sample.backlog + 33*blockDuration + sample.sink
	follower := snapcastBuffer - (chain + leaderTrim(sample, 33)) + chain

	if leader != follower {
		t.Fatalf("leader sounds at +%v, follower at +%v", leader, follower)
	}
}

// Each look falls at a point in its own interval, the intervals follow on without
// drifting, and a stall starts them over instead of looking several times at once.
func TestSampleClockLooksOncePerInterval(t *testing.T) {
	t.Parallel()

	start := time.Unix(1000, 0)
	offsets := []time.Duration{100 * time.Millisecond, 450 * time.Millisecond, 0}
	i := 0
	clock := newSampleClock(start, 500*time.Millisecond, func(time.Duration) time.Duration {
		d := offsets[i%len(offsets)]
		i++

		return d
	})

	now := start
	for _, want := range []time.Duration{100 * time.Millisecond, 850 * time.Millisecond, 50 * time.Millisecond} {
		got := clock.next(now)
		if got != want {
			t.Fatalf("wait = %v, want %v", got, want)
		}

		now = now.Add(got)
	}

	// Two seconds late: the next look is in a fresh interval from now.
	now = now.Add(2 * time.Second)
	if got := clock.next(now); got != 100*time.Millisecond {
		t.Fatalf("after a stall wait = %v, want 100 ms", got)
	}

	// A new pace starts its intervals at once.
	clock.reset(now, chainHeldEvery)
	clock.jitter = func(d time.Duration) time.Duration { return d / 2 }

	if got := clock.next(now); got != chainHeldEvery/2 {
		t.Fatalf("after reset wait = %v, want half of %v", got, chainHeldEvery)
	}
}

// The beats runChainMeter measures (snapclient's 50 ms writes, the 20 ms blocks, the
// DAC's 80 ms periods) fill a buffer at once and drain it evenly. Looked at on a fixed
// 500 ms ticker, the mean depends on where in the beat the ticker happens to fall; with
// sampleClock it is the true mean, half the beat.
func TestSampleClockMeasuresTheTrueMeanOfABeat(t *testing.T) {
	t.Parallel()

	sawtooth := func(at, beat time.Duration) time.Duration { return beat - at%beat }

	for _, every := range []time.Duration{chainEvery, chainHeldEvery} {
		for _, beat := range []time.Duration{20 * time.Millisecond, 50 * time.Millisecond, 80 * time.Millisecond} {
			rng := rand.New(rand.NewPCG(1, uint64(beat)))
			clock := newSampleClock(time.Unix(0, 0), every, func(d time.Duration) time.Duration {
				return time.Duration(rng.Int64N(int64(d)))
			})

			const samples = 4000

			now, sum := time.Unix(0, 0), time.Duration(0)
			for range samples {
				now = now.Add(clock.next(now))
				sum += sawtooth(time.Duration(now.UnixNano()), beat)
			}

			if mean := sum / samples; (mean - beat/2).Abs() > 2*time.Millisecond {
				t.Errorf("every %v, beat %v: mean %v, want %v", every, beat, mean, beat/2)
			}
		}
	}

	// The fixed ticker it replaces: every look at the same point of a 50 ms beat.
	var sum time.Duration
	for k := range 10 {
		sum += sawtooth(time.Duration(k)*chainEvery+7*time.Millisecond, 50*time.Millisecond)
	}

	if mean := sum / 10; mean != 43*time.Millisecond {
		t.Fatalf("fixed ticker mean = %v, want 43 ms: 18 ms off the true 25", mean)
	}
}
