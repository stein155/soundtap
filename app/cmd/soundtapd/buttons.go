// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/oplock"
	"github.com/stein155/soundtap/app/internal/panel"
)

// keySourceToggle is the source key: AUX and Bluetooth on a tap, Bluetooth on a hold.
const keySourceToggle = hw.KeySourceToggle

const (
	holdToStore = 1750 * time.Millisecond
	// Volume ramps while the key is held, like the stock speaker: one step on the
	// press, then a repeat every volumeRepeat after volumeRepeatDelay. Without this
	// a user needs twenty separate presses to cross the range.
	volumeRepeatDelay = 300 * time.Millisecond
	volumeRepeat      = 300 * time.Millisecond
	// volumeRampMax ends a ramp once it has had time to cross the whole 0-100 scale.
	// Neither the keypad nor the IR driver repeats while a key is held, so a lost
	// release looks exactly like a hold; past the full scale a hold does nothing a
	// human wants, so stopping there loses nothing.
	volumeRampMax = volumeRepeatDelay + 100*volumeRepeat

	// Recovery combos, matching the stock speaker so the labels on the internet and
	// in the owner's guide still apply. Both are long holds on purpose: they are the
	// last way in when the network is unreachable, and neither may be reachable by
	// accident.
	chordHold = 10 * time.Second
	// chordMeant: a combo held this long was meant, so letting go early ends the whole
	// gesture. A shorter overlap was a slip, and the surviving key carries on.
	chordMeant = time.Second
)

// recoveryChords are front-panel escape hatches for when the network is unreachable.
func recoveryChords(factoryReset, setupMode func()) []chord {
	return []chord{
		// Stock: preset 1 + volume down, ten seconds.
		{name: "factory-reset", keys: []string{"PRESET_1", "VOLUME_DOWN"}, hold: chordHold, action: factoryReset},
		// Stock: preset 2 + volume down, held until the light turns amber.
		{name: "setup-mode", keys: []string{"PRESET_2", "VOLUME_DOWN"}, hold: chordHold, action: setupMode},
		// The Wireless Link has only these two keys, and a network that went away left it
		// with no way into setup short of a USB update. Our gesture, see docs/hardware/wla/buttons.md.
		{name: "setup-mode-two-keys", keys: []string{keySourceToggle, sourceBluetooth}, hold: chordHold, action: setupMode},
	}
}

// resetLock is the operations lock a physical factory reset takes and never gives back:
// soundtapd exits holding it, so no install starts between the reset and the restart. A
// package variable, so its open lock file is never collected and closed under it.
var resetLock oplock.Lock

// exclusive runs action under the operations lock, taken when the chord fires and not when
// it was pressed: a BCO flash never sets firmwareActive, and an install that started while
// the chord was counting down must still win. release false keeps the lock after
// action returns.
func exclusive(op *oplock.Lock, release bool, refused, action func()) func() {
	return func() {
		if !op.TryLock() {
			refused()
			return
		}
		if release {
			defer op.Unlock()
		}
		action()
	}
}

// keyActor is the slice of speakerstate the button loops drive. Taking an
// interface keeps the edge/hold/repeat logic testable without a live Speaker and
// without a real keypad.
type keyActor interface {
	ApplyKey(key string)
	StoreCurrentAsPreset(key string) (bool, error)
}

// buttonHandler turns press/release edges into speaker actions.
//
// Neither keypad backend reports "still held" — the ST10 char device emits one
// byte per edge, and the input path deliberately drops kernel autorepeat — so
// both hold behaviours are timed here:
//
//   - a preset stores at holdToStore while the key is still down (stock
//     behaviour: you feel it commit before you let go), and the release that
//     follows is then ignored;
//   - volume ramps until release.
//
// One handler belongs to one device read loop. The maps are only touched from
// that loop; the timer and repeat goroutines only call into sp, which is
// mutex-protected.
type buttonHandler struct {
	sp        keyActor
	log       *slog.Logger
	feedback  func(string)
	chime     func()                // fires once after a successful preset store (nil = no tone)
	auxHoldBT bool                  // SOURCE_TOGGLE held for holdToStore selects Bluetooth
	btPair    func()                // fires after BLUETOOTH held for holdToStore (nil = disabled)
	holds     map[int]*time.Timer   // preset hold-to-store, keyed by device index
	repeats   map[int]chan struct{} // volume auto-repeat, closed on release

	chords     []chord
	down       map[string]bool // actions currently held, by action name
	downIndex  map[string]int  // device index for each held action
	chordTimer *time.Timer
	chordStop  chan struct{} // ends the panel's count on release
	chordArmed string
	chordSince time.Time
	swallow    map[string]bool // keys of an abandoned combo whose release does nothing

	// confirms says a key held here can confirm a waiting request (confirm.go): the
	// speaker's own panel, and the SoundTouch 300's processor keys (newLPMKeys). The IR
	// remote of the other models leaves it false: a remote works from the next room, and a
	// confirmation is proof that someone stands at the speaker.
	confirms     bool
	confirmKey   string      // the key being held to confirm, "" when none
	confirmTimer *time.Timer // fires confirmHold after confirmKey went down
	confirmLog   []any       // attributes of the current key for the confirm log lines
}

