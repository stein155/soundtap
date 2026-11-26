// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package display

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/oled"
)

func newTestManager(t *testing.T, standby bool) (*Manager, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fb0")

	err := os.WriteFile(path, bytes.Repeat([]byte{7}, oled.Width*oled.Height), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// No New: its background loop steps on its own every second, racing the steps the
	// tests take and writing into the temp dir while it is removed.
	mgr := &Manager{
		fb: oled.NewFramebuffer(path), avail: true,
		standby: func(context.Context) bool { return standby },
		log:     slog.New(slog.NewTextHandler(os.Stderr, nil)),

		mu:             sync.Mutex{},
		standbyFrame:   nil,
		npFrame:        nil,
		overlayFrame:   nil,
		transientFrame: nil,
		transientUntil: time.Time{},
		shown:          nil,
		drawnAt:        time.Time{},
		wake:           nil,
	}

	return mgr, path
}

// In standby with only the clock to show, the panel is looked at once a second;
// a transient or waking brings it back to paintTick.
func TestStandbyTickAndReturnToFast(t *testing.T) {
	t.Parallel()

	standby := true
	mgr, _ := newTestManager(t, true)
	mgr.standby = func(context.Context) bool { return standby }

	ctx := context.Background()
	if got := mgr.tickEvery(ctx); got != time.Second {
		t.Fatalf("standby clock: tick %v, want 1s", got)
	}

	mgr.SetTransient(Content{Frame: bytes.Repeat([]byte{5}, oled.Width*oled.Height)}, time.Minute)

	if got := mgr.tickEvery(ctx); got != paintTick {
		t.Fatalf("transient in standby: tick %v, want %v", got, paintTick)
	}

	mgr.transientFrame = nil
	standby = false

	if got := mgr.tickEvery(ctx); got != paintTick {
		t.Fatalf("awake: tick %v, want %v", got, paintTick)
	}
}

// At the slow standby tick a new transient (a key press) is painted at once, not up to a
// second later.
func TestNudgePaintsAtOnceInStandby(t *testing.T) {
	t.Parallel()

	mgr, path := newTestManager(t, true)
	mgr.wake = make(chan struct{}, 1)
	mgr.SetStandby(Content{Frame: bytes.Repeat([]byte{9}, oled.Width*oled.Height)})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() { mgr.loop(ctx); close(done) }()

	defer func() { cancel(); <-done }()
	// The tick that paints the clock drops the loop to the 1 s tick; the next one is due
	// a second after that paint, so a paint within half of it can only come from the nudge.
	waitPainted(t, path, mgr.standbyFrame, 10*time.Second)

	transient := bytes.Repeat([]byte{4}, oled.Width*oled.Height)
	mgr.SetTransient(Content{Frame: transient}, time.Minute)
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
	t.Parallel()

	mgr := New(context.Background(), filepath.Join(t.TempDir(), "missing"), nil,
		slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if mgr.Available() {
		t.Fatal("missing fb reported available")
	}

	mgr.SetStandby(Content{Frame: nil})
	mgr.SetNowPlaying(Content{Frame: nil})
	mgr.ClearNowPlaying()
}

func TestTransientOutranksAndRestoresNativeScreen(t *testing.T) {
	t.Parallel()

	mgr, path := newTestManager(t, false)
	mgr.SetNowPlaying(Content{Frame: bytes.Repeat([]byte{9}, oled.Width*oled.Height)})
	mgr.SetTransient(Content{Frame: bytes.Repeat([]byte{11}, oled.Width*oled.Height)}, time.Hour)
	mgr.step(context.Background())

	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, mgr.transientFrame) {
		t.Fatal("transient not drawn over now-playing")
	}

	mgr.mu.Lock()
	mgr.transientUntil = time.Now().Add(-time.Second)
	mgr.mu.Unlock()
	mgr.step(context.Background())

	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, mgr.npFrame) {
		t.Fatal("now-playing not restored after transient")
	}
}

func TestNativeScreens(t *testing.T) {
	t.Parallel()

	blank := bytes.Repeat([]byte{7}, oled.Width*oled.Height)
	mgr, path := newTestManager(t, true)
	mgr.SetStandby(Content{Frame: bytes.Repeat([]byte{9}, oled.Width*oled.Height)})
	mgr.SetNowPlaying(Content{Frame: bytes.Repeat([]byte{5}, oled.Width*oled.Height)})
	mgr.step(context.Background())

	got, _ := os.ReadFile(path)
	if bytes.Equal(got, blank) || !bytes.Equal(got, mgr.standbyFrame) {
		t.Fatal("standby clock not drawn")
	}

	mgr.standby = func(context.Context) bool { return false }
	mgr.step(context.Background())

	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, mgr.npFrame) {
		t.Fatal("now-playing not drawn")
	}

	mgr.ClearNowPlaying()
	mgr.step(context.Background())

	got, _ = os.ReadFile(path)
	if !bytes.Equal(got, make([]byte, oled.Width*oled.Height)) {
		t.Fatal("panel not dark after now-playing cleared")
	}
}

// An unchanged standby frame is not written again every second: on the ST20 each write
// is an SPI transfer, all night long. After repaintEvery it is, once.
func TestUnchangedFrameRepaintsOnlyAfterRepaintEvery(t *testing.T) {
	t.Parallel()

	mgr, path := newTestManager(t, true)
	mgr.SetStandby(Content{Frame: bytes.Repeat([]byte{9}, oled.Width*oled.Height)})
	mgr.step(context.Background())

	scribble := bytes.Repeat([]byte{3}, oled.Width*oled.Height)

	err := os.WriteFile(path, scribble, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	mgr.drawnAt = time.Now().Add(-2 * time.Second)
	mgr.step(context.Background())

	if got, _ := os.ReadFile(path); !bytes.Equal(got, scribble) {
		t.Fatal("unchanged frame written again after 2 s")
	}

	mgr.drawnAt = time.Now().Add(-repaintEvery)
	mgr.step(context.Background())

	if got, _ := os.ReadFile(path); !bytes.Equal(got, mgr.standbyFrame) {
		t.Fatal("unchanged frame not written again after repaintEvery")
	}
}

// With the standby clock off nothing is wanted in standby: the panel goes dark rather
// than back to a frame of ours from before, such as the boot screen's "Connecting…".
func TestStandbyWithoutClockGoesDark(t *testing.T) {
	t.Parallel()

	mgr, path := newTestManager(t, true)
	connecting := bytes.Repeat([]byte{3}, oled.Width*oled.Height)
	mgr.SetOverlay(Content{Frame: connecting})
	mgr.step(context.Background())
	mgr.ClearOverlay()
	mgr.SetNowPlaying(Content{Frame: bytes.Repeat([]byte{5}, oled.Width*oled.Height)})
	mgr.standby = func(context.Context) bool { return false }
	mgr.step(context.Background())

	mgr.standby = func(context.Context) bool { return true }
	mgr.step(context.Background())

	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, make([]byte, oled.Width*oled.Height)) {
		t.Fatalf("standby without a clock left %v on the panel, want it dark", got[:4])
	}
}
