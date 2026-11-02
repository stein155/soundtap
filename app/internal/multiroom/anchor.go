// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package multiroom

import (
	"sync"
	"time"
)

// AnchorHold is how long this speaker's anchor keeps running after the last sign that it
// leads or is about to lead a group. A group being set up is a burst of invites and
// membership changes, and WatchMultiroom sees a leader's followers every 15 s even in
// standby: the anchor stays up through both, and goes 1 minute after the group is gone.
const AnchorHold = time.Minute

// AnchorDemand says whether this speaker's own snapclient should run as the silent group
// anchor. In plain standby it does not: the anchor rendered silence into /dev/null
// 35 times a second, about 0.8 % CPU on a Wireless Link, all day. Leading a group, or being
// about to, is a Want, and the anchor runs from then until Hold has passed without another.
// Its zero value never wants the anchor; use NewAnchorDemand.
type AnchorDemand struct {
	hold    time.Duration
	changed func()

	mu    sync.Mutex
	until time.Time
	timer *time.Timer
}

// NewAnchorDemand returns a demand that keeps the anchor for hold after each Want and calls
// changed, without the lock held, whenever Wanted may have turned: at a Want that starts
// the demand, and when the hold runs out.
func NewAnchorDemand(hold time.Duration, changed func()) *AnchorDemand {
	return &AnchorDemand{hold: hold, changed: changed}
}

// Want starts the anchor if it is not running and keeps it for the hold from now.
func (a *AnchorDemand) Want() {
	a.mu.Lock()
	now := time.Now()
	starts := !now.Before(a.until)
	a.until = now.Add(a.hold)
	if a.timer == nil {
		a.timer = time.AfterFunc(a.hold, a.expire)
	} else {
		a.timer.Reset(a.hold)
	}
	a.mu.Unlock()
	if starts && a.changed != nil {
		a.changed()
	}
}

// expire tells the process gate to look again once the hold may have run out.
func (a *AnchorDemand) expire() {
	if a.changed != nil {
		a.changed()
	}
}

// Wanted reports whether the anchor should run now.
func (a *AnchorDemand) Wanted() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Now().Before(a.until)
}
