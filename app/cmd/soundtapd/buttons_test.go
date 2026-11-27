// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/oplock"
)

// fakeActor records what the button layer asks the speaker to do.
type fakeActor struct {
	mu     sync.Mutex
	keys   []string
	stored []string
	// storable is what StoreCurrentAsPreset reports.
	storable bool
	storeErr error
}

func (f *fakeActor) ApplyKey(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.keys = append(f.keys, key)
}

func (f *fakeActor) StoreCurrentAsPreset(key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.stored = append(f.stored, key)

	return f.storable, f.storeErr
}

func (f *fakeActor) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	fired := 0

	for _, k := range f.keys {
		if k == key {
			fired++
		}
	}

	return fired
}

func (f *fakeActor) snapshot() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.keys...), append([]string(nil), f.stored...)
}

// newFakeActor is an actor that has applied nothing and reports storable for a store.
func newFakeActor(storable bool) *fakeActor {
	return &fakeActor{mu: sync.Mutex{}, keys: nil, stored: nil, storable: storable, storeErr: nil}
}

func testHandler() (*buttonHandler, *fakeActor) {
	a := newFakeActor(true)

	return newButtonHandler(a, slog.New(slog.DiscardHandler)), a
}

// A short press plays the preset; nothing is stored.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestPresetShortPressPlays(t *testing.T) {
	h, a := testHandler()
	h.Handle(6, true, "PRESET_1")
	h.Handle(6, false, "PRESET_1")

	keys, stored := a.snapshot()
	if len(keys) != 1 || keys[0] != "PRESET_1" {
		t.Fatalf("keys = %v, want one PRESET_1", keys)
	}

	if len(stored) != 0 {
		t.Fatalf("stored = %v, want none", stored)
	}

	if overlay, _ := frontPanel.Current(time.Now()); overlay == nil {
		t.Fatal("the panel does not show the preset's number")
	}
}

// Holding past the threshold stores while still held, and the release that ends
// the gesture must not then also play the preset.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestPresetHoldStoresAndSwallowsTheRelease(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		handler, actor := testHandler()
		handler.Handle(6, true, "PRESET_1")

		deadline := time.Now().Add(holdToStore + 2*time.Second)
		for time.Now().Before(deadline) {
			if _, stored := actor.snapshot(); len(stored) > 0 {
				break
			}

			time.Sleep(10 * time.Millisecond)
		}

		handler.Handle(6, false, "PRESET_1")

		keys, stored := actor.snapshot()
		if len(stored) != 1 || stored[0] != "PRESET_1" {
			t.Fatalf("stored = %v, want one PRESET_1", stored)
		}

		if len(keys) != 0 {
			t.Fatalf("keys = %v, want none: the release ends a store, it does not play", keys)
		}
	})
}

// Volume ramps while held — a single press per step would need twenty presses to
// cross the range, which is not how the speaker behaves.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestVolumeRepeatsWhileHeld(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		handler, actor := testHandler()
		handler.Handle(0, true, "VOLUME_UP")

		if got := actor.count("VOLUME_UP"); got != 1 {
			t.Fatalf("immediate steps = %d, want 1", got)
		}

		deadline := time.Now().Add(volumeRepeatDelay + 20*volumeRepeat + 2*time.Second)
		for time.Now().Before(deadline) && actor.count("VOLUME_UP") < 4 {
			time.Sleep(10 * time.Millisecond)
		}

		if got := actor.count("VOLUME_UP"); got < 4 {
			t.Fatalf("steps while held = %d, want the ramp to keep going", got)
		}

		handler.Handle(0, false, "VOLUME_UP")

		settled := actor.count("VOLUME_UP")

		time.Sleep(volumeRepeatDelay + 5*volumeRepeat)

		if got := actor.count("VOLUME_UP"); got != settled {
			t.Fatalf("steps kept coming after release: %d then %d", settled, got)
		}
	})
}

