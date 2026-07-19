// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import "time"

// startRepeat steps the volume now and keeps stepping until the key is released.
// A duplicate press (a release edge lost on the wire) restarts rather than
// stacking a second ramp.
func (h *buttonHandler) startRepeat(index int, action string) {
	h.stopRepeat(index)
	stop := make(chan struct{})
	h.repeats[index] = stop
	h.log.Info("button", "key", action)
	h.sp.ApplyKey(action)
	go func() {
		t := time.NewTimer(volumeRepeatDelay)
		defer t.Stop()
		// A release byte can be lost, and a ramp with no end would drive the
		// volume to a rail and stay there. Give up after a hold no human means.
		giveUp := time.NewTimer(volumeRampMax)
		defer giveUp.Stop()
		for {
			select {
			case <-stop:
				return
			case <-giveUp.C:
				h.log.Warn("volume key held too long, releasing", "key", action)
				return
			case <-t.C:
				h.sp.ApplyKey(action)
				t.Reset(volumeRepeat)
			}
		}
	}()
}

func (h *buttonHandler) stopRepeat(index int) {
	if stop, ok := h.repeats[index]; ok {
		close(stop)
		delete(h.repeats, index)
	}
}

// reset drops every in-flight gesture. Called when the device disappears, so a
// press whose release we will now never see cannot leave a ramp running.
func (h *buttonHandler) Reset() {
	h.cancelGestures()
	h.disarmChord()
	// A hold whose release will never come is not a hold anyone can vouch for.
	if h.confirmTimer != nil {
		h.confirmTimer.Stop()
	}
	h.confirmKey, h.confirmTimer = "", nil
	for action := range h.down {
		delete(h.down, action)
		delete(h.downIndex, action)
	}
}
