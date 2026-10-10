// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package sleeptimer is the speaker's sleep timer: one timer that, when it runs out,
// calls a fire function (standby) and retries it a bounded number of times. It is in
// memory only: a speaker that reboots comes up in standby, so a timer that survived the
// reboot would have nothing left to turn off.
package sleeptimer

import (
	"context"
	"sync"
	"time"
)

// Attempts is how often an expired timer tries to reach standby, RetryEvery
// the pause between tries. A standby that fails once (a backend busy for a moment) is
// worth trying again; one that fails three times will not be fixed by a fourth, and the
// timer must end some time.
const (
	Attempts   = 3
	RetryEvery = 10 * time.Second
)

// Timer is the one running timer. Its zero value is "no timer".
//
// gen identifies the current timer. Every set or cancel moves it on, and a callback
// that finds gen moved on does nothing: Stop does not wait for a callback that has
// already started, so without it a timer firing in the same instant it is replaced or
// cancelled would still turn the speaker off. That check alone comes too late for a
// callback already inside fire, so each timer also has a context, cancelled with it,
// that fire passes down to recheck right before it acts. firing is the gen whose fire
// is running, 0 for none.
type Timer struct {
	mu      sync.Mutex
	timer   *time.Timer
	cancel  context.CancelFunc
	gen     uint64
	firing  uint64
	minutes int
	endsAt  time.Time
}

// Set replaces whatever is running with a timer of minutes*unit, or with nothing for
// minutes == 0. When it runs out it calls fire (on the timer's goroutine, without the
// lock) with the timer's context and the attempt number, 1-based; while fire fails it
// tries again every retry, up to Attempts times. Until fire succeeds or the attempts are
// spent the timer stays visible as running, endsAt then being the next attempt.
func (t *Timer) Set(minutes int, unit, retry time.Duration, fire func(ctx context.Context, attempt int) error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopLocked()
	if minutes <= 0 {
		return
	}
	d := time.Duration(minutes) * unit
	t.minutes, t.endsAt = minutes, time.Now().Add(d)
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.scheduleLocked(ctx, t.gen, d, retry, fire, 1)
}

// stopLocked clears the timer and moves gen on. Caller holds t.mu.
func (t *Timer) stopLocked() {
	if t.timer != nil {
		t.timer.Stop()
	}
	if t.cancel != nil {
		t.cancel()
	}
	t.gen++
	t.timer, t.cancel, t.minutes, t.endsAt = nil, nil, 0, time.Time{}
}

// scheduleLocked arms attempt number attempt of timer gen after d. Caller holds t.mu,
// which the callback also takes first, so it cannot see t.timer before it is recorded.
func (t *Timer) scheduleLocked(ctx context.Context, gen uint64, d, retry time.Duration, fire func(context.Context, int) error, attempt int) {
	t.timer = time.AfterFunc(d, func() {
		t.mu.Lock()
		current := t.gen == gen
		if current {
			t.firing = gen
		}
		t.mu.Unlock()
		if !current {
			return
		}
		err := fire(ctx, attempt)
		t.mu.Lock()
		defer t.mu.Unlock()
		t.firing = 0
		if t.gen != gen {
			return // cancelled or replaced while fire ran (a successful standby does that)
		}
		if err == nil || attempt >= Attempts {
			t.stopLocked()
			return
		}
		t.endsAt = time.Now().Add(retry)
		t.scheduleLocked(ctx, gen, retry, retry, fire, attempt+1)
	})
}

// Cancel stops a running timer, if there is one.
func (t *Timer) Cancel() { t.Set(0, 0, 0, nil) }

// CancelFiring is Cancel for a standby, which may be the one a firing timer caused:
// while a fire runs it stops only that fire's timer, so one set meanwhile is left
// alone. A standby from elsewhere landing during a fire is taken for the fire's own.
func (t *Timer) CancelFiring() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.firing == 0 || t.firing == t.gen {
		t.stopLocked()
	}
}

// View is the API shape: {"minutes": 0, "endsAt": ""} when nothing is running.
func (t *Timer) View() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	ends := ""
	if t.minutes > 0 {
		ends = t.endsAt.UTC().Format(time.RFC3339)
	}
	return map[string]any{"minutes": t.minutes, "endsAt": ends}
}
