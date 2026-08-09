// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/hw/model"
)

// In standby the keypad slows down; out of it it runs as before, and retick moves a
// running ticker back to the fast period on the first look after waking.
func TestStandbyRatesAndReturnToFast(t *testing.T) {
	if keypadSweepEvery(false) != 30*time.Millisecond || keypadSweepEvery(true) != 60*time.Millisecond {
		t.Fatalf("keypad sweep = %v awake, %v in standby", keypadSweepEvery(false), keypadSweepEvery(true))
	}
	every := keypadSweepStep
	tk := time.NewTicker(every)
	defer tk.Stop()
	retick(tk, &every, keypadSweepEvery(true))
	if every != keypadStandbySweep {
		t.Fatalf("standby: ticker at %v", every)
	}
	retick(tk, &every, keypadSweepEvery(false))
	if every != keypadSweepStep {
		t.Fatalf("after waking: ticker at %v, want %v", every, keypadSweepStep)
	}
}

// fakePanel is a simulated ST10 front panel: three strobe lines, four switch-ins, and a
// set of keys held down. Switch-ins are active low and only pulled down when the strobe
// their key sits on is asserted, which is what the real matrix does.
type fakePanel struct {
	strobes  [3]int
	asserted [3]bool
	// down is keyed by scancode, the same strobe*4+switchin space the stock driver uses.
	down map[int]bool
}

func newFakePanel() *fakePanel { return &fakePanel{down: map[int]bool{}} }

type fakeStrobe struct {
	p *fakePanel
	i int
}

func (f *fakeStrobe) num() int     { return 100 + f.i }
func (f *fakeStrobe) Close() error { return nil }
func (f *fakeStrobe) write(v int) error {
	f.p.strobes[f.i] = v
	f.p.asserted[f.i] = v == 1
	return nil
}
func (f *fakeStrobe) read() (int, error) { return f.p.strobes[f.i], nil }
func (f *fakeStrobe) direction(v string) error {
	switch v {
	case "in":
		f.p.asserted[f.i] = false
	case "low":
		f.p.strobes[f.i], f.p.asserted[f.i] = 0, true
	case "high":
		f.p.strobes[f.i], f.p.asserted[f.i] = 1, true
	}
	return nil
}

type fakeSwitchin struct {
	p *fakePanel
	j int
}

func (f *fakeSwitchin) num() int               { return 200 + f.j }
func (f *fakeSwitchin) Close() error           { return nil }
func (f *fakeSwitchin) write(int) error        { return nil }
func (f *fakeSwitchin) direction(string) error { return nil }

func (f *fakeSwitchin) read() (int, error) {
	if f.j == 3 { // directly wired key: no strobe involved
		if f.p.down[keypadDirectIndex] {
			return 0, nil
		}
		return 1, nil
	}
	for i, asserted := range f.p.asserted {
		if asserted && f.p.down[i*4+f.j] {
			return 0, nil
		}
	}
	return 1, nil
}

func (p *fakePanel) matrix() *keypadMatrix {
	m := &keypadMatrix{activeLevel: 1}
	for i := range 3 {
		m.strobes = append(m.strobes, &fakeStrobe{p: p, i: i})
	}
	for j := range 4 {
		m.switchins = append(m.switchins, &fakeSwitchin{p: p, j: j})
	}
	return m
}

func (p *fakePanel) st20Matrix() *keypadMatrix {
	m := p.matrix()
	m.activeLevel = 0
	m.releaseToIn = true
	return m
}