// A lost release edge must not leave a ramp running forever; reset() is what the
// read loop calls when the device disappears mid-gesture.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestResetStopsAnOrphanedRamp(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		handler, actor := testHandler()
		handler.Handle(0, true, "VOLUME_DOWN")
		handler.Reset()

		settled := actor.count("VOLUME_DOWN")

		time.Sleep(volumeRepeatDelay + 5*volumeRepeat)

		if got := actor.count("VOLUME_DOWN"); got != settled {
			t.Fatalf("ramp survived reset: %d then %d", settled, got)
		}

		if len(handler.repeats) != 0 || len(handler.holds) != 0 {
			t.Fatalf("reset left state behind: %d repeats, %d holds", len(handler.repeats), len(handler.holds))
		}
	})
}

// A press whose release never arrives stops once the ramp could have crossed the
// whole scale, not half a minute later.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestLostReleaseStopsAtFullScale(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		handler, actor := testHandler()
		handler.Handle(0, true, "VOLUME_UP")
		time.Sleep(volumeRampMax + volumeRepeat)

		settled := actor.count("VOLUME_UP")
		if settled < 100 {
			t.Fatalf("steps = %d, want a held key to reach the top of the scale", settled)
		}

		time.Sleep(5 * time.Second)

		if got := actor.count("VOLUME_UP"); got != settled {
			t.Fatalf("ramp kept going without a release: %d then %d", settled, got)
		}
	})
}

// A duplicate press (release edge lost on the wire) restarts the ramp instead of
// stacking a second one that nothing will ever stop.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestDuplicatePressDoesNotStackRamps(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		handler, actor := testHandler()
		handler.Handle(0, true, "VOLUME_UP")
		handler.Handle(0, true, "VOLUME_UP")
		handler.Handle(0, false, "VOLUME_UP")

		settled := actor.count("VOLUME_UP")

		time.Sleep(volumeRepeatDelay + 5*volumeRepeat)

		if got := actor.count("VOLUME_UP"); got != settled {
			t.Fatalf("a ramp outlived the release: %d then %d", settled, got)
		}
	})
}

// Power and source act on the press edge, and the release must not act again.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestEdgeKeysActOnceOnPress(t *testing.T) {
	for _, key := range []string{"POWER", "SOURCE_TOGGLE"} {
		h, a := testHandler()
		h.Handle(2, true, key)
		h.Handle(2, false, key)

		if keys, _ := a.snapshot(); len(keys) != 1 || keys[0] != key {
			t.Fatalf("%s: keys = %v, want exactly one", key, keys)
		}
	}
}

// firedChords records which chords fired. Chord actions run on a timer goroutine,
// so the recorder is guarded.
type firedChords struct {
	mu    sync.Mutex
	names []string
}

func (f *firedChords) record(name string) func() {
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		f.names = append(f.names, name)
	}
}

func (f *firedChords) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.names...)
}

func testChordHandler(fired *firedChords) (*buttonHandler, *fakeActor) {
	actor := newFakeActor(true)
	record := fired.record
	chords := []chord{
		{
			name: "factory-reset", keys: []string{"PRESET_1", "VOLUME_DOWN"},
			hold: 60 * time.Millisecond, action: record("factory-reset"),
		},
		{
			name: "setup-mode", keys: []string{"PRESET_2", "VOLUME_DOWN"},
			hold: 60 * time.Millisecond, action: record("setup-mode"),
		},
	}

	return newButtonHandler(actor, slog.New(slog.DiscardHandler), chords...), actor
}

// The panel counts the seconds down while a chord is held, and the count and the lamps stop
// when it fires, or when it is let go.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestChordCountsDownOnThePanel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords

		handler, _ := testChordHandler(&fired)
		handler.chords[0].hold = 3 * time.Second
		show := newFakeChordPanel()
		handler.chordShow = show
		handler.Handle(6, true, "PRESET_1")
		handler.Handle(4, true, "VOLUME_DOWN")

		if !chordCounting.Load() || show.word != "Factory reset" || show.hold != 3*time.Second {
			t.Fatalf("held chord: lamps %v, panel %q for %s", chordCounting.Load(), show.word, show.hold)
		}

		time.Sleep(4 * time.Second)
		synctest.Wait()

		if downs, ups := show.counts(); chordCounting.Load() || downs != 1 || ups != 1 {
			t.Fatalf("after firing: lamps %v, count started %d, ended %d", chordCounting.Load(), downs, ups)
		}

		handler.Handle(4, false, "VOLUME_DOWN")
		handler.Handle(6, false, "PRESET_1")
		handler.Handle(6, true, "PRESET_1")
		handler.Handle(4, true, "VOLUME_DOWN")
		handler.Handle(4, false, "VOLUME_DOWN")
		handler.Handle(6, false, "PRESET_1")
		time.Sleep(4 * time.Second)
		synctest.Wait()

		if downs, ups := show.counts(); chordCounting.Load() || downs != 2 || ups != 2 {
			t.Fatalf("a released chord kept counting: lamps %v, started %d, ended %d", chordCounting.Load(), downs, ups)
		}
	})
}

