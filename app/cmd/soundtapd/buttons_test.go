// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"context"
	"encoding/binary"
	"io"
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
	n := 0
	for _, k := range f.keys {
		if k == key {
			n++
		}
	}
	return n
}

func (f *fakeActor) snapshot() ([]string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.keys...), append([]string(nil), f.stored...)
}

func testHandler() (*buttonHandler, *fakeActor) {
	a := &fakeActor{storable: true}
	return newButtonHandler(a, slog.New(slog.NewTextHandler(io.Discard, nil))), a
}

// A short press plays the preset; nothing is stored.
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
func TestPresetHoldStoresAndSwallowsTheRelease(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		h, a := testHandler()
		h.Handle(6, true, "PRESET_1")
		deadline := time.Now().Add(holdToStore + 2*time.Second)
		for time.Now().Before(deadline) {
			if _, stored := a.snapshot(); len(stored) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		h.Handle(6, false, "PRESET_1")
		keys, stored := a.snapshot()
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
func TestVolumeRepeatsWhileHeld(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		h, a := testHandler()
		h.Handle(0, true, "VOLUME_UP")
		if got := a.count("VOLUME_UP"); got != 1 {
			t.Fatalf("immediate steps = %d, want 1", got)
		}
		deadline := time.Now().Add(volumeRepeatDelay + 20*volumeRepeat + 2*time.Second)
		for time.Now().Before(deadline) && a.count("VOLUME_UP") < 4 {
			time.Sleep(10 * time.Millisecond)
		}
		if got := a.count("VOLUME_UP"); got < 4 {
			t.Fatalf("steps while held = %d, want the ramp to keep going", got)
		}
		h.Handle(0, false, "VOLUME_UP")
		settled := a.count("VOLUME_UP")
		time.Sleep(volumeRepeatDelay + 5*volumeRepeat)
		if got := a.count("VOLUME_UP"); got != settled {
			t.Fatalf("steps kept coming after release: %d then %d", settled, got)
		}
	})
}

// A lost release edge must not leave a ramp running forever; reset() is what the
// read loop calls when the device disappears mid-gesture.
func TestResetStopsAnOrphanedRamp(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		h, a := testHandler()
		h.Handle(0, true, "VOLUME_DOWN")
		h.Reset()
		settled := a.count("VOLUME_DOWN")
		time.Sleep(volumeRepeatDelay + 5*volumeRepeat)
		if got := a.count("VOLUME_DOWN"); got != settled {
			t.Fatalf("ramp survived reset: %d then %d", settled, got)
		}
		if len(h.repeats) != 0 || len(h.holds) != 0 {
			t.Fatalf("reset left state behind: %d repeats, %d holds", len(h.repeats), len(h.holds))
		}
	})
}

// A press whose release never arrives stops once the ramp could have crossed the
// whole scale, not half a minute later.
func TestLostReleaseStopsAtFullScale(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		h, a := testHandler()
		h.Handle(0, true, "VOLUME_UP")
		time.Sleep(volumeRampMax + volumeRepeat)
		settled := a.count("VOLUME_UP")
		if settled < 100 {
			t.Fatalf("steps = %d, want a held key to reach the top of the scale", settled)
		}
		time.Sleep(5 * time.Second)
		if got := a.count("VOLUME_UP"); got != settled {
			t.Fatalf("ramp kept going without a release: %d then %d", settled, got)
		}
	})
}

// A duplicate press (release edge lost on the wire) restarts the ramp instead of
// stacking a second one that nothing will ever stop.
func TestDuplicatePressDoesNotStackRamps(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		h, a := testHandler()
		h.Handle(0, true, "VOLUME_UP")
		h.Handle(0, true, "VOLUME_UP")
		h.Handle(0, false, "VOLUME_UP")
		settled := a.count("VOLUME_UP")
		time.Sleep(volumeRepeatDelay + 5*volumeRepeat)
		if got := a.count("VOLUME_UP"); got != settled {
			t.Fatalf("a ramp outlived the release: %d then %d", settled, got)
		}
	})
}

