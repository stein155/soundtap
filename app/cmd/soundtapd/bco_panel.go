// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/bco"
)

// bcoNoticeMax bounds the panel notice of a replacement that never ends it: the read of
// stock's Wi-Fi profile (up to 5 minutes, ADR 0129), a flash attempt (bcoFlashLimit) and the
// wait for the reboot after it (bcoRebootWait) fit in it.
const bcoNoticeMax = 25 * time.Minute

// bcoNoticeUntil is when the notice of bcoUpdateNotice lapses, in Unix nanoseconds; 0 when
// none is up.
//
//nolint:gochecknoglobals // process-wide state: the BCO install sets it, the boot screen reads it
var bcoNoticeUntil atomic.Int64

// bcoUpdateNotice keeps the ST20 panel on "Updating · Network card · Do not unplug" for the
// whole replacement of stock's BCO app, not only while bco.Flash holds the BCO: before the
// flash soundtapd reads stock's Wi-Fi profile, and after it the speaker waits for the boot
// confirmation and restarts, all without a network. Without it the panel says "Connecting…"
// for minutes, which reads as a fault. replacing is false on a boot that finds our own app:
// then nothing changes. The returned function takes the notice down; it also lapses after
// bcoNoticeMax.
//
//nolint:revive // flag-parameter: the caller defers bcoUpdateNotice(!ownBCOApp())() in one line
func bcoUpdateNotice(replacing bool) func() {
	if !replacing {
		return func() {}
	}

	bcoNoticeUntil.Store(time.Now().Add(bcoNoticeMax).UnixNano())

	return func() { bcoNoticeUntil.Store(0) }
}

// bcoUpdating is the boot screen's NetCard: a BCO flash runs, or a replacement of stock's
// BCO app does.
func bcoUpdating() bool {
	return bco.Flashing() || time.Now().UnixNano() < bcoNoticeUntil.Load()
}