// Holding the stock factory-reset combo long enough fires it, and — critically —
// releasing out of it must not also store a preset, which the preset key's own hold
// timer would otherwise have done.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestChordFiresAndSuppressesTheKeyGestures(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords

		handler, actor := testChordHandler(&fired)
		handler.Handle(6, true, "PRESET_1")
		handler.Handle(4, true, "VOLUME_DOWN")
		time.Sleep(250 * time.Millisecond)
		handler.Handle(4, false, "VOLUME_DOWN")
		handler.Handle(6, false, "PRESET_1")

		if got := fired.list(); len(got) != 1 || got[0] != "factory-reset" {
			t.Fatalf("fired = %v, want one factory-reset", got)
		}

		keys, stored := actor.snapshot()
		if len(stored) != 0 {
			t.Fatalf("the chord also stored a preset: %v", stored)
		}

		for _, k := range keys {
			if k == "PRESET_1" {
				t.Fatalf("the chord also played a preset: %v", keys)
			}
		}
	})
}

// Letting go early must cancel it. A recovery gesture that fires on a brush of two
// keys is worse than one that is hard to reach.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestChordReleasedEarlyDoesNotFire(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords

		h, _ := testChordHandler(&fired)
		h.Handle(6, true, "PRESET_1")
		h.Handle(4, true, "VOLUME_DOWN")
		h.Handle(4, false, "VOLUME_DOWN")
		time.Sleep(250 * time.Millisecond)

		if got := fired.list(); len(got) != 0 {
			t.Fatalf("fired after an early release: %v", got)
		}
	})
}

// Aborting a chord must return its still-held key to normal handling. Otherwise
// that key remains inert until physically released and pressed again.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestChordReleasedEarlyResumesSurvivingPreset(t *testing.T) {
	var fired firedChords

	handler, actor := testChordHandler(&fired)
	handler.Handle(6, true, "PRESET_1")
	handler.Handle(4, true, "VOLUME_DOWN")
	handler.Handle(4, false, "VOLUME_DOWN")
	handler.Handle(6, false, "PRESET_1")

	keys, _ := actor.snapshot()
	if len(keys) != 1 || keys[0] != "PRESET_1" {
		t.Fatalf("keys = %v, want surviving PRESET_1 press", keys)
	}
}

// The two stock combos share volume-down, so the one that is fully held must win
// and the other must not arm behind it.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestOnlyTheHeldChordArms(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords

		h, _ := testChordHandler(&fired)
		h.Handle(5, true, "PRESET_2")
		h.Handle(4, true, "VOLUME_DOWN")
		time.Sleep(250 * time.Millisecond)

		if got := fired.list(); len(got) != 1 || got[0] != "setup-mode" {
			t.Fatalf("fired = %v, want one setup-mode", got)
		}
	})
}

// A device that disappears mid-chord must not fire it: we no longer know whether
// the keys are still down.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestResetCancelsAnArmedChord(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords

		handler, _ := testChordHandler(&fired)
		handler.Handle(6, true, "PRESET_1")
		handler.Handle(4, true, "VOLUME_DOWN")
		handler.Reset()
		time.Sleep(250 * time.Millisecond)

		if got := fired.list(); len(got) != 0 {
			t.Fatalf("a chord survived reset: %v", got)
		}

		if len(handler.down) != 0 {
			t.Fatalf("reset left %d keys marked as held", len(handler.down))
		}
	})
}