// Power and source act on the press edge, and the release must not act again.
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

// firedChords records which chords fired. Chord actions run on a timer goroutine,
// so the recorder is guarded.
type firedChords struct {
	mu    sync.Mutex
	names []string
}

func testChordHandler(fired *firedChords) (*buttonHandler, *fakeActor) {
	a := &fakeActor{storable: true}
	record := fired.record
	chords := []chord{
		{name: "factory-reset", keys: []string{"PRESET_1", "VOLUME_DOWN"}, hold: 60 * time.Millisecond, action: record("factory-reset")},
		{name: "setup-mode", keys: []string{"PRESET_2", "VOLUME_DOWN"}, hold: 60 * time.Millisecond, action: record("setup-mode")},
	}
	return newButtonHandler(a, slog.New(slog.NewTextHandler(io.Discard, nil)), chords...), a
}

// The panel counts the seconds down while a chord is held, and the lamps stop when it ends.
func TestChordCountsDownOnThePanel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords
		h, _ := testChordHandler(&fired)
		h.chords[0].hold = 3 * time.Second
		var shown []string
		h.feedback = func(s string) { shown = append(shown, s) }
		h.Handle(6, true, "PRESET_1")
		h.Handle(4, true, "VOLUME_DOWN")
		if !chordCounting.Load() {
			t.Fatal("the lamps do not show a held chord")
		}
		time.Sleep(4 * time.Second)
		synctest.Wait()
		if chordCounting.Load() {
			t.Fatal("the lamps still count after the chord fired")
		}
		if want := []string{"Hold to reset", "Hold to reset 2", "Hold to reset 1"}; !slices.Equal(shown, want) {
			t.Fatalf("shown = %q, want %q", shown, want)
		}
		h.Handle(4, false, "VOLUME_DOWN")
		h.Handle(6, false, "PRESET_1")

		shown = nil
		h.Handle(6, true, "PRESET_1")
		h.Handle(4, true, "VOLUME_DOWN")
		h.Handle(4, false, "VOLUME_DOWN")
		h.Handle(6, false, "PRESET_1")
		time.Sleep(4 * time.Second)
		synctest.Wait()
		if slices.Contains(shown, "Hold to reset 2") || chordCounting.Load() {
			t.Fatalf("a released chord kept counting: %q", shown)
		}
	})
}

// Holding the stock factory-reset combo long enough fires it, and — critically —
// releasing out of it must not also store a preset, which the preset key's own hold
// timer would otherwise have done.
func TestChordFiresAndSuppressesTheKeyGestures(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords
		h, a := testChordHandler(&fired)
		h.Handle(6, true, "PRESET_1")
		h.Handle(4, true, "VOLUME_DOWN")
		time.Sleep(250 * time.Millisecond)
		h.Handle(4, false, "VOLUME_DOWN")
		h.Handle(6, false, "PRESET_1")
		if got := fired.list(); len(got) != 1 || got[0] != "factory-reset" {
			t.Fatalf("fired = %v, want one factory-reset", got)
		}
		keys, stored := a.snapshot()
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
func TestChordReleasedEarlyResumesSurvivingPreset(t *testing.T) {
	var fired firedChords
	h, a := testChordHandler(&fired)
	h.Handle(6, true, "PRESET_1")
	h.Handle(4, true, "VOLUME_DOWN")
	h.Handle(4, false, "VOLUME_DOWN")
	h.Handle(6, false, "PRESET_1")

	keys, _ := a.snapshot()
	if len(keys) != 1 || keys[0] != "PRESET_1" {
		t.Fatalf("keys = %v, want surviving PRESET_1 press", keys)
	}
}

// The two stock combos share volume-down, so the one that is fully held must win
// and the other must not arm behind it.
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
func TestResetCancelsAnArmedChord(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords
		h, _ := testChordHandler(&fired)
		h.Handle(6, true, "PRESET_1")
		h.Handle(4, true, "VOLUME_DOWN")
		h.Reset()
		time.Sleep(250 * time.Millisecond)
		if got := fired.list(); len(got) != 0 {
			t.Fatalf("a chord survived reset: %v", got)
		}
		if len(h.down) != 0 {
			t.Fatalf("reset left %d keys marked as held", len(h.down))
		}
	})
}

