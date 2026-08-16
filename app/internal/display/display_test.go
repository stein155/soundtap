// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package display

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/oled"
)

func newTestManager(t *testing.T, standby bool) (*Manager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fb0")
	if err := os.WriteFile(path, bytes.Repeat([]byte{7}, oled.Width*oled.Height), 0o600); err != nil {
		t.Fatal(err)
	}
	// No New: its background loop steps on its own every second, racing the steps the
	// tests take and writing into the temp dir while it is removed.
	m := &Manager{fb: oled.NewFramebuffer(path), avail: true,
		standby: func(context.Context) bool { return standby },
		log:     slog.New(slog.NewTextHandler(os.Stderr, nil))}
	return m, path
}

// In standby with only the clock to show, the panel is looked at once a second;
// a transient or waking brings it back to paintTick.
func TestStandbyTickAndReturnToFast(t *testing.T) {
	standby := true
	m, _ := newTestManager(t, true)
	m.standby = func(context.Context) bool { return standby }
	ctx := context.Background()
	if got := m.tickEvery(ctx); got != time.Second {
		t.Fatalf("standby clock: tick %v, want 1s", got)
	}
	m.SetTransient(Content{Frame: bytes.Repeat([]byte{5}, oled.Width*oled.Height)}, time.Minute)
	if got := m.tickEvery(ctx); got != paintTick {
		t.Fatalf("transient in standby: tick %v, want %v", got, paintTick)
	}
	m.transientFrame = nil
	standby = false
	if got := m.tickEvery(ctx); got != paintTick {
		t.Fatalf("awake: tick %v, want %v", got, paintTick)
	}
}

// At the slow standby tick a new transient (a key press) is painted at once, not up to a
// second later.
func TestNudgePaintsAtOnceInStandby(t *testing.T) {
	m, path := newTestManager(t, true)
	m.wake = make(chan struct{}, 1)
	m.SetStandby(Content{Frame: bytes.Repeat([]byte{9}, oled.Width*oled.Height)})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.loop(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	// The tick that paints the clock drops the loop to the 1 s tick; the next one is due
	// a second after that paint, so a paint within half of it can only come from the nudge.
	waitPainted(t, path, m.standbyFrame, 10*time.Second)
	transient := bytes.Repeat([]byte{4}, oled.Width*oled.Height)
	m.SetTransient(Content{Frame: transient}, time.Minute)
	waitPainted(t, path, transient, standbyPaintTick/2)
}

func waitPainted(t *testing.T, path string, want []byte, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if got, _ := os.ReadFile(path); bytes.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("frame not painted within %v", within)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestUnavailableIsNoop(t *testing.T) {
	m := New(context.Background(), filepath.Join(t.TempDir(), "missing"), nil,
		slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if m.Available() {
		t.Fatal("missing fb reported available")
	}
	m.SetStandby(Content{})
	m.SetNowPlaying(Content{})
	m.ClearNowPlaying()
}

func TestTransientOutranksAndRestoresNativeScreen(t *testing.T) {
	m, path := newTestManager(t, false)
	m.SetNowPlaying(Content{Frame: bytes.Repeat([]byte{9}, oled.Width*oled.Height)})
	m.SetTransient(Content{Frame: bytes.Repeat([]byte{11}, oled.Width*oled.Height)}, time.Hour)
	m.step(context.Background())
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, m.transientFrame) {
		t.Fatal("transient not drawn over now-playing")
	}
	m.mu.Lock()
	m.transientUntil = time.Now().Add(-time.Second)
	m.mu.Unlock()
	m.step(context.Background())
	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, m.npFrame) {
		t.Fatal("now-playing not restored after transient")
	}
}

func TestNativeScreens(t *testing.T) {
	blank := bytes.Repeat([]byte{7}, oled.Width*oled.Height)
	m, path := newTestManager(t, true)
	m.SetStandby(Content{Frame: bytes.Repeat([]byte{9}, oled.Width*oled.Height)})
	m.SetNowPlaying(Content{Frame: bytes.Repeat([]byte{5}, oled.Width*oled.Height)})
	m.step(context.Background())
	got, _ := os.ReadFile(path)
	if bytes.Equal(got, blank) || !bytes.Equal(got, m.standbyFrame) {
		t.Fatal("standby clock not drawn")
	}
	m.standby = func(context.Context) bool { return false }
	m.step(context.Background())
	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, m.npFrame) {
		t.Fatal("now-playing not drawn")
	}
	m.ClearNowPlaying()
	m.step(context.Background())
	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, blank) {
		t.Fatal("panel not restored after now-playing cleared")
	}
}

// An unchanged standby frame is not written again every second: on the ST20 each write
// is an SPI transfer, all night long. After repaintEvery it is, once.
func TestUnchangedFrameRepaintsOnlyAfterRepaintEvery(t *testing.T) {
	m, path := newTestManager(t, true)
	m.SetStandby(Content{Frame: bytes.Repeat([]byte{9}, oled.Width*oled.Height)})
	m.step(context.Background())
	scribble := bytes.Repeat([]byte{3}, oled.Width*oled.Height)
	if err := os.WriteFile(path, scribble, 0o600); err != nil {
		t.Fatal(err)
	}
	m.drawnAt = time.Now().Add(-2 * time.Second)
	m.step(context.Background())
	if got, _ := os.ReadFile(path); !bytes.Equal(got, scribble) {
		t.Fatal("unchanged frame written again after 2 s")
	}
	m.drawnAt = time.Now().Add(-repaintEvery)
	m.step(context.Background())
	if got, _ := os.ReadFile(path); !bytes.Equal(got, m.standbyFrame) {
		t.Fatal("unchanged frame not written again after repaintEvery")
	}
}
