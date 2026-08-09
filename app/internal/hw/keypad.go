// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/hw/model"
)

// The ST10/ST20 front panel is a 3x3 strobed matrix plus one directly wired key. On the
// stock kernel Bose's shelby-keypad.ko owns it and soundtapd only reads the byte stream
// it publishes on /dev/shelby-keypad (ReadKeyDevice). The mainline 6.12 kernel has no such
// driver, so the buttons were dead there — this file scans the matrix from userspace
// instead, and feeds the same KeySink the char device does.
//
// # WHY USERSPACE, AND WHY NO INTERRUPTS
//
// Earlier attempts put the keypad back in the kernel — gpio-keys, gpio-matrix-keypad,
// and a custom scanner — and every one of them stopped Wi-Fi from coming up, which is
// how the keypad ended up disabled on mainline in the first place. They all have one
// thing in common: they request GPIO interrupts on the switch-in lines. Those lines are
// gpio0_22/23/26/27, and gpio0 is also the parent of the WL1835's own interrupt
// (gpio0_31). A switch-in floats whenever its strobe is not asserted — the pads are
// muxed pull-disabled on purpose, because the pull-ups sit on the board behind the
// strobe FETs — so edge-triggered interrupts on them are an interrupt storm on the
// radio's interrupt line.
//
// The stock driver does not do that either. Disassembling shelby-keypad.ko shows
// keypad_button_tick() re-arming itself with mod_timer(jiffies + 1) and polling; the
// `interrupts` property in the stock device tree is only ever used to mark the lines as
// wake sources for suspend. So polling is not a workaround here, it is what the
// hardware was always driven with.
//
// GEOMETRY (from the stock rhino_sm2.dtb and shelby-keypad.ko, not guessed)
//
//	strobes    = <60 44 45>       gpio1_28 (gpmc_be1n), gpio1_12/13 (gpmc_ad12/13)
//	switchins  = <23 26 22 27>    gpio0_23/26/22/27 (gpmc_ad9/ad10/ad8/ad11)
//	has-fets   = <1>              a strobe is asserted high and released low
//
// ST20 spotty uses gpio0 throughout: strobes 28/20/17, switch-ins 22/23/26/29,
// has-fets=0. Its strobe is asserted low and released to input/high-Z.
//
// keypad_button_scan() asserts one strobe, reads switch-ins 0..2 and reports them as
// scancodes strobe*4+0..2, releases the strobe, and once per sweep reads switch-in 3
// unstrobed as scancode 12. Switch-ins read low when the key is down. That is exactly
// the index space defaultKeymap is written in, so the stock key map applies unchanged.
//
// The pads still have to be muxed to GPIO mode; that is the one kernel-side change, a
// pinctrl group in am335x-soundtap-st10.dts with no driver and no interrupt behind it.
const (
	// AM335x GPIO bank base addresses. Mainline numbers gpiochips in probe order, so
	// the sysfs bases are not the stock kernel's fixed 0/32/64/96 and have to be looked
	// up — the same lookup the init script does for the amp mute line.
	gpioBank0Addr = "44e07000" // switch-ins
	gpioBank1Addr = "4804c000" // strobes
)

var (
	keypadStrobeOffsets   = []int{28, 12, 13}     // gpio1: gpmc_be1n, gpmc_ad12, gpmc_ad13
	keypadSwitchinOffsets = []int{23, 26, 22, 27} // gpio0: gpmc_ad9, ad10, ad8, ad11
)

