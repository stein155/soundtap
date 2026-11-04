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
	base := time.Unix(1000, 0)
	now := base
	p := newPaceClock(20 * time.Millisecond)
	p.now = func() time.Time { return now }
	p.next = base
	now = base.Add(100 * time.Millisecond) // stalled 100 ms: five blocks due
	due := 0
	for p.next.Add(p.period).Before(now) || p.next.Add(p.period).Equal(now) {
		if !p.wait(context.Background()) {
			t.Fatal("wait false")
		}
		due++
	}
	if due != 5 {
		t.Fatalf("caught up %d blocks, want 5", due)
	}
	now = p.next.Add(time.Second) // a long stall restarts the clock
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.wait(ctx)
	if want := now.Add(p.period); !p.next.Equal(want) {
		t.Fatalf("next = %v, want %v", p.next, want)
	}
}
