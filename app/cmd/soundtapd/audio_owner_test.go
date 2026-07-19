// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"testing"
	"time"
)

// A muted amp opens after about 300 ms of sound the DAC kept up with, and an underrun in
// that window starts it again: the ST20 popped when the amp opened on the first block and
// the DAC ran dry right after.
func TestSinkSettleOpensOnlyOnASteadySink(t *testing.T) {
	var s sinkSettle
	if s.step(100, 7) {
		t.Fatal("open on the first block")
	}
	if s.step(100+unmuteSettleBlocks-1, 7) {
		t.Fatal("open before the window is over")
	}
	if s.step(100+unmuteSettleBlocks-1, 8) {
		t.Fatal("open across an underrun")
	}
	if s.step(100+2*unmuteSettleBlocks-2, 8) {
		t.Fatal("the window did not start again at the underrun")
	}
	if !s.step(100+2*unmuteSettleBlocks-1, 8) {
		t.Fatal("still shut after a whole steady window")
	}
}

type standbyFake struct{ standby, entered bool }

func (f *standbyFake) IsStandby() bool { return f.standby }
func (f *standbyFake) EnterStandby(context.Context) error {
	f.entered, f.standby = true, true
	return nil
}

// An install puts a playing speaker in standby first, so the music fades out before the
// install's work can starve the DAC; a speaker already in standby is left alone.
func TestAnInstallQuietsAPlayingSpeaker(t *testing.T) {
	playing := &standbyFake{}
	quietForInstall(playing)
	if !playing.entered {
		t.Fatal("a playing speaker was not put in standby")
	}
	idle := &standbyFake{standby: true}
	start := time.Now()
	quietForInstall(idle)
	if idle.entered || time.Since(start) > time.Second {
		t.Fatal("a speaker in standby was touched or waited for")
	}
}
