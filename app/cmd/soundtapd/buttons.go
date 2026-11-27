// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/oplock"
)

// keySourceToggle is the source key: AUX and Bluetooth on a tap, Bluetooth on a hold.
const keySourceToggle = hw.KeySourceToggle

// The volume keys' action names, as the keymaps spell them.
const (
	keyVolumeUp   = "VOLUME_UP"
	keyVolumeDown = "VOLUME_DOWN"
)

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
		{name: "factory-reset", keys: []string{"PRESET_1", keyVolumeDown}, hold: chordHold, action: factoryReset},
		// Stock: preset 2 + volume down, held until the light turns amber.
		{name: "setup-mode", keys: []string{"PRESET_2", keyVolumeDown}, hold: chordHold, action: setupMode},
		// The Wireless Link has only these two keys, and a network that went away left it
		// with no way into setup short of a USB update. Our gesture, see docs/hardware/wla/buttons.md.
		{
			name: "setup-mode-two-keys", keys: []string{keySourceToggle, sourceBluetooth},
			hold: chordHold, action: setupMode,
		},
	}
}

// resetLock is the operations lock a physical factory reset takes and never gives back:
// soundtapd exits holding it, so no install starts between the reset and the restart. A
// package variable, so its open lock file is never collected and closed under it.
//
//nolint:gochecknoglobals // process-wide state: held from the reset until the process exits
var resetLock oplock.Lock

// exclusive runs action under the operations lock, taken when the chord fires and not when
// it was pressed: a BCO flash never sets firmwareActive, and an install that started while
// the chord was counting down must still win. The lock is given back when action returns.
func exclusive(op *oplock.Lock, refused, action func()) func() {
	return exclusiveKept(op, refused, func() {
		defer op.Unlock()

		action()
	})
}

// exclusiveKept is exclusive that keeps the lock after action returns.
func exclusiveKept(op *oplock.Lock, refused, action func()) func() {
	return func() {
		if !op.TryLock() {
			refused()

			return
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
	chordShow  chordPanel // the ST20 panel's count while a chord is held
	chordArmed string
	chordSince time.Time
	swallow    map[string]bool // keys of an abandoned combo whose release does nothing

	// confirms says a key held here can confirm a waiting request (confirm.go): the
	// speaker's own panel, and the SoundTouch 300's processor keys (newLPMKeys). The IR
	// remote of the other models leaves it false: a remote works from the next room, and a
	// confirmation is proof that someone stands at the speaker.
	confirms   bool
	confirmMu  sync.Mutex   // guards confirm: the hold and grace timers change it too
	confirm    *confirmHeld // the hold that may confirm a waiting request, nil when none
	confirmLog []any        // attributes of the current key for the confirm log lines
}

// confirmGrace is how long a release of the confirming key may last and still count as
// part of the hold. The ST20's panel is a polled matrix with a 60 ms debounce, and a
// thumb that rolls on the key during a two-second hold opens the contact for a sweep or
// two: the hold started over, and the owner held for four seconds instead of two. A
// release this long is letting go.
const confirmGrace = 250 * time.Millisecond

// confirmHeld is one hold of a key to confirm the request that waited when it went down.
// Its timers check that it is still the handler's current hold before they act, so a
// timer that fires as the hold ends or restarts does nothing.
type confirmHeld struct {
	key      string
	gen      uint64      // the request this hold confirms
	attrs    []any       // the key's log attributes
	timer    *time.Timer // confirmHold after the key went down
	grace    *time.Timer // running while the key is up, until it counts as let go
	released bool        // the key is up, within confirmGrace
	due      bool        // confirmHold passed while the key was up: confirm on the next press
	fired    bool        // the hold ran its full time while held
}

// What the panel says a held chord does, over the seconds left and a bar (panel.Panel.ChordDown).
const (
	chordSetupMode    = "Setup mode"
	chordFactoryReset = "Factory reset"
)

// chordPanel is the panel's side of a chord: frontPanel on the speaker, a fake in tests.
type chordPanel interface {
	ChordDown(word string, hold time.Duration)
	ChordUp()
}

// chordCounting is set while a chord is held and has not fired, so the lamps show it is
// working: stock has its own lamp state for this, and the holds run for many seconds.
//
//nolint:gochecknoglobals // process-wide state: the key loops set it, the lamp loop reads it
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
		sp:         sp,
		log:        log,
		feedback:   nil,
		chime:      nil,
		auxHoldBT:  false,
		btPair:     nil,
		holds:      map[int]*time.Timer{},
		repeats:    map[int]chan struct{}{},
		chords:     chords,
		down:       map[string]bool{},
		downIndex:  map[string]int{},
		chordTimer: nil,
		chordShow:  frontPanel,
		chordArmed: "",
		chordSince: time.Time{},
		swallow:    map[string]bool{},
		confirms:   false,
		confirmMu:  sync.Mutex{},
		confirm:    nil,
		confirmLog: nil,
	}
}

func isVolumeKey(action string) bool {
	return action == keyVolumeUp || action == keyVolumeDown
}

