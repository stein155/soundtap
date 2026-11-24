// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package panel

import (
	"context"
	"fmt"
	"io"
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
func TestClockOffClearsStandbyFrame(t *testing.T) {
	oldTick := clockScreenTick
	clockScreenTick = time.Millisecond
	defer func() { clockScreenTick = oldTick }()
	fb := filepath.Join(t.TempDir(), "fb")
	if err := os.WriteFile(fb, make([]byte, oled.Width*oled.Height), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	disp := display.New(ctx, fb, func(context.Context) bool { return true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Like stock, no clock before the first NTP sync of the boot.
	synced := filepath.Join(t.TempDir(), "clock-synced")
	old := clocksync.Path
	clocksync.Path = synced
	defer func() { clocksync.Path = old }()
	var off atomic.Bool
	go RunClockScreen(ctx, disp, func() settings.Display {
		return settings.Display{Timezone: "UTC", ClockOff: off.Load()}
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	if err := os.WriteFile(synced, nil, 0o600); err != nil {
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
	base := time.Date(2026, 9, 25, 23, 40, 0, 0, time.UTC)
	firstDraw, _ := standbyFrame(base, false)
	first := firstDraw()
	var prevX, prevY int
	xs, ys := map[int]bool{}, map[int]bool{}
	for i := range 400 {
		at := base.Add(time.Duration(i) * walkStep)
		draw, _ := standbyFrame(at, false)
		frame := draw()
		if litPixels(frame) < 200 {
			t.Fatalf("step %d: the clock is nearly blank", i)
		}
		x0, y0, x1, y1 := litBox(frame)
		if x0 < walkMargin || y0 < walkMargin || x1 > oled.Width-1-walkMargin || y1 > oled.Height-1-walkBottom {
			t.Fatalf("step %d: lit box (%d,%d)-(%d,%d) leaves the walk's area", i, x0, y0, x1, y1)
		}
		if i > 0 && at.Minute() == at.Add(-walkStep).Minute() && (abs(x0-prevX) > 1 || abs(y0-prevY) > 1) {
			t.Fatalf("step %d: jumped from (%d,%d) to (%d,%d)", i, prevX, prevY, x0, y0)
		}
		prevX, prevY = x0, y0
		xs[x0], ys[y0] = true, true
	}
	if len(xs) < 10 || len(ys) < 20 {
		t.Errorf("the walk reached %d columns and %d rows", len(xs), len(ys))
	}
	again, _ := standbyFrame(base, false)
	if string(again()) != string(first) {
		t.Error("the same moment drew a different place: a restart would jump")
	}
}

// The standby screen is the time alone, as stock's: on a 12-hour clock AM/PM sits under
// it and nothing else is drawn.
func TestStandbyIsTheTimeAlone(t *testing.T) {
	at := time.Date(2026, 9, 25, 20, 42, 0, 0, time.UTC)
	draw24, key24 := standbyFrame(at, false)
	draw12, key12 := standbyFrame(at, true)
	_, y0, _, y1 := litBox(draw24())
	_, ty0, _, ty1 := litBox(draw12())
	if y1-y0 >= ty1-ty0 {
		t.Errorf("24-hour clock %d rows tall, 12-hour with PM %d: want PM on a line under it", y1-y0, ty1-ty0)
	}
	if !strings.Contains(key12, "PM") || strings.Contains(key24, "PM") {
		t.Errorf("keys: 24-hour %q, 12-hour %q", key24, key12)
	}
}

func TestBounce(t *testing.T) {
	var got []int
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

func litBox(frame []byte) (x0, y0, x1, y1 int) {
	x0, y0, x1, y1 = oled.Width, oled.Height, -1, -1
	for i, v := range frame {
		if v != 0 {
			x, y := i%oled.Width, i/oled.Width
			x0, x1, y0, y1 = min(x0, x), max(x1, x), min(y0, y), max(y1, y)
		}
	}
	return x0, y0, x1, y1
}

func abs(v int) int { return max(v, -v) }

func TestDimFrame(t *testing.T) {
	if got := dimFrame([]byte{204, 100}, 0); got[0] != 204 || got[1] != 100 {
		t.Fatalf("default changed the frame: %v", got)
	}
	if got := dimFrame([]byte{204, 100}, 25); got[0] != 63 || got[1] != 31 {
		t.Fatalf("25%%: %v", got)
	}
}