const (
	// One whole sweep per tick, so every key is sampled once per tick. The stock driver
	// runs a strobe per jiffy, a sweep per 30 ms. Driving one strobe per 10 ms tick gave
	// the same per-key rate, but each timer wakeup costs a scheduler pass on these
	// single-core boxes: the 100 Hz loop was 7 % CPU on the ST20, in standby too.
	keypadSweepStep = 30 * time.Millisecond
	// In standby a sweep per 60 ms, so a 120 ms debounce: still under a deliberate press,
	// and half the wakeups all night. The press that wakes the speaker brings the
	// next sweep back to 30 ms.
	keypadStandbySweep = 60 * time.Millisecond
	// Sweeps a raw reading must disagree with the reported state before we believe it.
	// At one sample per 30 ms sweep this is a 60 ms debounce, well under the shortest
	// deliberate press and well over contact bounce.
	keypadDebounce = 2
	// Scancode of the directly wired key, which is not part of the matrix.
	keypadDirectIndex = 12
)

// keypadMatrix is the opened front panel: strobes to drive, switch-ins to sample.
type keypadMatrix struct {
	strobes     []gpioLine
	switchins   []gpioLine
	activeLevel int
	releaseToIn bool
}

type keypadGeometry struct {
	strobeAddr, switchAddr       string
	strobeOffsets, switchOffsets []int
	activeLevel                  int
	releaseToIn                  bool
}

// GPIOLine is one line of a SoC GPIO bank, by the bank's register address.
type GPIOLine struct {
	Bank string
	Line int
}

// KeypadLines are the GPIO lines the keypad matrix of modelName drives and reads: what
// soundtap-root must let soundtapd export (internal/rootd).
func KeypadLines(modelName string) []GPIOLine {
	g := keypadGeometryFor(modelName)
	var out []GPIOLine
	for _, off := range g.strobeOffsets {
		out = append(out, GPIOLine{g.strobeAddr, off})
	}
	for _, off := range g.switchOffsets {
		out = append(out, GPIOLine{g.switchAddr, off})
	}
	return out
}

func keypadGeometryFor(modelName string) keypadGeometry {
	if modelName == model.ST20 {
		return keypadGeometry{
			strobeAddr: gpioBank0Addr, switchAddr: gpioBank0Addr,
			strobeOffsets: []int{28, 20, 17}, switchOffsets: []int{22, 23, 26, 29},
			activeLevel: 0, releaseToIn: true,
		}
	}
	return keypadGeometry{
		strobeAddr: gpioBank1Addr, switchAddr: gpioBank0Addr,
		strobeOffsets: keypadStrobeOffsets, switchOffsets: keypadSwitchinOffsets,
		activeLevel: 1,
	}
}

func openKeypadMatrix(modelName string) (*keypadMatrix, error) {
	g := keypadGeometryFor(modelName)
	strobeBase, err := gpioBankBase(g.strobeAddr)
	if err != nil {
		return nil, err
	}
	switchBase, err := gpioBankBase(g.switchAddr)
	if err != nil {
		return nil, err
	}
	m := &keypadMatrix{activeLevel: g.activeLevel, releaseToIn: g.releaseToIn}
	initial := "low"
	if g.releaseToIn {
		initial = "in"
	}
	for _, off := range g.strobeOffsets {
		line, err := openGPIOLine(strobeBase+off, initial)
		if err != nil {
			m.close()
			return nil, err
		}
		m.strobes = append(m.strobes, line)
	}
	for _, off := range g.switchOffsets {
		line, err := openGPIOLine(switchBase+off, "in")
		if err != nil {
			m.close()
			return nil, err
		}
		m.switchins = append(m.switchins, line)
	}
	return m, nil
}

func (m *keypadMatrix) release(g gpioLine) error {
	if m.releaseToIn {
		return g.direction("in")
	}
	return g.write(0)
}

func (m *keypadMatrix) close() {
	for _, g := range m.strobes {
		_ = m.release(g) // never leave a strobe driving the panel
		_ = g.Close()
	}
	for _, g := range m.switchins {
		_ = g.Close()
	}
	m.strobes, m.switchins = nil, nil
}

// keyRead is one raw sample: a scancode and whether its key is down.
type keyRead struct {
	index   int
	pressed bool
}

