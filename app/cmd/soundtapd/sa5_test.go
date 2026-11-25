// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import "testing"

// The SA-5 stays muted until its controller has been read, and for good when it cannot be;
// a model without a controller is not held.
//
//nolint:paralleltest // swaps package variables other tests read: board
func TestTheSA5StaysMutedUntilItsControllerAgrees(t *testing.T) {
	saved, savedGate := board, unmuteGate.Load()

	t.Cleanup(func() { board = saved; unmuteGate.Store(savedGate) })
	unmuteGate.Store(nil)

	board.STM32Amp = false

	if !mayUnmute() {
		t.Error("a model without a controller is held muted")
	}

	board.STM32Amp = true

	if mayUnmute() {
		t.Error("the SA-5 unmuted before its controller was read")
	}

	amplifierOn := false
	gate := func() bool { return amplifierOn }
	unmuteGate.Store(&gate)

	if mayUnmute() {
		t.Error("the SA-5 unmuted while its controller says off")
	}

	amplifierOn = true

	if !mayUnmute() {
		t.Error("the SA-5 stayed muted with its amplifier on")
	}
}