// Handle takes one key edge of the panel, a key device or the remote (hw.KeySink).
//
//nolint:revive // hw.KeySink's signature: pressed is the edge, which Handle routes on
func (h *buttonHandler) Handle(index int, pressed bool, action string) {
	if pressed && h.confirmPressed(action) || !pressed && h.confirmReleased(action) {
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

	if pressed {
		if h.chordPressed() {
			return
		}

		h.dispatchPress(index, action)

		return
	}

	if h.chordReleased(action) {
		return
	}

	h.dispatchRelease(index, action)
}

// chordPressed takes a press that arms a recovery chord, and reports whether it took it.
func (h *buttonHandler) chordPressed() bool {
	if !h.armChord() {
		return false
	}
	// The gesture is a chord now, not two single presses: drop the store
	// timer the preset key armed and the ramp the volume key started, so
	// releasing out of a recovery combo cannot also store a preset.
	h.cancelGestures()

	return true
}

// chordReleased takes a release that ends a recovery chord, and reports whether it took it.
func (h *buttonHandler) chordReleased(action string) bool {
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

// dispatchRelease acts on a release that is neither a confirmation nor part of a chord.
func (h *buttonHandler) dispatchRelease(index int, action string) {
	switch {
	case strings.HasPrefix(action, "PRESET_"):
		h.presetReleased(index, action)
	case isVolumeKey(action):
		h.stopRepeat(index)
	case action == sourceBluetooth && h.btPair != nil:
		h.bluetoothReleased(index)
	case action == keySourceToggle && h.auxHoldBT:
		h.sourceToggleReleased(index)
	default: // the other keys act on their press
	}
}

// dispatchPress acts on a press that is neither a confirmation nor part of a chord.
func (h *buttonHandler) dispatchPress(index int, action string) {
	switch {
	case strings.HasPrefix(action, "PRESET_"):
		h.presetPressed(index, action)
	case isVolumeKey(action):
		h.startRepeat(index, action)
	case action == sourceBluetooth && h.btPair != nil:
		h.bluetoothPressed(index)
	case action == keySourceToggle && h.auxHoldBT:
		h.sourceTogglePressed(index)
	default:
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

// confirmReleased takes the release of the key held to confirm a waiting request, and
// reports whether it took it (confirmPressed). A release shorter than confirmGrace is
// contact chatter, not letting go: the grace timer decides, and a press of the same key
// within it carries on the hold.
func (h *buttonHandler) confirmReleased(action string) bool {
	h.confirmMu.Lock()
	defer h.confirmMu.Unlock()

	held := h.confirm
	if held == nil || action != held.key {
		return false
	}

	if held.fired {
		h.confirm = nil

		return true
	}

	if !held.released {
		held.released = true
		held.grace = time.AfterFunc(confirmGrace, func() { h.confirmLetGo(held) })
	}

	return true
}

// confirmPressed takes the keys that confirm a waiting request, and reports whether it took
// this press. While a request waits, a key on the panel other than volume belongs to the
// confirmation: held for confirmHold it confirms the request that was waiting when the
// key went down, and a shorter press confirms nothing. Neither plays, stores or switches
// anything, and the release is swallowed. Volume keys keep working, so someone can still
// turn the speaker down while it waits.
//
// A press of the confirming key within confirmGrace of its release carries on the hold
// from when it began. The hold confirms only while the key is down, so a hold whose time
// ran out during such a gap confirms on the press that ends the gap.
func (h *buttonHandler) confirmPressed(action string) bool {
	h.confirmMu.Lock()
	defer h.confirmMu.Unlock()

	held := h.confirm
	if held != nil && action == held.key && held.released {
		// Pressed again within the grace: the same hold.
		held.grace.Stop()
		held.released, held.grace = false, nil

		if held.due {
			held.due = false
			h.confirmFire(held)
		}

		return true
	}

	if !h.confirms || isVolumeKey(action) {
		return false
	}

	gen := confirmer.waiting()
	if gen == 0 {
		return false
	}

	if held != nil {
		// One key confirms; a second one pressed meanwhile does nothing, release included.
		h.swallow[action] = true

		return true
	}

	attrs := append([]any{"key", action}, h.confirmLog...)
	h.log.Info("button held for a waiting request", attrs...)

	held = &confirmHeld{
		key: action, gen: gen, attrs: attrs, timer: nil, grace: nil, released: false, due: false, fired: false,
	}
	h.confirm = held
	held.timer = time.AfterFunc(confirmHold, func() {
		h.confirmMu.Lock()
		defer h.confirmMu.Unlock()

		if h.confirm != held {
			return
		}

		if held.released {
			held.due = true

			return
		}

		h.confirmFire(held)
	})

	return true
}

// confirmFire confirms the request hold held began under, with the key down. Called with
// confirmMu held.
func (h *buttonHandler) confirmFire(held *confirmHeld) {
	held.fired = true
	if confirmer.held(held.gen) {
		h.log.Info("button confirmed a request", held.attrs...)
	}
}

// confirmLetGo ends hold held once its key stayed up for confirmGrace: letting go before
// confirmHold confirms nothing and asks again.
func (h *buttonHandler) confirmLetGo(held *confirmHeld) {
	h.confirmMu.Lock()
	defer h.confirmMu.Unlock()

	if h.confirm != held || !held.released {
		return
	}

	held.timer.Stop()

	h.confirm = nil

	if !held.fired {
		h.log.Info("button released before it confirmed", "key", held.key, "hold", confirmHold)
		confirmer.remind()
	}
}

// confirmCancel drops the current hold without confirming or asking again.
func (h *buttonHandler) confirmCancel() {
	h.confirmMu.Lock()
	defer h.confirmMu.Unlock()

	if held := h.confirm; held != nil {
		held.timer.Stop()

		if held.grace != nil {
			held.grace.Stop()
		}
	}

	h.confirm = nil
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

	for _, chordDef := range h.chords {
		if !allDown(h.down, chordDef.keys) {
			continue
		}

		name, action := chordDef.name, chordDef.action
		h.chordArmed, h.chordSince = name, time.Now()
		h.log.Info("chord held, counting down", "chord", name, "hold", chordDef.hold)

		text := chordSetupMode
		if name == "factory-reset" {
			text = chordFactoryReset
		}
		// The panel counts the seconds down with a bar, above the volume overlay the
		// chord's volume key brings up, and stays until the chord fires or is let go.
		show := h.chordShow
		show.ChordDown(text, chordDef.hold)
		chordCounting.Store(true)

		h.chordTimer = time.AfterFunc(chordDef.hold, func() {
			chordCounting.Store(false)
			show.ChordUp()
			h.log.Warn("chord fired", "chord", name)
			action()
		})

		return true
	}

	return false
}

func (h *buttonHandler) disarmChord() bool {
	chordCounting.Store(false)

	canceled := h.chordTimer != nil && h.chordTimer.Stop()
	if canceled {
		h.chordShow.ChordUp() // a chord that fired has ended its count itself
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
			h.presetPressed(index, action)
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

		if slices.Contains(c.keys, action) {
			return true
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

// presetNumber is the slot a PRESET_n action names.
func presetNumber(action string) int {
	preset, _ := strconv.Atoi(strings.TrimPrefix(action, "PRESET_"))

	return preset
}

// presetPressed arms the store of a preset held for holdToStore.
func (h *buttonHandler) presetPressed(index int, action string) {
	// The panel shows the "storing" bar once the key is held a moment (panelState).
	frontPanel.PresetDown(presetNumber(action))
	h.armHold(index, holdToStore, func() {
		stored, err := h.sp.StoreCurrentAsPreset(action)

		switch {
		case err != nil:
			h.log.Error("button store preset", "key", action, "err", err)
			frontPanel.PresetUp()
			h.show("Preset store failed")
		case stored:
			h.log.Info("button store preset (held)", "key", action)
			frontPanel.PresetStored()

			if h.chime != nil {
				h.chime()
			}
		default:
			h.log.Info("button store: nothing storable", "key", action)
			frontPanel.PresetUp()
			h.show("Nothing to store")
		}
	})
}

// presetReleased plays a preset tapped shorter than holdToStore.
func (h *buttonHandler) presetReleased(index int, action string) {
	// A pending timer means the store never fired, so this is a short tap. As stock shows
	// the preset's number at once: a station can take seconds to start.
	if h.releaseHold(index) {
		h.log.Info("button play", "key", action)
		frontPanel.PresetTapped(presetNumber(action))
		h.sp.ApplyKey(action)

		return
	}

	frontPanel.PresetUp()
}

// sourceToggleHold is stock's source key: a tap toggles the source, a press-and-hold
// selects Bluetooth (BoseApp: "KEY_VAL_AUX_INPUT pressed in PnH"). Pairings are cleared
// on the web page only. The hold timer is keyed by device index like preset holds.
func (h *buttonHandler) sourceTogglePressed(index int) {
	h.armHold(index, holdToStore, func() {
		h.log.Info("source hold: selecting Bluetooth")
		h.sp.ApplyKey(sourceBluetooth)
	})
}

// sourceToggleReleased toggles the source after a tap of the source key (sourceTogglePressed).
func (h *buttonHandler) sourceToggleReleased(index int) {
	if h.releaseHold(index) {
		// Timer not yet fired: this was a short tap, do the normal toggle.
		h.log.Info("button", "key", keySourceToggle)
		h.sp.ApplyKey(keySourceToggle)
	}
	// If the timer already fired (hold completed), the release is ignored.
}

// bluetoothHold is the Bluetooth key: a tap selects Bluetooth, a press-and-hold opens the
// pairing window, as the stock key does.
func (h *buttonHandler) bluetoothPressed(index int) {
	h.armHold(index, holdToStore, func() {
		h.log.Info("bluetooth hold: opening the pairing window")
		h.sp.ApplyKey(sourceBluetooth)
		h.btPair()
	})
}

// bluetoothReleased selects Bluetooth after a tap of the Bluetooth key (bluetoothPressed).
func (h *buttonHandler) bluetoothReleased(index int) {
	if h.releaseHold(index) {
		h.log.Info("button", "key", sourceBluetooth)
		h.sp.ApplyKey(sourceBluetooth)
	}
}