// step asserts one strobe, samples the three switch-ins under it, and releases the
// strobe again. The fourth switch-in is not part of the matrix — its key is wired
// straight to the line — so it is sampled once per sweep with no strobe asserted,
// exactly where the stock driver samples it.
//
// into is reused across ticks to keep a 100 Hz loop allocation-free.
func (m *keypadMatrix) step(strobe int, into []keyRead) ([]keyRead, error) {
	out := into[:0]
	if m.releaseToIn {
		if err := m.strobes[strobe].direction("low"); err != nil {
			return nil, err
		}
	} else if err := m.strobes[strobe].write(m.activeLevel); err != nil {
		return nil, err
	}
	for j := range len(m.switchins) - 1 {
		v, err := m.switchins[j].read()
		if err != nil {
			_ = m.release(m.strobes[strobe])
			return nil, err
		}
		out = append(out, keyRead{index: strobe*4 + j, pressed: v == 0}) // active low
	}
	if err := m.release(m.strobes[strobe]); err != nil {
		return nil, err
	}
	if strobe == len(m.strobes)-1 {
		v, err := m.switchins[len(m.switchins)-1].read()
		if err != nil {
			return nil, err
		}
		out = append(out, keyRead{index: keypadDirectIndex, pressed: v == 0})
	}
	return out, nil
}

// keypadScanner turns raw per-sweep samples into press and release edges. It owns the
// debounce; everything above it — hold-to-store, volume ramp, recovery chords — is the
// same KeySink the stock keypad path feeds.
type keypadScanner struct {
	m     *keypadMatrix
	keys  KeySink
	km    map[int]string
	log   *slog.Logger
	state map[int]bool // last reported state, by scancode
	seen  map[int]int  // consecutive sweeps disagreeing with state, by scancode
	reads []keyRead
	next  int // strobe to drive on the next tick

	// Nothing is dispatched until the panel has read completely idle once. If the
	// pads are not muxed, or the polarity is not what we think, the switch-ins read
	// low forever — and a "key" that is held from the first sweep would toggle
	// standby and ramp the volume before anyone touched the speaker. Waiting for
	// idle turns that failure into silence plus a log line. It also covers the
	// ordinary case of soundtapd restarting while a finger is on a button.
	settled              bool
	held                 []int // scancodes down during the sweep in progress
	sweeps               int
	startupFactorySweeps int
}

// Sweeps to wait before saying out loud that the panel never went idle: ~3 s, long
// enough that a deliberate press at startup does not trip it.
const keypadSettleWarn = 100

func newKeypadScanner(m *keypadMatrix, keys KeySink, km map[int]string, log *slog.Logger) *keypadScanner {
	return &keypadScanner{
		m: m, keys: keys, km: km, log: log,
		state: map[int]bool{},
		seen:  map[int]int{},
		reads: make([]keyRead, 0, 4),
	}
}

func startupFactoryChord(held []int, keymap map[int]string) bool {
	if len(held) != 2 {
		return false
	}
	found := map[string]bool{}
	for _, index := range held {
		found[keymap[index]] = true
	}
	return found["PRESET_1"] && found["VOLUME_DOWN"]
}

// tick drives one strobe and dispatches whatever changed under it.
func (s *keypadScanner) tick() error {
	reads, err := s.m.step(s.next, s.reads)
	if err != nil {
		return err
	}
	s.reads = reads
	endOfSweep := s.next == len(s.m.strobes)-1
	s.next = (s.next + 1) % len(s.m.strobes)

	if !s.settled {
		s.settle(reads, endOfSweep)
		return nil
	}

	for _, r := range reads {
		if s.state[r.index] == r.pressed {
			s.seen[r.index] = 0
			continue
		}
		s.seen[r.index]++
		if s.seen[r.index] < keypadDebounce {
			continue
		}
		s.seen[r.index] = 0
		s.state[r.index] = r.pressed
		action, ok := s.km[r.index]
		if !ok {
			s.log.Debug("unmapped key index", "index", r.index, "pressed", r.pressed)
			continue
		}
		s.keys.Handle(r.index, r.pressed, action)
	}
	return nil
}

