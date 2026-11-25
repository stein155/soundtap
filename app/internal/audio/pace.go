// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package audio

import (
	"context"
	"time"
)

// paceMaxBehind is how far behind the wall clock the Mixer may fall and still make the
// time up. A longer stall is written off: catching up more would only burst into a sink
// that has already played silence for it.
const paceMaxBehind = 400 * time.Millisecond

// paceClock clocks the Mixer one block per period against the wall clock. A time.Ticker
// drops the ticks a busy loop misses, so every stall left the Mixer short for good: on an
// ST20 starting a Spotify track it wrote 8% below real time for seconds and aplay ran dry
// ("underrun!!!", heard as a pop). paceClock keeps a deadline instead and, when late,
// returns at once until it has caught up.
type paceClock struct {
	period time.Duration
	next   time.Time
	timer  *time.Timer
	now    func() time.Time
}

func newPaceClock(period time.Duration) *paceClock {
	t := time.NewTimer(time.Hour)
	t.Stop()

	return &paceClock{period: period, next: time.Time{}, timer: t, now: time.Now}
}

func (p *paceClock) stop() { p.timer.Stop() }

// wait returns when the next block is due, at once while behind; false when ctx ended.
func (p *paceClock) wait(ctx context.Context) bool {
	now := p.now()
	if p.next.IsZero() || now.Sub(p.next) > paceMaxBehind {
		p.next = now
	}

	p.next = p.next.Add(p.period)

	d := p.next.Sub(now)
	if d <= 0 {
		return ctx.Err() == nil
	}

	p.timer.Reset(d)

	select {
	case <-ctx.Done():
		p.timer.Stop()

		return false
	case <-p.timer.C:
		return true
	}
}
