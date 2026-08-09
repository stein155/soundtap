// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package hw

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// testSynth builds an IR synth on a fake clock, so the release grace period is
// decided by the test instead of by wall time.
func testSynth(km map[byte]string) (*irSynth, *recordKeys, *fakeClock) {
	a := &recordKeys{}
	clk := &fakeClock{t: time.Unix(1700000000, 0)}
	s := newIRSynth(a, km, irReleaseGrace, false, discardLog())
	s.now = clk.now
	return s, a, clk
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var testIRMap = map[byte]string{
	0x10: "POWER", 0x11: "VOLUME_UP", 0x12: "VOLUME_DOWN", 0x13: "PRESET_1",
}

// A code is a press; the driver's release marker releases it once the grace period
// has passed, and the grace period is what makes a 0x00 that the next repeat
// contradicts harmless.
func TestIRShortPressPlaysOnce(t *testing.T) {
	s, a, clk := testSynth(testIRMap)
	s.code(0x13)
	s.code(irReleaseCode)
	if got := strings.Join(a.got(), " "); got != "+PRESET_1" {
		t.Fatalf("edges = %s, want only the press until the grace period ends", got)
	}
	clk.advance(irReleaseGrace + time.Millisecond)
	s.tick()
	if got := strings.Join(a.got(), " "); got != "+PRESET_1 -PRESET_1" {
		t.Fatalf("edges = %s, want one press and its release", got)
	}
}

// A held key is a single code byte and then silence: the driver suppresses every
// repeat frame that matches its latch. Nothing may act until the 0x00 arrives.
func TestIRHoldStaysDownWhileTheDriverIsSilent(t *testing.T) {
	s, a, clk := testSynth(testIRMap)
	s.code(0x13)
	for range 20 { // a long hold, no bytes at all
		clk.advance(irReleaseGrace)
		s.tick()
	}
	if got := strings.Join(a.got(), " "); got != "+PRESET_1" {
		t.Fatalf("edges = %s, want the press only: the key is still down", got)
	}
	if s.heldName != "PRESET_1" {
		t.Fatalf("held = %q, want PRESET_1", s.heldName)
	}
}

// If the driver's state timer expires between two repeats of a held key — which
// depends on a kernel HZ we have not established — the 0x00 must not turn one hold
// into a burst of presses.
func TestIRPrematureReleaseIsAbsorbed(t *testing.T) {
	s, a, clk := testSynth(testIRMap)
	s.code(0x13)
	for range 5 {
		s.code(irReleaseCode)
		clk.advance(irReleaseGrace / 2) // the next repeat beats the grace period
		s.tick()
		s.code(0x13)
	}
	if got := strings.Join(a.got(), " "); got != "+PRESET_1" {
		t.Fatalf("edges = %s, want the press only: this is still one hold", got)
	}
	s.code(irReleaseCode)
	clk.advance(irReleaseGrace + time.Millisecond)
	s.tick()
	if got := strings.Join(a.got(), " "); got != "+PRESET_1 -PRESET_1" {
		t.Fatalf("edges = %s, want exactly one press out of five contradicted releases", got)
	}
}

// Two real presses are two presses.
func TestIRSeparatePressesAreSeparate(t *testing.T) {
	s, a, clk := testSynth(testIRMap)
	for range 2 {
		s.code(0x13)
		s.code(irReleaseCode)
		clk.advance(irReleaseGrace + time.Millisecond)
		s.tick()
	}
	if got := strings.Join(a.got(), " "); got != "+PRESET_1 -PRESET_1 +PRESET_1 -PRESET_1" {
		t.Fatalf("edges = %s, want two presses", got)
	}
}

// Volume is pressed on the code and stays held through contradicted releases, so the
// sink's ramp neither restarts nor stops early; the release ends it.
func TestIRVolumeActsOnPressAndRampsWhileHeld(t *testing.T) {
	s, a, clk := testSynth(testIRMap)
	s.code(0x11)
	if got := strings.Join(a.got(), " "); got != "+VOLUME_UP" {
		t.Fatalf("edges = %s, want the press at once", got)
	}
	// The sink's ramp owns the stepping from here; a contradicted release must not add
	// an edge of its own.
	for range 4 {
		s.code(irReleaseCode)
		s.code(0x11)
	}
	if got := strings.Join(a.got(), " "); got != "+VOLUME_UP" {
		t.Fatalf("edges while held = %s, want still the one press", got)
	}
	s.code(irReleaseCode)
	clk.advance(irReleaseGrace + time.Millisecond)
	s.tick()
	if got := strings.Join(a.got(), " "); got != "+VOLUME_UP -VOLUME_UP" {
		t.Fatalf("edges = %s, want the release that stops the ramp", got)
	}
}

// The driver injects 0x00 before a different key's code, but a switch must release
// the old key even without it — a stuck ramp is the worst failure here.
func TestIRNewCodeReleasesTheHeldOne(t *testing.T) {
	s, a, _ := testSynth(testIRMap)
	s.code(0x11) // volume up, held
	s.code(0x10) // power, with no release marker in between
	if got := strings.Join(a.got(), " "); got != "+VOLUME_UP -VOLUME_UP +POWER" {
		t.Fatalf("edges = %s, want volume released before power is pressed", got)
	}
	if s.heldName != "POWER" {
		t.Fatalf("held = %q, want POWER", s.heldName)
	}
}

// Holding a preset is one long press: the sink's hold-to-store timer sees no release
// until the remote goes quiet and the grace period ends.
func TestIRPresetHoldStores(t *testing.T) {
	s, a, clk := testSynth(testIRMap)
	s.code(0x13)
	// No further bytes: on this driver a hold really is silence.
	for range 20 {
		clk.advance(irReleaseGrace)
		s.tick()
	}
	if got := strings.Join(a.got(), " "); got != "+PRESET_1" {
		t.Fatalf("edges = %s, want the press held", got)
	}
	s.code(irReleaseCode)
	clk.advance(irReleaseGrace + time.Millisecond)
	s.tick()
	if got := strings.Join(a.got(), " "); got != "+PRESET_1 -PRESET_1" {
		t.Fatalf("edges = %s, want one press and its release", got)
	}
}

// A release marker with nothing held is not an event.
func TestIRReleaseWithNothingHeldIsInert(t *testing.T) {
	s, a, clk := testSynth(testIRMap)
	s.code(irReleaseCode)
	clk.advance(irReleaseGrace + time.Millisecond)
	s.tick()
	if got := a.got(); len(got) != 0 {
		t.Fatalf("a stray release acted: %v", got)
	}
}

// An unmapped code does nothing, and says so at most once per quiet window.
func TestIRUnknownCodeIsInertAndRateLimited(t *testing.T) {
	var buf strings.Builder
	a := &recordKeys{}
	clk := &fakeClock{t: time.Unix(1700000000, 0)}
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := newIRSynth(a, testIRMap, irReleaseGrace, false, log)
	s.now = clk.now

	for range 50 {
		s.code(0x7e)
		clk.advance(time.Second)
	}
	if got := a.got(); len(got) != 0 {
		t.Fatalf("an unmapped code acted: %v", got)
	}
	if n := strings.Count(buf.String(), "unmapped ir code"); n != 1 {
		t.Fatalf("unmapped code logged %d times in %s, want 1", n, irUnknownQuiet)
	}
	// Past the quiet window it is worth saying again.
	clk.advance(irUnknownQuiet)
	s.code(0x7e)
	if n := strings.Count(buf.String(), "unmapped ir code"); n != 2 {
		t.Fatalf("logged %d times after the quiet window, want 2", n)
	}
}

// Learn mode reports every byte and drives nothing: it is the capture pass that
// produces ir.map. The release markers are reported as such, because where they
// fall is what says whether this remote's holds survive.
func TestIRLearnModeReportsAndDoesNotAct(t *testing.T) {
	var buf strings.Builder
	a := &recordKeys{}
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s := newIRSynth(a, testIRMap, irReleaseGrace, true, log)
	s.code(0x11)
	s.code(0x7e)
	s.code(irReleaseCode)
	if got := a.got(); len(got) != 0 {
		t.Fatalf("learn mode acted: %v", got)
	}
	out := buf.String()
	if n := strings.Count(out, "ir learn"); n != 3 {
		t.Fatalf("learn lines = %d, want 3", n)
	}
	if !strings.Contains(out, "0x7e") || !strings.Contains(out, "0x11") {
		t.Fatalf("learn output lacks the codes: %s", out)
	}
	if !strings.Contains(out, "release") {
		t.Fatalf("learn output does not name the release marker: %s", out)
	}
}

// reset is what the read loop calls when the device disappears mid-press: no
// release marker is coming, so nothing may stay held.
func TestIRResetDropsTheHeldButton(t *testing.T) {
	s, a, clk := testSynth(testIRMap)
	s.code(0x12) // volume down, held
	s.reset()
	if s.heldName != "" {
		t.Fatalf("still held after reset: %q", s.heldName)
	}
	if a.resets != 1 {
		t.Fatalf("sink reset %d times, want 1: its ramp would survive", a.resets)
	}
	s.code(irReleaseCode)
	clk.advance(irReleaseGrace + time.Millisecond)
	s.tick()
	if got := strings.Join(a.got(), " "); got != "+VOLUME_DOWN" {
		t.Fatalf("edges = %s: reset ends the gesture, it does not replay or release it", got)
	}
}

func TestParseIRCode(t *testing.T) {
	cases := []struct {
		in   string
		want byte
		ok   bool
	}{
		{"0x1a", 0x1a, true}, {"0XFF", 0xff, true},
		{"26", 26, true}, {"255", 255, true}, {"1", 1, true},
		// 0x00 is the driver's release marker and can never arrive as a key, so a
		// map line claiming it is a misreading of a capture.
		{"0x00", 0, false}, {"0", 0, false},
		{"256", 0, false}, {"0x100", 0, false}, {"-1", 0, false},
		{"1a", 0, false}, {"", 0, false},
	}
	for _, c := range cases {
		got, err := parseIRCode(c.in)
		if (err == nil) != c.ok || (c.ok && got != c.want) {
			t.Errorf("parseIRCode(%q) = (%#x, %v); want (%#x, ok=%v)", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestLoadIRKeymap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ir.map")
	body := "# my remote\n" +
		"0x10 = POWER\n" +
		"17=VOLUME_UP\n" +
		"  0x12  =  volume_down  \n" + // case and spacing are the user's business
		"0x13 = MUTE\n" + // a real speaker action since applyKey learned to mute
		"0x14 = NEXT_TRACK\n" +
		"0x15 = FLY_TO_THE_MOON\n" + // no such speaker action: refused, not bound to nothing
		"0x00 = POWER\n" + // the release marker is not a key
		"garbage\n" +
		"0x999 = POWER\n" +
		"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	km := LoadIRKeymap(path, discardLog())
	want := map[byte]string{0x10: "POWER", 0x11: "VOLUME_UP", 0x12: "VOLUME_DOWN", 0x13: "MUTE", 0x14: "NEXT_TRACK"}
	if len(km) != len(want) {
		t.Fatalf("keymap = %v, want %v", km, want)
	}
	for c, a := range want {
		if km[c] != a {
			t.Errorf("code %#x = %q, want %q", c, km[c], a)
		}
	}
	// A missing map is the shipping default, not a failure.
	if km := LoadIRKeymap(filepath.Join(dir, "absent"), discardLog()); len(km) != 0 {
		t.Fatalf("missing file gave %v, want an empty map", km)
	}
}

func TestStockIRKeymapAndPersistentOverride(t *testing.T) {
	stock := filepath.Join("..", "..", "..", "os", "overlay", "etc", "soundtap", "ir.map")
	km := LoadIRKeymap(stock, discardLog())
	want := map[byte]string{
		0xCD: "POWER", 0x3F: "VOLUME_UP", 0xBF: "VOLUME_DOWN",
		0x1F: "PRESET_1", 0xEF: "PRESET_2", 0x6F: "PRESET_3",
		0xAF: "PRESET_4", 0x2F: "PRESET_5", 0xCF: "PRESET_6",
		0xE7: "PREV_TRACK", 0x67: "NEXT_TRACK", 0x27: "PLAY_PAUSE", 0x0F: "AUX_INPUT",
	}
	if len(km) != len(want) {
		t.Fatalf("stock keymap = %v, want %v", km, want)
	}
	for code, action := range want {
		if km[code] != action {
			t.Errorf("stock code %#x = %q, want %q", code, km[code], action)
		}
	}

	persistent := filepath.Join(t.TempDir(), "ir.map")
	if got := irMapPath("", persistent, stock); got != stock {
		t.Fatalf("missing persistent map selected %q, want stock %q", got, stock)
	}
	if err := os.WriteFile(persistent, []byte("0x10 = POWER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := irMapPath("", persistent, stock); got != persistent {
		t.Fatalf("persistent map selected %q, want %q", got, persistent)
	}
	if got := irMapPath("/custom/map", persistent, stock); got != "/custom/map" {
		t.Fatalf("configured map selected %q", got)
	}
}

// scriptedIR replays a canned code stream, then reports idle forever.
type scriptedIR struct {
	codes []byte
	// closed counts Close calls, so the loop's cleanup is observable.
	closed int
}

func (s *scriptedIR) next(time.Duration) (byte, bool, error) {
	if len(s.codes) == 0 {
		return 0, false, nil
	}
	c := s.codes[0]
	s.codes = s.codes[1:]
	return c, true, nil
}

func (s *scriptedIR) Close() error { s.closed++; return nil }

// run pumps the device's bytes and lets the grace period expire on its own.
// Idling is the device's job — next() is asked for a bounded wait rather than the
// loop reading in a circle — so this fake reports idle immediately.
func TestIRRunConsumesCodesAndStopsOnContext(t *testing.T) {
	s, a, _ := testSynth(testIRMap)
	s.now = time.Now // real clock: the release must fire on its own here
	dev := &scriptedIR{codes: []byte{0x10, irReleaseCode}}
	ctx, cancel := context.WithTimeout(context.Background(), 4*irReleaseGrace)
	defer cancel()
	s.run(ctx, dev)
	if got := strings.Join(a.got(), " "); got != "+POWER -POWER" {
		t.Fatalf("edges = %s, want one press and its release", got)
	}
	if s.heldName != "" {
		t.Fatalf("still held after the release marker: %q", s.heldName)
	}
	if dev.closed != 0 {
		t.Fatalf("run closed the device (%d times); the caller owns that", dev.closed)
	}
}

// A device that errors ends the pump so the caller can reopen it, instead of
// looping on a dead fd.
func TestIRRunReturnsOnDeviceError(t *testing.T) {
	s, _, _ := testSynth(testIRMap)
	done := make(chan struct{})
	go func() {
		s.run(context.Background(), errorIR{})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return on a device error")
	}
}

type errorIR struct{}

func (errorIR) next(time.Duration) (byte, bool, error) { return 0, false, os.ErrClosed }
func (errorIR) Close() error                           { return nil }

// A model with no receiver sets IRDev to "". Before this was treated as disabled the
// empty path reached the open loop and was retried for about half a minute at every
// boot on hardware that has nothing to open — the Wireless Link, for one.
func TestIRDeviceIsNoneWhenTheModelHasNoReceiver(t *testing.T) {
	for _, dev := range []string{"", "off", "-"} {
		t.Run("dev="+dev, func(t *testing.T) {
			t.Setenv("IR_DEVICE", dev)
			if got := IRDevice(""); got != "" {
				t.Fatalf("IRDevice = %q for a model with no IR receiver, want none", got)
			}
		})
	}
}

// boseFrame builds the durations of one frame: header, sixteen bits of code and its
// complement least significant first, stop mark.
func boseFrame(code byte, mark, zero, one uint32) []uint32 {
	d := []uint32{1060, 1450}
	bits := uint16(code) | uint16(^code)<<8
	for i := range 16 {
		sp := zero
		if bits&(1<<i) != 0 {
			sp = one
		}
		d = append(d, mark, sp)
	}
	return append(d, mark)
}

func TestDecodeBoseFrame(t *testing.T) {
	for _, c := range []byte{0x01, 0x4c, 0xcd, 0xfe} {
		got, ok := decodeBoseFrame(boseFrame(c, 534, 468, 1468))
		if !ok || got != c {
			t.Fatalf("code %#x: got %#x ok=%v", c, got, ok)
		}
		// No stop mark: the last space ran into the gap.
		f := boseFrame(c, 534, 468, 1468)
		if got, ok := decodeBoseFrame(f[:len(f)-1]); !ok || got != c {
			t.Fatalf("code %#x without stop mark: got %#x ok=%v", c, got, ok)
		}
		// A receiver that stretches every edge still decodes.
		if got, ok := decodeBoseFrame(boseFrame(c, 700, 600, 1700)); !ok || got != c {
			t.Fatalf("code %#x stretched: got %#x ok=%v", c, got, ok)
		}
	}
	bad := boseFrame(0x4c, 534, 468, 1468)
	bad[5] = 1468 // flip one bit: the complement check has to catch it
	if _, ok := decodeBoseFrame(bad); ok {
		t.Fatal("a frame failing the complement check decoded")
	}
	if _, ok := decodeBoseFrame([]uint32{500, 500, 500}); ok {
		t.Fatal("a fragment decoded")
	}
}

func TestLircFramerPressAndRelease(t *testing.T) {
	var f lircFramer
	now := time.Unix(0, 0)
	for range 3 { // a held key retransmits
		for _, d := range boseFrame(0x4c, 534, 468, 1468) {
			f.sample(lircKindPulse|d, now)
		}
		f.sample(lircKindSpace|40000, now) // the gap before the next frame
		now = now.Add(100 * time.Millisecond)
	}
	for i := range 3 {
		if c, ok := f.pop(); !ok || c != 0x4c {
			t.Fatalf("frame %d: got %#x ok=%v", i, c, ok)
		}
	}
	f.idle(now) // 100 ms after the last frame: still held
	if c, ok := f.pop(); ok {
		t.Fatalf("released too early: %#x", c)
	}
	f.idle(now.Add(200 * time.Millisecond))
	if c, ok := f.pop(); !ok || c != irReleaseCode {
		t.Fatalf("no release: got %#x ok=%v", c, ok)
	}
}

func TestIRResolveSettlesOnTheMapsReading(t *testing.T) {
	km := map[byte]string{0xcd: "POWER", 0x3f: "VOLUME_UP"}
	s := newIRSynth(&recordKeys{}, km, 0, false, discardLog())
	// 0xb3 reversed is 0xcd: the first press settles "reversed"...
	if got := s.resolve(0xb3); got != 0xcd {
		t.Fatalf("got %#x", got)
	}
	// ...and every later code is read the same way.
	if got := s.resolve(reverseBits(0x3f)); got != 0x3f {
		t.Fatalf("got %#x", got)
	}
	for _, k := range []byte{0xcd, 0x3f, 0xbf, 0x1f, 0xef, 0x6f, 0xaf, 0x2f, 0xcf, 0xe7, 0x67, 0x27, 0x0f} {
		if reverseBits(reverseBits(k)) != k {
			t.Fatalf("reverseBits is not an involution at %#x", k)
		}
	}
}