// settle watches the panel until it reads idle once, or until the stock startup chord
// is held, before tick dispatches anything.
func (s *keypadScanner) settle(reads []keyRead, endOfSweep bool) {
	for _, r := range reads {
		if r.pressed {
			s.held = append(s.held, r.index)
		}
	}
	if !endOfSweep {
		return
	}
	s.sweeps++
	if startupFactoryChord(s.held, s.km) {
		s.startupFactorySweeps++
	} else {
		s.startupFactorySweeps = 0
	}
	switch {
	case len(s.held) == 0:
		s.settled = true
		s.log.Info("keypad idle, scanning", "sweeps", s.sweeps)
	case s.startupFactorySweeps >= keypadDebounce:
		// Stock accepts volume-down + preset-1 held through power-on. Trust only
		// that exact debounced pair; every other non-idle startup still waits.
		s.settled = true
		for _, index := range s.held {
			s.state[index] = true
			s.keys.Handle(index, true, s.km[index])
		}
		s.log.Warn("startup factory-reset chord armed")
	case s.sweeps == keypadSettleWarn:
		s.log.Warn("keypad never reads idle; ignoring it until it does",
			"scancodes_down", s.held, "sweeps", s.sweeps)
	}
	s.held = s.held[:0]
}

// KeypadMatrixSupported reports whether this model has a known front panel wired to GPIO
// banks we can find. BUTTON_MATRIX forces the answer either way, so the scan can be
// turned off on hardware without a rebuild if it ever misbehaves.
func KeypadMatrixSupported(modelName string) bool {
	if v := strings.TrimSpace(os.Getenv("BUTTON_MATRIX")); v != "" {
		return v == "1"
	}
	if modelName != model.ST10 && modelName != model.ST20 {
		return false
	}
	g := keypadGeometryFor(modelName)
	if _, err := gpioBankBase(g.switchAddr); err != nil {
		return false
	}
	_, err := gpioBankBase(g.strobeAddr)
	return err == nil
}

// RunKeypadMatrix polls the model's front panel until ctx ends, handing every debounced
// edge to keys. standby slows the sweep down.
func RunKeypadMatrix(ctx context.Context, modelName string, keys KeySink, km map[int]string, standby func() bool, log *slog.Logger) {
	for ctx.Err() == nil {
		m, err := openKeypadMatrix(modelName)
		if err != nil {
			// These are static platform resources. If they are missing now they will
			// not appear later, so this is a hard stop and not a retry loop.
			log.Error("keypad matrix unavailable", "err", err)
			return
		}
		log.Info("keypad matrix up", "strobes", lineNums(m.strobes), "switchins", lineNums(m.switchins))
		err = scanKeypad(ctx, m, keys, km, standby, log)
		m.close()
		keys.Reset() // a press whose release we will now never see must not leave a ramp running
		if ctx.Err() != nil {
			return
		}
		log.Warn("keypad scan stopped, reopening", "err", err)
		if !waitToReopen(ctx) {
			return
		}
	}
}

func scanKeypad(ctx context.Context, m *keypadMatrix, keys KeySink, km map[int]string, standby func() bool, log *slog.Logger) error {
	s := newKeypadScanner(m, keys, km, log)
	every := keypadSweepStep
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		for range m.strobes {
			if err := s.tick(); err != nil {
				return err
			}
		}
		retick(t, &every, keypadSweepEvery(standby()))
	}
}

func keypadSweepEvery(standby bool) time.Duration {
	if standby {
		return keypadStandbySweep
	}
	return keypadSweepStep
}

// retick moves t to want when that differs from the period it runs at; an unchanged
// period leaves the ticker's phase alone.
func retick(t *time.Ticker, every *time.Duration, want time.Duration) {
	if want != *every {
		*every = want
		t.Reset(want)
	}
}

func lineNums(lines []gpioLine) []int {
	out := make([]int, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.num())
	}
	return out
}
