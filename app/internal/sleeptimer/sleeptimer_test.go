// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package sleeptimer_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/sleeptimer"
)

var errBusy = errors.New("busy")

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()

	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		if ok() {
			return
		}
	}

	t.Fatal("condition not met within 2s")
}

func TestFiresOnceAndClears(t *testing.T) {
	t.Parallel()

	var (
		timer sleeptimer.Timer
		fired atomic.Int32
	)

	timer.Set(2, time.Millisecond, time.Millisecond, func(context.Context, int) error {
		fired.Add(1)

		return nil
	})

	if timer.View()["minutes"] != 2 {
		t.Fatalf("view %v: want the running timer", timer.View())
	}

	waitUntil(t, func() bool { return fired.Load() == 1 && timer.View()["minutes"] == 0 })
}

func TestRetriesAFailingFireThenGivesUp(t *testing.T) {
	t.Parallel()

	var (
		timer sleeptimer.Timer
		tries atomic.Int32
	)

	timer.Set(1, time.Millisecond, time.Millisecond, func(context.Context, int) error {
		tries.Add(1)

		return errBusy
	})
	waitUntil(t, func() bool { return tries.Load() == sleeptimer.Attempts && timer.View()["minutes"] == 0 })
	time.Sleep(10 * time.Millisecond)

	if n := tries.Load(); n != sleeptimer.Attempts {
		t.Fatalf("fired %d times, want %d", n, sleeptimer.Attempts)
	}
}

func TestCancelAndReplaceNeverFireTheOldTimer(t *testing.T) {
	t.Parallel()

	var (
		timer sleeptimer.Timer
		old   atomic.Int32
	)

	timer.Set(5, time.Millisecond, time.Millisecond, func(context.Context, int) error {
		old.Add(1)

		return nil
	})
	timer.Cancel()
	timer.Set(5, time.Millisecond, time.Millisecond, func(context.Context, int) error {
		old.Add(1)

		return nil
	})
	timer.Set(60, time.Hour, time.Hour, func(context.Context, int) error {
		t.Error("the replacement fired")

		return nil
	})
	time.Sleep(20 * time.Millisecond)

	if n := old.Load(); n != 0 {
		t.Fatalf("a cancelled or replaced timer fired %d times", n)
	}

	timer.Cancel()
}

// A fire already running when its timer is replaced sees its context end, and the
// standby it then causes (CancelFiring) leaves the replacement alone.
func TestReplaceReachesAFireInProgress(t *testing.T) {
	t.Parallel()

	var timer sleeptimer.Timer

	started, release, done := make(chan context.Context), make(chan struct{}), make(chan struct{})

	timer.Set(1, time.Millisecond, time.Hour, func(ctx context.Context, _ int) error {
		started <- ctx

		<-release
		timer.CancelFiring()
		close(done)

		return nil
	})

	var ctx context.Context
	select {
	case ctx = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the timer never fired")
	}

	timer.Set(60, time.Hour, time.Hour, func(context.Context, int) error {
		t.Error("the replacement fired")

		return nil
	})

	if ctx.Err() == nil {
		t.Fatal("replacing the timer left the running fire's context alive")
	}

	close(release)
	<-done

	if m := timer.View()["minutes"]; m != 60 {
		t.Fatalf("minutes = %v after the old fire's standby, want the replacement's 60", m)
	}
	// Once the old fire has returned, an ordinary standby ends the timer. done closes just
	// before that return, so give the fire's goroutine a moment to clear firing.
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(time.Millisecond) {
		timer.CancelFiring()

		if m := timer.View()["minutes"]; m == 0 {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("minutes = %v after a standby, want 0", m)
		}
	}
}
