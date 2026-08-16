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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/clocksync"
	"github.com/stein155/soundtap/app/internal/display"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/weather"
	"github.com/stein155/soundtap/app/oled"
	"github.com/stein155/soundtap/app/oled/screen"
)

type blockingWeather struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*blockingWeather) Configure(float64, float64) {}

func (w *blockingWeather) Fetch(ctx context.Context) (*weather.Current, error) {
	w.once.Do(func() { close(w.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.release:
		return &weather.Current{TempC: 12, Code: 3}, nil
	}
}

func TestClockWeatherRefreshIsLoopOwned(t *testing.T) {
	oldTick := clockScreenTick
	clockScreenTick = time.Millisecond
	defer func() { clockScreenTick = oldTick }()
	screen.Width("j800-11", "x") // the font atlas loads once, slowly under -race: not inside the timed stop
	dir := t.TempDir()
	fb := filepath.Join(dir, "fb")
	if err := os.WriteFile(fb, make([]byte, oled.Width*oled.Height), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	disp := display.New(ctx, fb, func(context.Context) bool { return true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	wx := &blockingWeather{started: make(chan struct{}), release: make(chan struct{})}
	configCalls := 0
	done := make(chan struct{})
	go func() {
		RunClockScreen(ctx, disp, wx, func() settings.Display {
			configCalls++
			return settings.Display{City: "X", Timezone: "UTC", WeatherOn: true, Lat: float64(configCalls%2 + 1), Lon: 1}
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	<-wx.started
	close(wx.release)
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("clock screen did not stop")
	}
}

func TestClockRejectsWeatherFromPreviousLocation(t *testing.T) {
	old := settings.Display{City: "X", Timezone: "UTC", WeatherOn: true, Lat: 1, Lon: 1}
	current := settings.Display{City: "X", Timezone: "UTC", WeatherOn: true, Lat: 2, Lon: 2}
	if cur, accepted, err := acceptWeatherRefresh(weatherRefresh{
		cfg: old, cur: &weather.Current{TempC: 12},
	}, current); accepted || cur != nil || err != nil {
		t.Fatalf("stale refresh accepted: cur=%v err=%v", cur, err)
	}
}

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
	go RunClockScreen(ctx, disp, nil, func() settings.Display {
		return settings.Display{City: "X", Timezone: "UTC", ClockOff: off.Load()}
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

type flakyWeather struct{ calls atomic.Int32 }

func (*flakyWeather) Configure(float64, float64) {}

func (w *flakyWeather) Fetch(context.Context) (*weather.Current, error) {
	switch w.calls.Add(1) {
	case 1:
		return nil, context.DeadlineExceeded // no route yet
	case 2:
		time.Sleep(20 * time.Millisecond) // slower than weatherRetry: no second fetch behind it
	}
	return &weather.Current{TempC: 12, Code: 3}, nil
}

// A failed first fetch is retried soon, not after the full refresh interval.
func TestClockRetriesWeatherWithoutAReading(t *testing.T) {
	oldTick, oldEvery, oldRetry := clockScreenTick, weatherEvery, weatherRetry
	clockScreenTick, weatherEvery, weatherRetry = time.Millisecond, time.Hour, 5*time.Millisecond
	defer func() { clockScreenTick, weatherEvery, weatherRetry = oldTick, oldEvery, oldRetry }()
	fb := filepath.Join(t.TempDir(), "fb")
	if err := os.WriteFile(fb, make([]byte, oled.Width*oled.Height), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	disp := display.New(ctx, fb, func(context.Context) bool { return true }, quiet)
	wx := &flakyWeather{}
	done := make(chan struct{})
	go func() {
		RunClockScreen(ctx, disp, wx, func() settings.Display {
			return settings.Display{City: "X", Timezone: "UTC", WeatherOn: true, Lat: 1, Lon: 1}
		}, quiet)
		close(done)
	}()
	// The loop reads the interval variables: it must be gone before they are restored.
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(2 * time.Second)
	for wx.calls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("weather fetched %d times in 2 s, want a retry after the failure", wx.calls.Load())
		}
		time.Sleep(time.Millisecond)
	}
	// With a reading in hand it waits the full interval again.
	time.Sleep(50 * time.Millisecond)
	if n := wx.calls.Load(); n != 2 {
		t.Fatalf("weather fetched %d times, want 2 once a reading is in", n)
	}
}

// The standby clock walks as stock's does: one pixel diagonally per step, bouncing off
// the edges, never onto the panel's outer pixels or its bottom two rows, and over a full
// round every row and column of the free area is reached.
func TestStandbyClockWalks(t *testing.T) {
	base := time.Date(2026, 9, 25, 23, 40, 0, 0, time.UTC)
	firstDraw, _ := standbyFrame(base, false, &weather.Current{TempC: -12})
	first := firstDraw()
	var prevX, prevY int
	xs, ys := map[int]bool{}, map[int]bool{}
	for i := range 400 {
		at := base.Add(time.Duration(i) * walkStep)
		draw, _ := standbyFrame(at, false, &weather.Current{TempC: -12})
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
	again, _ := standbyFrame(base, false, &weather.Current{TempC: -12})
	if string(again()) != string(first) {
		t.Error("the same moment drew a different place: a restart would jump")
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
