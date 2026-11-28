// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package panel

import (
	"bytes"
	"context"
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
	"github.com/stein155/soundtap/app/oled/screen"
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

	// Like stock, no clock before the first NTP sync of the boot.
	synced := filepath.Join(t.TempDir(), "clock-synced")
	old := clocksync.Path
	clocksync.Path = synced

	// Not t.Context(): that ends only after the test, while the temp dirs are already
	// being removed. Cleanups run last-registered first, so this one, registered after
	// both TempDir calls, stops the clock loop and the display's paint loop (which writes
	// the framebuffer back as it stops) before the files go, and before clocksync.Path,
	// which the clock loop reads, is put back.
	ctx, cancel := context.WithCancel(context.Background())
	disp := display.New(ctx, fbPath, func(context.Context) bool { return true }, slog.New(slog.DiscardHandler))
	clockDone := make(chan struct{})

	t.Cleanup(func() {
		cancel()
		<-clockDone
		<-disp.Done()

		clocksync.Path = old
	})

	var off atomic.Bool

	go func() {
		defer close(clockDone)

		clockScreenEvery(ctx, disp, func() settings.Display {
			return settings.Display{Timezone: "UTC", ClockOff: off.Load(), Hour12: false}
		}, slog.New(slog.DiscardHandler), time.Millisecond)
	}()

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

// The standby clock moves to the next place of driftPath every minute, never more than 4 px
// from the middle, and the same moment always lands on the same place.
func TestStandbyClockDrifts(t *testing.T) {
	t.Parallel()

	for _, at := range driftPath {
		if abs(at[0]) > 4 || abs(at[1]) > 4 {
			t.Errorf("drift %v is more than 4 px from the middle", at)
		}
	}

	base := time.Date(2026, 9, 25, 23, 40, 0, 0, time.UTC)
	places := map[[2]int]bool{}

	for minute := range driftPath {
		when := base.Add(time.Duration(minute)*time.Minute + 30*time.Second)
		draw, key := standbyFrame(when, false)
		driftX, driftY := driftAt(int(when.Unix() / 60))

		if !bytes.Equal(draw(), screen.Standby(when.Format("15:04"), "", driftX, driftY).Pix(fullLevel)) {
			t.Errorf("minute %d: the clock is not at drift (%d, %d)", minute, driftX, driftY)
		}

		if again, againKey := standbyFrame(when.Add(-20*time.Second), false); againKey != key ||
			!bytes.Equal(again(), draw()) {
			t.Errorf("minute %d: the same minute drew a different place: a restart would jump", minute)
		}

		places[[2]int{driftX, driftY}] = true
	}

	if len(places) != len(driftPath) {
		t.Errorf("the clock stood in %d places over %d minutes", len(places), len(driftPath))
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