// testScanner returns a scanner that has already seen the panel idle, which is what
// every test below is interested in. TestMatrixStaysQuietUntilThePanelReadsIdle covers
// the settling itself.
func testScanner(p *fakePanel) (*keypadScanner, *recordKeys) {
	a := &recordKeys{}
	s := newKeypadScanner(p.matrix(), a, defaultKeymap, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for range 3 {
		if err := s.tick(); err != nil {
			panic(err)
		}
	}
	return s, a
}

// sweep runs one full pass over all three strobes.
func sweep(t *testing.T, s *keypadScanner, n int) {
	t.Helper()
	for range n * 3 {
		if err := s.tick(); err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
}

// A held key reaches the speaker under the stock key map, and only after the strobe
// that key sits on has actually been driven.
func TestMatrixPressReachesTheMappedKey(t *testing.T) {
	p := newFakePanel()
	s, a := testScanner(p)

	// Scancode 2 is strobe 0 x switch-in 2 = POWER, an edge key: it acts on press.
	p.down[2] = true
	sweep(t, s, keypadDebounce+1)

	if got := a.presses("POWER"); got != 1 {
		t.Fatalf("POWER pressed %d times, want 1", got)
	}
}

// Every position in the stock map is reachable, and each reaches its own key. This is
// the test that would catch a transposed matrix — strobe*4+switchin the wrong way round
// silently swaps PRESET_3 with VOLUME_DOWN and PRESET_6 with POWER.
func TestMatrixGeometryMatchesTheStockKeymap(t *testing.T) {
	for code, want := range defaultKeymap {
		if want == "POWER" || want == "SOURCE_TOGGLE" {
			continue // handled by the dedicated tests below
		}
		t.Run(fmt.Sprintf("code%d", code), func(t *testing.T) {
			p := newFakePanel()
			s, a := testScanner(p)
			p.down[code] = true
			sweep(t, s, keypadDebounce+1)
			p.down[code] = false
			sweep(t, s, keypadDebounce+1)

			if got := strings.Join(a.got(), " "); got != "+"+want+" -"+want {
				t.Fatalf("scancode %d produced %s, want +%s -%s", code, got, want, want)
			}
		})
	}
}

// The fourth switch-in is not on the matrix: its key must register with no strobe
// asserted, and must not be attributed to whichever strobe happened to be driven.
func TestDirectKeyIsReadUnstrobed(t *testing.T) {
	p := newFakePanel()
	s, a := testScanner(p)

	p.down[keypadDirectIndex] = true
	sweep(t, s, keypadDebounce+1)

	if got := a.presses("SOURCE_TOGGLE"); got != 1 {
		t.Fatalf("SOURCE_TOGGLE pressed %d times, want 1", got)
	}
	for i, level := range p.strobes {
		if level != 0 {
			t.Fatalf("strobe %d left asserted after a sweep", i)
		}
	}
}

func TestST20MatrixAssertsLowAndReleasesToInput(t *testing.T) {
	p := newFakePanel()
	m := p.st20Matrix()
	p.down[2] = true

	reads, err := m.step(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reads[2].pressed {
		t.Fatal("active-low ST20 strobe did not expose pressed key")
	}
	for i, asserted := range p.asserted {
		if asserted {
			t.Fatalf("strobe %d still drives panel after sample", i)
		}
	}
}

func TestST20GeometryMatchesStockDTB(t *testing.T) {
	g := keypadGeometryFor(model.ST20)
	if fmt.Sprint(g.strobeOffsets) != "[28 20 17]" || fmt.Sprint(g.switchOffsets) != "[22 23 26 29]" {
		t.Fatalf("ST20 geometry = %v/%v", g.strobeOffsets, g.switchOffsets)
	}
	if g.strobeAddr != gpioBank0Addr || g.switchAddr != gpioBank0Addr || g.activeLevel != 0 || !g.releaseToIn {
		t.Fatalf("ST20 drive config = %+v", g)
	}
}

// Contact bounce — a single sweep's worth of a key appearing down — must not produce a
// press. Without this the volume ramp starts on electrical noise.
func TestMatrixDebouncesASingleSweepGlitch(t *testing.T) {
	p := newFakePanel()
	s, a := testScanner(p)

	p.down[2] = true
	sweep(t, s, 1)
	p.down[2] = false
	sweep(t, s, keypadDebounce+2)

	if got := a.got(); len(got) != 0 {
		t.Fatalf("glitch produced %v, want nothing", got)
	}
}

// Releasing has to debounce too, and has to arrive: a preset that never sees its
// release edge would store itself after the hold timer instead of playing.
func TestMatrixReportsReleaseAfterPress(t *testing.T) {
	p := newFakePanel()
	s, a := testScanner(p)

	p.down[6] = true // strobe 1 x switch-in 2 = PRESET_1
	sweep(t, s, keypadDebounce+1)
	if got := strings.Join(a.got(), " "); got != "+PRESET_1" {
		t.Fatalf("press produced %s, want +PRESET_1", got)
	}
	p.down[6] = false
	sweep(t, s, keypadDebounce+1)

	if got := strings.Join(a.got(), " "); got != "+PRESET_1 -PRESET_1" {
		t.Fatalf("release produced %s, want +PRESET_1 -PRESET_1", got)
	}
}

// Two keys on different strobes must not bleed into each other; each strobe only ever
// reports the keys wired under it.
func TestMatrixKeepsStrobesIndependent(t *testing.T) {
	p := newFakePanel()
	s, a := testScanner(p)

	p.down[0] = true // strobe 0 x switch-in 0 = VOLUME_UP
	sweep(t, s, keypadDebounce+1)
	p.down[0] = false
	sweep(t, s, keypadDebounce+1)

	if got := a.presses("VOLUME_DOWN"); got != 0 {
		t.Fatalf("VOLUME_DOWN fired %d times while VOLUME_UP was held", got)
	}
	if got := a.presses("VOLUME_UP"); got == 0 {
		t.Fatal("VOLUME_UP never fired")
	}
}

// A panel that reads pressed from the first sweep is a wiring or polarity problem, not
// ten keys held at once. It must produce nothing at all — otherwise a mis-muxed pad
// toggles standby and ramps the volume the moment soundtapd starts — and it must start
// working the instant the reading becomes plausible.
func TestMatrixStaysQuietUntilThePanelReadsIdle(t *testing.T) {
	p := newFakePanel()
	a := &recordKeys{}
	s := newKeypadScanner(p.matrix(), a, defaultKeymap, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for code := range defaultKeymap {
		p.down[code] = true
	}
	sweep(t, s, keypadSettleWarn+2)
	if got := a.got(); len(got) != 0 {
		t.Fatalf("stuck panel produced %v, want nothing", got)
	}

	for code := range defaultKeymap {
		p.down[code] = false
	}
	sweep(t, s, 1) // one idle sweep is enough to trust it
	p.down[2] = true
	sweep(t, s, keypadDebounce+1)

	if got := a.presses("POWER"); got != 1 {
		t.Fatalf("POWER pressed %d times after settling, want 1", got)
	}
}

// Stock accepts volume-down + preset-1 held through power-on; the panel never reads idle
// then, yet the pair must reach the sink.
func TestMatrixAcceptsFactoryResetChordHeldFromStartup(t *testing.T) {
	p := newFakePanel()
	p.down[4] = true // VOLUME_DOWN
	p.down[6] = true // PRESET_1
	a := &recordKeys{}
	s := newKeypadScanner(p.matrix(), a, defaultKeymap, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sweep(t, s, keypadDebounce)

	// Both keys reach the sink as held, so its factory-reset chord starts counting.
	if a.presses("PRESET_1") != 1 || a.presses("VOLUME_DOWN") != 1 || len(a.got()) != 2 {
		t.Fatalf("factory-reset chord held from startup produced %v", a.got())
	}
}

func TestGPIOBankBaseResolvesByAddress(t *testing.T) {
	root := t.TempDir()
	old := gpioRoot
	gpioRoot = root
	defer func() { gpioRoot = old }()

	// Mainline hands out bases in probe order, so the layout below — gpio2 first —
	// is the case that a hard-coded 0/32/64/96 assumption gets wrong.
	for _, c := range []struct {
		chip string
		dev  string
	}{
		{"gpiochip0", "481ac000.gpio"},
		{"gpiochip32", "44e07000.gpio"},
		{"gpiochip64", "4804c000.gpio"},
	} {
		dev := filepath.Join(root, "devices", c.dev)
		if err := os.MkdirAll(dev, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dev, filepath.Join(root, c.chip)); err != nil {
			t.Fatal(err)
		}
	}

	if base, err := gpioBankBase(gpioBank0Addr); err != nil || base != 32 {
		t.Fatalf("gpio0 base = %d, %v; want 32", base, err)
	}
	if base, err := gpioBankBase(gpioBank1Addr); err != nil || base != 64 {
		t.Fatalf("gpio1 base = %d, %v; want 64", base, err)
	}
	if _, err := gpioBankBase("deadbeef"); err == nil {
		t.Fatal("resolved a bank that is not there")
	}
}

func TestOpenGPIOLineReadsAndWritesValue(t *testing.T) {
	root := t.TempDir()
	old := gpioRoot
	gpioRoot = root
	defer func() { gpioRoot = old }()

	dir := filepath.Join(root, "gpio7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"direction", "value"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	g, err := openGPIOLine(7, "low")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()

	if got, _ := os.ReadFile(filepath.Join(dir, "direction")); string(got) != "low" {
		t.Fatalf("direction = %q, want %q", got, "low")
	}
	if v, err := g.read(); err != nil || v != 0 {
		t.Fatalf("read = %d, %v; want 0", v, err)
	}
	if err := g.write(1); err != nil {
		t.Fatalf("write: %v", err)
	}
	if v, err := g.read(); err != nil || v != 1 {
		t.Fatalf("read after write = %d, %v; want 1", v, err)
	}
}

// The matrix must stay off on anything that is not a confirmed ST10 panel, and the
// override has to work in both directions so it can be turned off on hardware.
func TestKeypadMatrixSupportedHonoursTheOverride(t *testing.T) {
	root := t.TempDir()
	old := gpioRoot
	gpioRoot = root
	defer func() { gpioRoot = old }()

	t.Setenv("BUTTON_MATRIX", "")
	if KeypadMatrixSupported(model.ST10) {
		t.Fatal("claimed support with no gpiochips present")
	}
	t.Setenv("BUTTON_MATRIX", "1")
	if !KeypadMatrixSupported(model.ST10) {
		t.Fatal("override did not force the matrix on")
	}
	t.Setenv("BUTTON_MATRIX", "0")
	if KeypadMatrixSupported(model.ST10) {
		t.Fatal("override did not force the matrix off")
	}
}

// Direction changes go through the file opened once, not a reopen per strobe.
func TestSysfsGPIODirectionUsesOpenFile(t *testing.T) {
	old := gpioRoot
	gpioRoot = t.TempDir()
	t.Cleanup(func() { gpioRoot = old })
	dir := filepath.Join(gpioRoot, "gpio7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"direction", "value"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("in"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	line, err := openGPIOLine(7, "in")
	if err != nil {
		t.Fatal(err)
	}
	defer line.Close()
	// Swapping the file out from under the line proves no reopen by path happens.
	if err := os.Rename(filepath.Join(dir, "direction"), filepath.Join(dir, "direction.open")); err != nil {
		t.Fatal(err)
	}
	if err := line.direction("low"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "direction.open"))
	if err != nil || string(got) != "low" {
		t.Fatalf("direction = %q, %v; want low", got, err)
	}
}