// What the panel says while a chord is held.
const (
	holdForSetup = "Hold for setup"
	holdToReset  = "Hold to reset"
)

// chordCounting is set while a chord is held and has not fired, so the lamps show it is
// working: stock has its own lamp state for this, and the holds run for many seconds.
var chordCounting atomic.Bool

// chord is a two-or-more-key gesture held for a while — the stock speaker's
// recovery combos, which are the only way back in once the network is unreachable.
// Like every other hold on this keypad it is
// timed here, because the driver reports edges only.
type chord struct {
	name   string
	keys   []string // action names that must all be held at once
	hold   time.Duration
	action func()
}

func newButtonHandler(sp keyActor, log *slog.Logger, chords ...chord) *buttonHandler {
	return &buttonHandler{
		sp:        sp,
		log:       log,
		holds:     map[int]*time.Timer{},
		repeats:   map[int]chan struct{}{},
		chords:    chords,
		down:      map[string]bool{},
		downIndex: map[string]int{},
		swallow:   map[string]bool{},
	}
}

func isVolumeKey(action string) bool {
	return action == "VOLUME_UP" || action == "VOLUME_DOWN"
}

// Handle takes one key edge of the panel, a key device or the remote (hw.KeySink).
func (h *buttonHandler) Handle(index int, pressed bool, action string) {
	if h.confirming(pressed, action) {
		return
	}
	// An install owns the speaker, as stock's update state does: no preset, source,
	// power or recovery gesture starts while it writes NAND. Volume stays, so
	// someone can still turn a speaker down. The release of an ignored press finds no
	// timer and does nothing.
	if pressed && !isVolumeKey(action) && firmwareActive.Load() {
		h.log.Info("button ignored: firmware install in progress", "key", action)
		h.show("Updating")
		return
	}
	h.down[action] = pressed
	if pressed {
		frontPanel.Touch()
		h.downIndex[action] = index
		delete(h.swallow, action)
	} else if h.swallow[action] {
		delete(h.swallow, action)
		return
	}
	if h.chordEdge(pressed, action) {
		return
	}
	h.dispatch(index, pressed, action)
}

// chordEdge takes an edge that arms or ends a recovery chord, and reports whether it
// took it.
func (h *buttonHandler) chordEdge(pressed bool, action string) bool {
	if pressed {
		if !h.armChord() {
			return false
		}
		// The gesture is a chord now, not two single presses: drop the store
		// timer the preset key armed and the ramp the volume key started, so
		// releasing out of a recovery combo cannot also store a preset.
		h.cancelGestures()
		return true
	}
	if h.chordArmed == "" || !contains(h.chords, h.chordArmed, action) {
		return false
	}
	held := time.Since(h.chordSince)
	if h.disarmChord() {
		if held < chordMeant {
			h.resumeChordGestures()
		} else {
			// Letting go of Preset 6 + Volume down at 8 s played preset 6.
			for a, d := range h.down {
				if d {
					h.swallow[a] = true
				}
			}
		}
	}
	return true
}

// dispatch acts on an edge that is neither a confirmation nor part of a chord.
func (h *buttonHandler) dispatch(index int, pressed bool, action string) {
	switch {
	case strings.HasPrefix(action, "PRESET_"):
		h.preset(index, pressed, action)
	case isVolumeKey(action):
		if pressed {
			h.startRepeat(index, action)
		} else {
			h.stopRepeat(index)
		}
	case action == sourceBluetooth && h.btPair != nil:
		h.bluetoothHold(index, pressed)
	case action == keySourceToggle && h.auxHoldBT:
		h.sourceToggleHold(index, pressed)
	case pressed:
		// On press, including POWER, and that is stock's behaviour rather than an
		// oversight. CSystemController's key handler branches on a per-product flag
		// before it looks at the state field: without it, `tst.w r3xf000` takes
		// the state==PRESSED path and logs "Non-Taigan - KEY_VAL_POWER pressed";
		// with it, `ubfx r3, r3; cmp r3` takes RELEASED and logs
		// "Taigan - KEY_VAL_POWER released". Taigan is the battery Portable, which
		// none of these boxes are. Read at 0x155e3c on 2026-09-19.
		h.log.Info("button", "key", action)
		h.sp.ApplyKey(action)
	}
}

