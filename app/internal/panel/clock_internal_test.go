// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package panel

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/clocksync"
	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/oled"
)

// Turning the clock off removes the frame it had put up.
//
//nolint:paralleltest // swaps clocksync.Path, a package-level setting of another package
func TestClockOffClearsStandbyFrame(t *testing.T) {
	fbPath := filepath.Join(t.TempDir(), "fb")

	err := os.WriteFile(fbPath, make([]byte, oled.Width*oled.Height), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	disp := display.New(ctx, fbPath, func(context.Context) bool { return true }, slog.New(slog.DiscardHandler))
	// Like stock, no clock before the first NTP sync of the boot.
	synced := filepath.Join(t.TempDir(), "clock-synced")
	old := clocksync.Path

	clocksync.Path = synced
	defer func() { clocksync.Path = old }()

	var off atomic.Bool

	go clockScreenEvery(ctx, disp, func() settings.Display {
		return settings.Display{Timezone: "UTC", ClockOff: off.Load(), Hour12: false}
	}, slog.New(slog.DiscardHandler), time.Millisecond)

	wait := func(what string, cond func() bool) {
		t.Helper()

		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			if cond() {
				return
			}

			time.Sleep(5 * time.Millisecond)
		}

		t.Fatalf("timed out waiting for %s", what)
	}

	time.Sleep(50 * time.Millisecond)

	if disp.HasStandby() {
		t.Fatal("a clock before the first NTP sync")
	}

	err = os.WriteFile(synced, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	wait("the clock frame", disp.HasStandby)
	off.Store(true)
	wait("the clock frame to go", func() bool { return !disp.HasStandby() })
}

// The standby clock walks as stock's does: one pixel diagonally per step, bouncing off
// the edges, never onto the panel's outer pixels or its bottom two rows, and over a full
// round every row and column of the free area is reached.
func TestStandbyClockWalks(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 25, 23, 40, 0, 0, time.UTC)
	firstDraw, _ := standbyFrame(base, false)
	first := firstDraw()

	var prevX, prevY int

	cols, rows := map[int]bool{}, map[int]bool{}

	for step := range 400 {
		when := base.Add(time.Duration(step) * walkStep)
		draw, _ := standbyFrame(when, false)

		frame := draw()
		if litPixels(frame) < 200 {
			t.Fatalf("step %d: the clock is nearly blank", step)
		}

		box := litBox(frame)
		left, top, right, bottom := box.Min.X, box.Min.Y, box.Max.X, box.Max.Y

		inside := left >= walkMargin && top >= walkMargin &&
			right <= oled.Width-1-walkMargin && bottom <= oled.Height-1-walkBottom
		if !inside {
			t.Fatalf("step %d: lit box (%d,%d)-(%d,%d) leaves the walk's area", step, left, top, right, bottom)
		}

		sameMinute := when.Minute() == when.Add(-walkStep).Minute()
		if step > 0 && sameMinute && (abs(left-prevX) > 1 || abs(top-prevY) > 1) {
			t.Fatalf("step %d: jumped from (%d,%d) to (%d,%d)", step, prevX, prevY, left, top)
		}

		prevX, prevY = left, top
		cols[left], rows[top] = true, true
	}

	if len(cols) < 10 || len(rows) < 20 {
		t.Errorf("the walk reached %d columns and %d rows", len(cols), len(rows))
	}

	again, _ := standbyFrame(base, false)
	if !bytes.Equal(again(), first) {
		t.Error("the same moment drew a different place: a restart would jump")
	}
}

// The standby screen is the time alone, as stock's: on a 12-hour clock AM/PM sits under
// it and nothing else is drawn.
func TestStandbyIsTheTimeAlone(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 25, 20, 42, 0, 0, time.UTC)
	draw24, key24 := standbyFrame(at, false)
	draw12, key12 := standbyFrame(at, true)
	tall24 := litBox(draw24()).Dy()

	tall12 := litBox(draw12()).Dy()
	if tall24 >= tall12 {
		t.Errorf("24-hour clock %d rows tall, 12-hour with PM %d: want PM on a line under it", tall24, tall12)
	}

	if !strings.Contains(key12, "PM") || strings.Contains(key24, "PM") {
		t.Errorf("keys: 24-hour %q, 12-hour %q", key24, key12)
	}
}

func TestBounce(t *testing.T) {
	t.Parallel()

	got := make([]int, 0, 9)
	for s := range 9 {
		got = append(got, bounce(s, 3))
	}

	if fmt.Sprint(got) != "[0 1 2 3 2 1 0 1 2]" {
		t.Errorf("bounce over 0..3: %v", got)
	}

	if bounce(7, 0) != 0 {
		t.Error("no room still moves")
	}
}

// litBox is the box around the lit pixels, its corners inclusive: Max is the last lit
// column and row, not one past them.
func litBox(frame []byte) image.Rectangle {
	box := image.Rectangle{Min: image.Pt(oled.Width, oled.Height), Max: image.Pt(-1, -1)}

	for i, v := range frame {
		if v != 0 {
			x, y := i%oled.Width, i/oled.Width
			box.Min.X, box.Max.X = min(box.Min.X, x), max(box.Max.X, x)
			box.Min.Y, box.Max.Y = min(box.Min.Y, y), max(box.Max.Y, y)
		}
	}

	return box
}

func abs(v int) int { return max(v, -v) }