// A single preset press must still behave normally when chords are configured.
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
func TestPresetStoreFiresChime(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		h, a := testHandler()
		a.storable = true
		var chimes atomic.Int32
		h.chime = func() { chimes.Add(1) }
		h.Handle(6, true, "PRESET_1")
		deadline := time.Now().Add(holdToStore + 2*time.Second)
		for time.Now().Before(deadline) {
			if _, stored := a.snapshot(); len(stored) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		h.Handle(6, false, "PRESET_1")
		// Brief wait: the chime fires in the same AfterFunc goroutine as the store.
		time.Sleep(50 * time.Millisecond)
		if got := chimes.Load(); got != 1 {
			t.Fatalf("chimes = %d, want 1 after a successful store", got)
		}
	})
}

func TestPresetNonStoreDoesNotFireChime(t *testing.T) {
	// A fake clock: the sleeps below cost no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		h, a := testHandler()
		a.storable = false // nothing is playing
		var chimes atomic.Int32
		h.chime = func() { chimes.Add(1) }
		h.Handle(6, true, "PRESET_1")
		deadline := time.Now().Add(holdToStore + 2*time.Second)
		for time.Now().Before(deadline) {
			if _, stored := a.snapshot(); len(stored) > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		h.Handle(6, false, "PRESET_1")
		time.Sleep(50 * time.Millisecond)
		if got := chimes.Load(); got != 0 {
			t.Fatalf("chimes = %d, want 0 when nothing was stored", got)
		}
	})
}

// A short tap of SOURCE_TOGGLE still toggles the source when the hold selects Bluetooth.
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
func TestSourceToggleHoldSelectsBluetooth(t *testing.T) {
	// A fake clock: the sleep below costs no wall time and cannot flake.
	synctest.Test(t, func(t *testing.T) {
		h, a := testHandler()
		h.auxHoldBT = true
		h.Handle(12, true, "SOURCE_TOGGLE")
		time.Sleep(holdToStore + 100*time.Millisecond)
		h.Handle(12, false, "SOURCE_TOGGLE")
		keys, _ := a.snapshot()
		if len(keys) != 1 || keys[0] != sourceBluetooth {
			t.Fatalf("keys = %v, want one %s after a hold", keys, sourceBluetooth)
		}
	})
}