// confirming takes the keys that confirm a waiting request, and reports whether it took
// this edge. While a request waits, a key on the panel other than volume belongs to the
// confirmation: held for confirmHold it confirms the request that was waiting when the
// key went down, and a shorter press confirms nothing. Neither plays, stores or switches
// anything, and the release is swallowed. Volume keys keep working, so someone can still
// turn the speaker down while it waits.
func (h *buttonHandler) confirming(pressed bool, action string) bool {
	if !pressed {
		if h.confirmKey == "" || action != h.confirmKey {
			return false
		}
		if h.confirmTimer.Stop() {
			h.log.Info("button released before it confirmed", "key", action, "hold", confirmHold)
			confirmer.remind()
		}
		h.confirmKey, h.confirmTimer = "", nil
		return true
	}
	if !h.confirms || isVolumeKey(action) {
		return false
	}
	gen := confirmer.waiting()
	if gen == 0 {
		return false
	}
	if h.confirmKey != "" {
		// One key confirms; a second one pressed meanwhile does nothing, release included.
		h.swallow[action] = true
		return true
	}
	attrs := append([]any{"key", action}, h.confirmLog...)
	h.log.Info("button held for a waiting request", attrs...)
	h.confirmKey = action
	h.confirmTimer = time.AfterFunc(confirmHold, func() {
		if confirmer.held(gen) {
			h.log.Info("button confirmed a request", attrs...)
		}
	})
	return true
}

func (h *buttonHandler) show(text string) {
	if h.feedback != nil {
		h.feedback(text)
	}
}

// armChord starts the timer for whichever chord is now fully held, and reports
// whether one was armed. Only one at a time: the stock combos share the volume-down
// key, so a second cannot become held without the first being released.
func (h *buttonHandler) armChord() bool {
	if h.chordArmed != "" {
		return true // already counting; this press is part of it
	}
	for _, c := range h.chords {
		if !allDown(h.down, c.keys) {
			continue
		}
		name, action := c.name, c.action
		h.chordArmed, h.chordSince = name, time.Now()
		h.log.Info("chord held, counting down", "chord", name, "hold", c.hold)
		text := holdForSetup
		if name == "factory-reset" {
			text = holdToReset
		}
		h.show(text)
		chordCounting.Store(true)
		h.chordStop = make(chan struct{})
		go h.countDown(text, int(c.hold/time.Second)-1, h.chordStop)
		h.chordTimer = time.AfterFunc(c.hold, func() {
			chordCounting.Store(false)
			h.log.Warn("chord fired", "chord", name)
			action()
		})
		return true
	}
	return false
}

// countDown shows the seconds left on the panel while a chord is held, as stock does. A panel
// message lasts about two seconds, so without it the panel went quiet long before the chord
// fired. Ends by itself when the time is up, or when stop closes on release.
func (h *buttonHandler) countDown(text string, left int, stop <-chan struct{}) {
	for ; left > 0; left-- {
		select {
		case <-time.After(time.Second):
			h.show(panel.Text(text) + " " + strconv.Itoa(left))
		case <-stop:
			return
		}
	}
}

func (h *buttonHandler) disarmChord() bool {
	if h.chordStop != nil {
		close(h.chordStop)
		h.chordStop = nil
	}
	chordCounting.Store(false)
	canceled := h.chordTimer != nil && h.chordTimer.Stop()
	if canceled {
		h.log.Info("chord released early", "chord", h.chordArmed)
	}
	h.chordTimer, h.chordArmed = nil, ""
	return canceled
}

func (h *buttonHandler) resumeChordGestures() {
	for action, pressed := range h.down {
		if !pressed {
			continue
		}
		index := h.downIndex[action]
		if strings.HasPrefix(action, "PRESET_") {
			h.preset(index, true, action)
		} else if isVolumeKey(action) {
			h.startRepeat(index, action)
		}
	}
}

