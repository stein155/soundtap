// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"context"
	"testing"
	"time"
)

// A stall shorter than paceMaxBehind is made up with back-to-back blocks; a longer one is
// written off.
func TestPaceClockCatchesUp(t *testing.T) {
	t.Parallel()

	base := time.Unix(1000, 0)
	now := base
	clock := newPaceClock(20 * time.Millisecond)
	clock.now = func() time.Time { return now }
	clock.next = base
	now = base.Add(100 * time.Millisecond) // stalled 100 ms: five blocks due
	due := 0

	for clock.next.Add(clock.period).Before(now) || clock.next.Add(clock.period).Equal(now) {
		if !clock.wait(context.Background()) {
			t.Fatal("wait false")
		}

		due++
	}

	if due != 5 {
		t.Fatalf("caught up %d blocks, want 5", due)
	}

	now = clock.next.Add(time.Second) // a long stall restarts the clock
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	clock.wait(ctx)

	if want := now.Add(clock.period); !clock.next.Equal(want) {
		t.Fatalf("next = %v, want %v", clock.next, want)
	}
}
