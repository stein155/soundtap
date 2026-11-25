// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
)

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for " + what)
		}

		time.Sleep(5 * time.Millisecond)
	}
}

func TestFollowBluetoothLeavesAndDropsTheLink(t *testing.T) {
	t.Parallel()

	speaker := speakerstate.New()
	marker := filepath.Join(t.TempDir(), "selected")

	var pauses, drops atomic.Int32

	ctx := t.Context()

	go followBluetooth(ctx, speaker, marker, func() {},
		func() error {
			pauses.Add(1)

			return nil
		}, func() error {
			drops.Add(1)

			return nil
		},
		50*time.Millisecond, slog.Default())

	exists := func() bool {
		_, err := os.Stat(marker)

		return err == nil
	}

	speaker.ApplyKey(sourceBluetooth)
	waitFor(t, "the marker", exists)
	speaker.ApplyKey("AUX_INPUT")
	waitFor(t, "the marker to go", func() bool { return !exists() })
	waitFor(t, "the pause", func() bool { return pauses.Load() == 1 })
	waitFor(t, "the link to drop", func() bool { return drops.Load() == 1 })
}

func TestFollowBluetoothKeepsTheLinkWhenSelectedAgain(t *testing.T) {
	t.Parallel()

	speaker := speakerstate.New()
	marker := filepath.Join(t.TempDir(), "selected")

	var drops atomic.Int32

	ctx := t.Context()

	go followBluetooth(ctx, speaker, marker, func() {},
		func() error { return nil }, func() error {
			drops.Add(1)

			return nil
		},
		200*time.Millisecond, slog.Default())

	exists := func() bool {
		_, err := os.Stat(marker)

		return err == nil
	}

	speaker.ApplyKey(sourceBluetooth)
	waitFor(t, "the marker", exists)
	speaker.ApplyKey("AUX_INPUT")
	waitFor(t, "the marker to go", func() bool { return !exists() })
	speaker.ApplyKey(sourceBluetooth)
	waitFor(t, "the marker again", exists)
	time.Sleep(300 * time.Millisecond)

	if drops.Load() != 0 {
		t.Fatal("dropped the link of a Bluetooth selected again")
	}
}