// cancelGestures drops the per-key holds and ramps without touching chord state.
func (h *buttonHandler) cancelGestures() {
	frontPanel.PresetUp()
	for index, t := range h.holds {
		t.Stop()
		delete(h.holds, index)
	}
	for index := range h.repeats {
		h.stopRepeat(index)
	}
}

func allDown(down map[string]bool, keys []string) bool {
	for _, k := range keys {
		if !down[k] {
			return false
		}
	}
	return len(keys) > 0
}

// contains reports whether the named chord uses action as one of its keys.
func contains(chords []chord, name, action string) bool {
	for _, c := range chords {
		if c.name != name {
			continue
		}
		for _, k := range c.keys {
			if k == action {
				return true
			}
		}
	}
	return false
}

// armHold starts a hold timer for a key, replacing any timer that key already has.
// The keypad is polled over sysfs, so a release edge can be missed (startRepeat says
// the same about volume ramps); without stopping the old timer first, a second press
// would leave the first one armed and it would fire on its own later. For a preset
// that stores a slot nobody asked to store; for SOURCE_TOGGLE it switches to
// Bluetooth; for the Bluetooth key it opens the pairing window.
func (h *buttonHandler) armHold(index int, d time.Duration, fn func()) {
	if t := h.holds[index]; t != nil {
		t.Stop()
	}
	h.holds[index] = time.AfterFunc(d, fn)
}

// releaseHold takes the hold timer for a key off the map and reports whether it was
// still pending — i.e. whether this release ends a short tap rather than a completed
// hold, which already ran its action.
func (h *buttonHandler) releaseHold(index int) bool {
	t := h.holds[index]
	delete(h.holds, index)
	return t != nil && t.Stop()
}

func (h *buttonHandler) preset(index int, pressed bool, action string) {
	n, _ := strconv.Atoi(strings.TrimPrefix(action, "PRESET_"))
	if pressed {
		// The panel shows the "storing" bar once the key is held a moment (panelState).
		frontPanel.PresetDown(n)
		h.armHold(index, holdToStore, func() {
			stored, err := h.sp.StoreCurrentAsPreset(action)
			if err != nil {
				h.log.Error("button store preset", "key", action, "err", err)
				frontPanel.PresetUp()
				h.show("Preset store failed")
			} else if stored {
				h.log.Info("button store preset (held)", "key", action)
				frontPanel.PresetStored()
				if h.chime != nil {
					h.chime()
				}
			} else {
				h.log.Info("button store: nothing storable", "key", action)
				frontPanel.PresetUp()
				h.show("Nothing to store")
			}
		})
		return
	}
	// A pending timer means the store never fired, so this is a short tap. As stock shows
	// the preset's number at once: a station can take seconds to start.
	if h.releaseHold(index) {
		h.log.Info("button play", "key", action)
		frontPanel.PresetTapped(n)
		h.sp.ApplyKey(action)
		return
	}
	frontPanel.PresetUp()
}

// sourceToggleHold is stock's source key: a tap toggles the source, a press-and-hold
// selects Bluetooth (BoseApp: "KEY_VAL_AUX_INPUT pressed in PnH"). Pairings are cleared
// on the web page only. The hold timer is keyed by device index like preset holds.
func (h *buttonHandler) sourceToggleHold(index int, pressed bool) {
	if pressed {
		h.armHold(index, holdToStore, func() {
			h.log.Info("source hold: selecting Bluetooth")
			h.sp.ApplyKey(sourceBluetooth)
		})
		return
	}
	if h.releaseHold(index) {
		// Timer not yet fired: this was a short tap, do the normal toggle.
		h.log.Info("button", "key", keySourceToggle)
		h.sp.ApplyKey(keySourceToggle)
	}
	// If the timer already fired (hold completed), the release is ignored.
}

// bluetoothHold is the Bluetooth key: a tap selects Bluetooth, a press-and-hold opens the
// pairing window, as the stock key does.
func (h *buttonHandler) bluetoothHold(index int, pressed bool) {
	if pressed {
		h.armHold(index, holdToStore, func() {
			h.log.Info("bluetooth hold: opening the pairing window")
			h.sp.ApplyKey(sourceBluetooth)
			h.btPair()
		})
		return
	}
	if h.releaseHold(index) {
		h.log.Info("button", "key", sourceBluetooth)
		h.sp.ApplyKey(sourceBluetooth)
	}
}