// A single preset press must still behave normally when chords are configured.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestChordsDoNotBreakOrdinaryPresses(t *testing.T) {
	var fired firedChords

	h, a := testChordHandler(&fired)
	h.Handle(6, true, "PRESET_1")
	h.Handle(6, false, "PRESET_1")

	keys, _ := a.snapshot()
	if len(keys) != 1 || keys[0] != "PRESET_1" {
		t.Fatalf("keys = %v, want one PRESET_1", keys)
	}

	if got := fired.list(); len(got) != 0 {
		t.Fatalf("a single press fired a chord: %v", got)
	}
}

// Holding a preset key past holdToStore fires the chime callback once.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestPresetStoreFiresChime(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		handler, actor := testHandler()
		actor.storable = true

		var chimes atomic.Int32

		handler.chime = func() { chimes.Add(1) }
		handler.Handle(6, true, "PRESET_1")

		deadline := time.Now().Add(holdToStore + 2*time.Second)
		for time.Now().Before(deadline) {
			if _, stored := actor.snapshot(); len(stored) > 0 {
				break
			}

			time.Sleep(10 * time.Millisecond)
		}

		handler.Handle(6, false, "PRESET_1")
		// Brief wait: the chime fires in the same AfterFunc goroutine as the store.
		time.Sleep(50 * time.Millisecond)

		if got := chimes.Load(); got != 1 {
			t.Fatalf("chimes = %d, want 1 after a successful store", got)
		}
	})
}

//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestPresetNonStoreDoesNotFireChime(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		handler, actor := testHandler()
		actor.storable = false // nothing is playing

		var chimes atomic.Int32

		handler.chime = func() { chimes.Add(1) }
		handler.Handle(6, true, "PRESET_1")

		deadline := time.Now().Add(holdToStore + 2*time.Second)
		for time.Now().Before(deadline) {
			if _, stored := actor.snapshot(); len(stored) > 0 {
				break
			}

			time.Sleep(10 * time.Millisecond)
		}

		handler.Handle(6, false, "PRESET_1")
		time.Sleep(50 * time.Millisecond)

		if got := chimes.Load(); got != 0 {
			t.Fatalf("chimes = %d, want 0 when nothing was stored", got)
		}
	})
}

// A short tap of SOURCE_TOGGLE still toggles the source when the hold selects Bluetooth.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestSourceToggleShortPressTogglesSource(t *testing.T) {
	h, a := testHandler()
	h.auxHoldBT = true
	h.Handle(12, true, "SOURCE_TOGGLE")
	h.Handle(12, false, "SOURCE_TOGGLE")

	keys, _ := a.snapshot()
	if len(keys) != 1 || keys[0] != "SOURCE_TOGGLE" {
		t.Fatalf("keys = %v, want one SOURCE_TOGGLE on short tap", keys)
	}
}

// Holding SOURCE_TOGGLE selects Bluetooth, as stock's press-and-hold does, and does not
// also toggle the source on release.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestSourceToggleHoldSelectsBluetooth(t *testing.T) {
	// A fake clock: the sleep below costs no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		handler, actor := testHandler()
		handler.auxHoldBT = true
		handler.Handle(12, true, "SOURCE_TOGGLE")
		time.Sleep(holdToStore + 100*time.Millisecond)
		handler.Handle(12, false, "SOURCE_TOGGLE")

		keys, _ := actor.snapshot()
		if len(keys) != 1 || keys[0] != sourceBluetooth {
			t.Fatalf("keys = %v, want one %s after a hold", keys, sourceBluetooth)
		}
	})
}

// Without Bluetooth, SOURCE_TOGGLE uses the simple press behaviour.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestSourceToggleWithoutBluetoothStillWorks(t *testing.T) {
	h, a := testHandler()
	h.Handle(12, true, "SOURCE_TOGGLE")
	h.Handle(12, false, "SOURCE_TOGGLE")

	keys, _ := a.snapshot()
	if len(keys) != 1 || keys[0] != "SOURCE_TOGGLE" {
		t.Fatalf("keys = %v, want one SOURCE_TOGGLE without Bluetooth", keys)
	}
}

