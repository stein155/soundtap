// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"testing"
	"time"
)

//nolint:paralleltest // sets a package variable other tests read: bcoNoticeUntil
func TestBCOUpdateNoticeCoversTheReplacementOnly(t *testing.T) {
	t.Cleanup(func() { bcoNoticeUntil.Store(0) })

	if bcoUpdating() {
		t.Fatal("notice up before any BCO work")
	}

	bcoUpdateNotice(false)()

	if bcoUpdating() {
		t.Fatal("a boot that finds our own app put the notice up")
	}

	end := bcoUpdateNotice(true)

	if !bcoUpdating() {
		t.Fatal("no notice while stock's BCO app is being replaced")
	}

	end()

	if bcoUpdating() {
		t.Fatal("notice still up after the replacement ended")
	}
}

//nolint:paralleltest // sets a package variable other tests read: bcoNoticeUntil
func TestBCOUpdateNoticeLapses(t *testing.T) {
	t.Cleanup(func() { bcoNoticeUntil.Store(0) })

	bcoUpdateNotice(true)

	if until := time.Unix(0, bcoNoticeUntil.Load()); time.Until(until) > bcoNoticeMax {
		t.Fatalf("notice runs until %v, past bcoNoticeMax", until)
	}

	// A replacement whose end never comes (a retry loop, a reboot that did not happen).
	bcoNoticeUntil.Store(time.Now().Add(-time.Second).UnixNano())

	if bcoUpdating() {
		t.Fatal("notice did not lapse")
	}
}