// Without Bluetooth, SOURCE_TOGGLE uses the simple press behaviour.
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
func TestLostReleaseDoesNotLeakHoldTimer(t *testing.T) {
	h, a := testHandler()
	h.auxHoldBT = true

	// Two presses with no release in between, then one release: exactly one timer may
	// be armed, and the release must leave nothing behind that can still fire.
	h.Handle(3, true, "SOURCE_TOGGLE")
	h.Handle(3, true, "SOURCE_TOGGLE")
	if n := len(h.holds); n != 1 {
		t.Fatalf("holds = %d after a duplicate press, want 1", n)
	}
	h.Handle(3, false, "SOURCE_TOGGLE")
	if n := len(h.holds); n != 0 {
		t.Fatalf("holds = %d after release, want 0", n)
	}
	if keys, _ := a.snapshot(); len(keys) != 1 || keys[0] != "SOURCE_TOGGLE" {
		t.Errorf("keys = %v, want one SOURCE_TOGGLE toggle", keys)
	}

	// The same for a preset: a leaked timer would store a slot nobody held.
	h.Handle(1, true, "PRESET_1")
	h.Handle(1, true, "PRESET_1")
	if n := len(h.holds); n != 1 {
		t.Fatalf("preset holds = %d after a duplicate press, want 1", n)
	}
	h.Handle(1, false, "PRESET_1")
	if n := len(h.holds); n != 0 {
		t.Fatalf("preset holds = %d after release, want 0", n)
	}
	if _, stored := a.snapshot(); len(stored) != 0 {
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
	if runtime.GOOS != "linux" {
		t.Skip("FIFO reads are only interruptible by Close where Go polls FIFOs (Linux)")
	}
	fifo := filepath.Join(t.TempDir(), "keypad")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no FIFO support:", err)
	}
	writer, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	h := newButtonHandler(&fakeActor{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	decode := func(record []byte) (int, bool, string, bool) { return 1, record[0] == 'P', "PRESET_1", true }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		hw.ReadKeyDevice(ctx, fifo, 1, decode, h)
		close(done)
	}()
	if _, err := writer.Write([]byte("P")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reader still blocked after cancellation")
	}
	if len(h.holds) != 0 || len(h.down) != 0 {
		t.Fatalf("gesture survived cancellation: holds=%d down=%d", len(h.holds), len(h.down))
	}
}

// Each recovery combo names itself while it counts down: the pairing combo said
// "Hold for setup" on the ST20, and the owner let go before it opened.
func TestChordFeedbackNamesTheCombo(t *testing.T) {
	for _, c := range []struct{ preset, want string }{
		{"PRESET_1", "Hold to reset"}, {"PRESET_2", "Hold for setup"},
	} {
		nop := func() {}
		h := newButtonHandler(&fakeActor{}, slog.New(slog.NewTextHandler(io.Discard, nil)), recoveryChords(nop, nop)...)
		var feedback string
		h.feedback = func(s string) { feedback = s }
		h.Handle(1, true, "VOLUME_DOWN")
		h.Handle(2, true, c.preset)
		h.disarmChord()
		if feedback != c.want {
			t.Errorf("%s + VOLUME_DOWN: feedback = %q, want %q", c.preset, feedback, c.want)
		}
	}
}

// A combo held for seconds and let go early was meant: the key still held must not
// then play its preset on release (Preset 6 + Volume down, let go at 8 s, played 6).
func TestChordHeldThenReleasedEarlySwallowsTheSurvivor(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords
		h, a := testChordHandler(&fired)
		h.chords[0].hold = time.Minute
		h.Handle(6, true, "PRESET_1")
		h.Handle(4, true, "VOLUME_DOWN")
		time.Sleep(chordMeant + time.Second)
		h.Handle(4, false, "VOLUME_DOWN")
		h.Handle(6, false, "PRESET_1")
		time.Sleep(holdToStore + time.Second)
		if keys, stored := a.snapshot(); len(keys) != 0 || len(stored) != 0 {
			t.Fatalf("keys = %v, stored = %v, want none", keys, stored)
		}
		if got := fired.list(); len(got) != 0 {
			t.Fatalf("fired = %v", got)
		}
	})
}

// While an install writes NAND only the volume keys act.
func TestKeysLockedDuringInstall(t *testing.T) {
	firmwareActive.Store(true)
	t.Cleanup(func() { firmwareActive.Store(false) })
	h, a := testHandler()
	var shown string
	h.feedback = func(s string) { shown = s }
	for _, key := range []string{"PRESET_1", "POWER", "SOURCE_TOGGLE", "BLUETOOTH"} {
		h.Handle(0, true, key)
		h.Handle(0, false, key)
	}
	h.Handle(4, true, "VOLUME_DOWN")
	h.Handle(4, false, "VOLUME_DOWN")
	keys, _ := a.snapshot()
	if strings.Join(keys, ",") != "VOLUME_DOWN" || shown != "Updating" {
		t.Fatalf("keys = %v, shown %q; want only VOLUME_DOWN and Updating", keys, shown)
	}
}