// TestLostReleaseDoesNotLeakHoldTimer covers a duplicate press on a polled keypad,
// where the release edge between the two was missed. The first timer must not stay
// armed: for SOURCE_TOGGLE it would switch to Bluetooth on its own, seconds after the
// user let go.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestLostReleaseDoesNotLeakHoldTimer(t *testing.T) {
	handler, actor := testHandler()
	handler.auxHoldBT = true

	// Two presses with no release in between, then one release: exactly one timer may
	// be armed, and the release must leave nothing behind that can still fire.
	handler.Handle(3, true, "SOURCE_TOGGLE")
	handler.Handle(3, true, "SOURCE_TOGGLE")

	if n := len(handler.holds); n != 1 {
		t.Fatalf("holds = %d after a duplicate press, want 1", n)
	}

	handler.Handle(3, false, "SOURCE_TOGGLE")

	if n := len(handler.holds); n != 0 {
		t.Fatalf("holds = %d after release, want 0", n)
	}

	if keys, _ := actor.snapshot(); len(keys) != 1 || keys[0] != "SOURCE_TOGGLE" {
		t.Errorf("keys = %v, want one SOURCE_TOGGLE toggle", keys)
	}

	// The same for a preset: a leaked timer would store a slot nobody held.
	handler.Handle(1, true, "PRESET_1")
	handler.Handle(1, true, "PRESET_1")

	if n := len(handler.holds); n != 1 {
		t.Fatalf("preset holds = %d after a duplicate press, want 1", n)
	}

	handler.Handle(1, false, "PRESET_1")

	if n := len(handler.holds); n != 0 {
		t.Fatalf("preset holds = %d after release, want 0", n)
	}

	if _, stored := actor.snapshot(); len(stored) != 0 {
		t.Errorf("stored = %v on a short tap, want none", stored)
	}
}

// An idle device must not keep its reader alive past cancellation, and a key held
// at that moment must not leave its hold timer running.
//
// Linux only: the speakers' key devices and the FIFO standing in for them are pollable
// there, so Close unblocks the read. Go on macOS does not poll FIFOs (golang/go#24164)
// and a blocked FIFO read survives Close; no speaker runs that.
func TestKeyDeviceReaderStopsOnCancel(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("FIFO reads are only interruptible by Close where Go polls FIFOs (Linux)")
	}

	fifo := filepath.Join(t.TempDir(), "keypad")

	err := syscall.Mkfifo(fifo, 0o600)
	if err != nil {
		t.Skip("no FIFO support:", err)
	}

	writer, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	handler := newButtonHandler(newFakeActor(false), slog.New(slog.DiscardHandler))
	decode := func(record []byte) (int, bool, string, bool) { return 1, record[0] == 'P', "PRESET_1", true }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		hw.ReadKeyDevice(ctx, fifo, 1, decode, handler)
		close(done)
	}()

	_, err = writer.WriteString("P")
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reader still blocked after cancellation")
	}

	if len(handler.holds) != 0 || len(handler.down) != 0 {
		t.Fatalf("gesture survived cancellation: holds=%d down=%d", len(handler.holds), len(handler.down))
	}
}

// fakeChordPanel records what the panel was told about a chord.
type fakeChordPanel struct {
	mu    sync.Mutex
	word  string
	hold  time.Duration
	downs int
	ups   int
}

func (f *fakeChordPanel) ChordDown(word string, hold time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.word, f.hold = word, hold
	f.downs++
}

func (f *fakeChordPanel) ChordUp() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ups++
}

func newFakeChordPanel() *fakeChordPanel {
	return &fakeChordPanel{mu: sync.Mutex{}, word: "", hold: 0, downs: 0, ups: 0}
}

// counts are the times the count started and ended.
func (f *fakeChordPanel) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.downs, f.ups
}

// Each recovery combo names itself on the panel, with its hold to count down: the setup
// combo showed nothing on the ST20 (2026-11-27), and the owner let go at 7 and 9 s.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestChordCountNamesTheCombo(t *testing.T) {
	for _, testCase := range []struct{ preset, want string }{
		{"PRESET_1", "Factory reset"}, {"PRESET_2", "Setup mode"},
	} {
		nop := func() {}
		handler := newButtonHandler(newFakeActor(false), slog.New(slog.DiscardHandler), recoveryChords(nop, nop)...)
		show := newFakeChordPanel()
		handler.chordShow = show
		handler.Handle(1, true, "VOLUME_DOWN")
		handler.Handle(2, true, testCase.preset)

		if show.word != testCase.want || show.hold != chordHold || show.downs != 1 {
			t.Errorf("%s + VOLUME_DOWN: panel = %q for %s (%d), want %q for %s",
				testCase.preset, show.word, show.hold, show.downs, testCase.want, chordHold)
		}

		handler.Handle(2, false, testCase.preset) // let go early: the count goes

		if _, ups := show.counts(); ups != 1 {
			t.Errorf("%s released early: %d ends of the count, want 1", testCase.preset, ups)
		}
	}
}

// A chord held its whole time ends the count before its action runs (which shows its own
// result), so nothing stays on the panel.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestChordCountEndsWhenItFires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		show := newFakeChordPanel()

		var ups atomic.Int64

		ups.Store(-1)

		handler := newButtonHandler(newFakeActor(false), slog.New(slog.DiscardHandler),
			chord{name: "setup-mode", keys: []string{"PRESET_2", keyVolumeDown}, hold: chordHold, action: func() {
				_, n := show.counts()
				ups.Store(int64(n))
			}})
		handler.chordShow = show
		handler.Handle(1, true, "VOLUME_DOWN")
		handler.Handle(2, true, "PRESET_2")
		time.Sleep(chordHold + time.Second)

		if downs, _ := show.counts(); downs != 1 || ups.Load() != 1 {
			t.Fatalf("count started %d times, ended %d times before the action; want 1, 1", downs, ups.Load())
		}
	})
}

// A combo held for seconds and let go early was meant: the key still held must not
// then play its preset on release (Preset 6 + Volume down, let go at 8 s, played 6).
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestChordHeldThenReleasedEarlySwallowsTheSurvivor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords

		handler, actor := testChordHandler(&fired)
		handler.chords[0].hold = time.Minute
		handler.Handle(6, true, "PRESET_1")
		handler.Handle(4, true, "VOLUME_DOWN")
		time.Sleep(chordMeant + time.Second)
		handler.Handle(4, false, "VOLUME_DOWN")
		handler.Handle(6, false, "PRESET_1")
		time.Sleep(holdToStore + time.Second)

		if keys, stored := actor.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys = %v, stored = %v, want none", keys, stored)
		}

		if got := fired.list(); len(got) != 0 {
			t.Fatalf("fired = %v", got)
		}
	})
}

// While an install writes NAND only the volume keys act.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestKeysLockedDuringInstall(t *testing.T) {
	firmwareActive.Store(true)
	t.Cleanup(func() { firmwareActive.Store(false) })

	handler, actor := testHandler()

	var shown string

	handler.feedback = func(s string) { shown = s }
	for _, key := range []string{"PRESET_1", "POWER", "SOURCE_TOGGLE", "BLUETOOTH"} {
		handler.Handle(0, true, key)
		handler.Handle(0, false, key)
	}

	handler.Handle(4, true, "VOLUME_DOWN")
	handler.Handle(4, false, "VOLUME_DOWN")

	keys, _ := actor.snapshot()
	if strings.Join(keys, ",") != "VOLUME_DOWN" || shown != "Updating" {
		t.Fatalf("keys = %v, shown %q; want only VOLUME_DOWN and Updating", keys, shown)
	}
}

// POWER acts on the press, and a long hold adds nothing: low-power standby is not
// supported, so holding POWER must not turn into a second toggle.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestPowerHoldIsOnePress(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		h, a := testHandler()
		h.Handle(2, true, "POWER")
		time.Sleep(chordHold + time.Second)
		h.Handle(2, false, "POWER")

		keys, _ := a.snapshot()
		if len(keys) != 1 || keys[0] != "POWER" {
			t.Fatalf("keys = %v, want one POWER for a long hold", keys)
		}
	})
}