// POWER acts on the press, and a long hold adds nothing: low-power standby is not
// supported, so holding POWER must not turn into a second toggle.
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
func TestBluetoothButton(t *testing.T) {
	ev := make([]byte, 16)
	binary.LittleEndian.PutUint16(ev[8:], 1)
	binary.LittleEndian.PutUint16(ev[10:], 237)
	binary.LittleEndian.PutUint32(ev[12:], 1)
	if code, pressed, action, ok := hw.InputEvent(ev); !ok || !pressed || action != "BLUETOOTH" {
		t.Fatalf("KEY_BLUETOOTH decodes to %d %v %v", code, pressed, ok)
	}
	synctest.Test(t, func(t *testing.T) {
		h, a := testHandler()
		var pairs atomic.Int32
		h.btPair = func() { pairs.Add(1) }
		h.Handle(0, true, "BLUETOOTH")
		h.Handle(0, false, "BLUETOOTH")
		h.Handle(0, true, "BLUETOOTH")
		time.Sleep(holdToStore + 100*time.Millisecond)
		h.Handle(0, false, "BLUETOOTH")
		keys, _ := a.snapshot()
		if strings.Join(keys, ",") != "BLUETOOTH,BLUETOOTH" || pairs.Load() != 1 {
			t.Fatalf("keys = %v, pairing windows = %d; want two BLUETOOTH and one window", keys, pairs.Load())
		}
	})
}

// The Wireless Link reaches setup with its two keys, and neither key does its own job.
func TestTwoKeyChordOpensSetupWithoutSourceChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fired firedChords
		chords := recoveryChords(fired.record("reset"), fired.record("setup"))
		a := &fakeActor{storable: true}
		h := newButtonHandler(a, slog.New(slog.NewTextHandler(io.Discard, nil)), chords...)
		h.auxHoldBT = true
		h.btPair = func() { t.Error("the chord started pairing") }
		h.Handle(0, true, keySourceToggle)
		h.Handle(1, true, sourceBluetooth)
		time.Sleep(chordHold + time.Second)
		synctest.Wait()
		h.Handle(0, false, keySourceToggle)
		h.Handle(1, false, sourceBluetooth)
		synctest.Wait()
		if got := fired.list(); !slices.Equal(got, []string{"setup"}) {
			t.Fatalf("fired %v, want setup only", got)
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if len(a.keys) != 0 {
			t.Fatalf("keys applied: %v", a.keys)
		}
	})
}

// A chord takes the operations lock when it fires: a BCO flash that holds it, or a
// Core install that took it while the chord was counting down, refuses the reset.
func TestChordRefusedWhileAnOperationHoldsTheLock(t *testing.T) {
	oplock.Path = filepath.Join(t.TempDir(), "operation.lock")
	t.Cleanup(func() { oplock.Path = "" })
	for _, tc := range []struct {
		name       string
		lockBefore bool
	}{{"BCO flash first", true}, {"install after the chord armed", false}} {
		t.Run(tc.name, func(t *testing.T) {
			var install, chordLock oplock.Lock
			var resets, refusals atomic.Int32
			h := newButtonHandler(&fakeActor{}, slog.New(slog.NewTextHandler(io.Discard, nil)), chord{
				name: "factory-reset", keys: []string{"PRESET_1", "VOLUME_DOWN"}, hold: 500 * time.Millisecond,
				action: exclusive(&chordLock, false, func() { refusals.Add(1) }, func() { resets.Add(1) }),
			})
			if tc.lockBefore && !install.TryLock() {
				t.Fatal("flash could not take the lock")
			}
			h.Handle(6, true, "PRESET_1")
			h.Handle(4, true, "VOLUME_DOWN")
			if !tc.lockBefore && !install.TryLock() {
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
	exclusive(&chordLock, false, func() {}, func() { ran = true })()
	if !ran || install.TryLock() {
		t.Fatalf("reset ran %v; the lock was free after it: want it held", ran)
	}
}