// The Wireless Link's Bluetooth button is KEY_BLUETOOTH. A tap selects Bluetooth;
// a press-and-hold selects it and opens the pairing window, once.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestBluetoothButton(t *testing.T) {
	ev := make([]byte, 16)
	binary.LittleEndian.PutUint16(ev[8:], 1)
	binary.LittleEndian.PutUint16(ev[10:], 237)
	binary.LittleEndian.PutUint32(ev[12:], 1)

	if code, pressed, action, ok := hw.InputEvent(ev); !ok || !pressed || action != "BLUETOOTH" {
		t.Fatalf("KEY_BLUETOOTH decodes to %d %v %v", code, pressed, ok)
	}

	synctest.Test(t, func(t *testing.T) {
		handler, actor := testHandler()

		var pairs atomic.Int32

		handler.btPair = func() { pairs.Add(1) }
		handler.Handle(0, true, "BLUETOOTH")
		handler.Handle(0, false, "BLUETOOTH")
		handler.Handle(0, true, "BLUETOOTH")
		time.Sleep(holdToStore + 100*time.Millisecond)
		handler.Handle(0, false, "BLUETOOTH")

		keys, _ := actor.snapshot()
		if strings.Join(keys, ",") != "BLUETOOTH,BLUETOOTH" || pairs.Load() != 1 {
			t.Fatalf("keys = %v, pairing windows = %d; want two BLUETOOTH and one window", keys, pairs.Load())
		}
	})
}

// The Wireless Link reaches setup with its two keys, and neither key does its own job.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestTwoKeyChordOpensSetupWithoutSourceChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords

		chords := recoveryChords(fired.record("reset"), fired.record("setup"))
		actor := newFakeActor(true)
		handler := newButtonHandler(actor, slog.New(slog.DiscardHandler), chords...)
		handler.auxHoldBT = true
		handler.btPair = func() { t.Error("the chord started pairing") }
		handler.Handle(0, true, keySourceToggle)
		handler.Handle(1, true, sourceBluetooth)
		time.Sleep(chordHold + time.Second)
		synctest.Wait()
		handler.Handle(0, false, keySourceToggle)
		handler.Handle(1, false, sourceBluetooth)
		synctest.Wait()

		if got := fired.list(); !slices.Equal(got, []string{"setup"}) {
			t.Fatalf("fired %v, want setup only", got)
		}

		actor.mu.Lock()
		defer actor.mu.Unlock()

		if len(actor.keys) != 0 {
			t.Fatalf("keys applied: %v", actor.keys)
		}
	})
}

// A chord takes the operations lock when it fires: a BCO flash that holds it, or a
// Core install that took it while the chord was counting down, refuses the reset.
//
//nolint:paralleltest // changes package-wide state other tests read: chordCounting, confirmPending, confirmer and more
func TestChordRefusedWhileAnOperationHoldsTheLock(t *testing.T) {
	oplock.Path = filepath.Join(t.TempDir(), "operation.lock")
	t.Cleanup(func() { oplock.Path = "" })

	for _, testCase := range []struct {
		name       string
		lockBefore bool
	}{{"BCO flash first", true}, {"install after the chord armed", false}} {
		t.Run(testCase.name, func(t *testing.T) {
			var (
				install, chordLock oplock.Lock
				resets, refusals   atomic.Int32
			)

			handler := newButtonHandler(newFakeActor(false), slog.New(slog.DiscardHandler), chord{
				name: "factory-reset", keys: []string{"PRESET_1", "VOLUME_DOWN"}, hold: 500 * time.Millisecond,
				action: exclusiveKept(&chordLock, func() { refusals.Add(1) }, func() { resets.Add(1) }),
			})

			if testCase.lockBefore && !install.TryLock() {
				t.Fatal("flash could not take the lock")
			}

			handler.Handle(6, true, "PRESET_1")
			handler.Handle(4, true, "VOLUME_DOWN")

			if !testCase.lockBefore && !install.TryLock() {
				t.Fatal("install could not take the lock")
			}

			deadline := time.Now().Add(5 * time.Second)
			for refusals.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			install.Unlock()

			if resets.Load() != 0 || refusals.Load() != 1 {
				t.Fatalf("resets %d, refusals %d; want 0 and 1", resets.Load(), refusals.Load())
			}
		})
	}
	// With nothing else running, the reset runs and keeps the lock for the shutdown.
	var chordLock, install oplock.Lock
	ran := false

	exclusiveKept(&chordLock, func() {}, func() { ran = true })()

	if !ran || install.TryLock() {
		t.Fatalf("reset ran %v; the lock was free after it: want it held", ran)
	}
}
