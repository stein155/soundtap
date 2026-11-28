// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package oled

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

var errBusFault = errors.New("bus fault")

// fakeBus records every line change, write and sleep in order.
type fakeBus struct {
	events []string
	// failWrite fails that write (1-based); 0 never.
	failWrite int
	writes    int
	written   int
}

func (b *fakeBus) setDC(level gpioLevel) error {
	b.events = append(b.events, "dc="+string(level))

	return nil
}

func (b *fakeBus) setCE(level gpioLevel) error {
	b.events = append(b.events, "ce="+string(level))

	return nil
}

func (b *fakeBus) write(buf []byte) (int, error) {
	b.writes++
	if b.writes == b.failWrite {
		b.events = append(b.events, "write fail")

		return 0, errBusFault
	}

	b.events = append(b.events, fmt.Sprintf("write %d", len(buf)))
	b.written += len(buf)

	return len(buf), nil
}

func testWire(bus *fakeBus, timing *lineTiming) (*wire, *time.Duration, *int) {
	var (
		slept time.Duration
		warns int
	)

	out := newWire(bus, timing)
	out.sleep = func(d time.Duration) {
		slept += d
		bus.events = append(bus.events, "sleep "+d.String())
	}
	out.warn = func(error) { warns++ }

	return out, &slept, &warns
}

// A frame's command, data and display-on share one chip-enable cycle: only D/C moves
// between them, and every line change has lineSettle around it.
func TestWireOneChipEnableCycle(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{events: nil, failWrite: 0, writes: 0, written: 0}
	out, _, _ := testWire(bus, &lineTiming{slow: false})

	err := out.send([]wireStep{
		{data: false, buf: []byte{cmdColumns, 0, 1, cmdRows, 0, 1}},
		{data: true, buf: []byte{1, 2, 3, 4}},
		{data: false, buf: []byte{cmdOn}},
	})
	if err != nil {
		t.Fatal(err)
	}

	settle := "sleep " + lineSettle.String()

	want := []string{
		"dc=0", settle, "ce=0", settle, "write 6", settle,
		"dc=1", settle, "write 4", settle,
		"dc=0", settle, "write 1", settle,
		"ce=1", settle,
	}
	if !slices.Equal(bus.events, want) {
		t.Fatalf("events\n got %q\nwant %q", bus.events, want)
	}

	// D/C is still low: the next frame's command goes without driving it again.
	bus.events = nil

	err = out.send([]wireStep{{data: false, buf: []byte{cmdContrast, 0x40}}})
	if err != nil {
		t.Fatal(err)
	}

	want = []string{"ce=0", settle, "write 2", settle, "ce=1", settle}
	if !slices.Equal(bus.events, want) {
		t.Fatalf("events\n got %q\nwant %q", bus.events, want)
	}
}

// A full frame goes in spidev-sized writes inside one chip-enable cycle, and its line
// settling fits the panel's 50 ms tick many times over.
func TestWireFullFrameBudget(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{events: nil, failWrite: 0, writes: 0, written: 0}
	out, slept, _ := testWire(bus, &lineTiming{slow: false})

	state := panelState{latest: nil, contrast: FullContrast, on: false}

	err := out.send(drawSteps(&state, make([]byte, Width*Height), FullContrast))
	if err != nil {
		t.Fatal(err)
	}

	if bus.written != 6+Width*Height/2+1 {
		t.Fatalf("wrote %d bytes", bus.written)
	}

	cycles := 0

	for _, e := range bus.events {
		if e == "ce=0" {
			cycles++
		}
	}

	if cycles != 1 || !slices.Contains(bus.events, "write 4096") || !slices.Contains(bus.events, "write 2304") {
		t.Fatalf("events %q: want one chip-enable cycle with writes of 4096 and 2304", bus.events)
	}

	// 6407 bytes at 2 MHz are 25.6 ms; with the settling a full frame stays under the
	// 50 ms paint tick (the panel's 20 frames a second) with room for the syscalls.
	wire := time.Duration(bus.written) * 8 * time.Second / 2_000_000 // spidev at 2 MHz
	if *slept > 2*time.Millisecond || wire+*slept > 30*time.Millisecond {
		t.Fatalf("settling %v and %v on the wire for a full frame", *slept, wire)
	}
}

// A failed write lets chip enable go, says so once, and from then on every step is sent
// the legacy way: its own D/C and chip-enable cycle, 10 ms around each change.
func TestWireFallsBackAfterFailure(t *testing.T) {
	t.Parallel()

	bus := &fakeBus{events: nil, failWrite: 2, writes: 0, written: 0}
	timing := &lineTiming{slow: false}
	out, _, warns := testWire(bus, timing)

	steps := []wireStep{{data: false, buf: []byte{cmdContrast, 1}}, {data: true, buf: []byte{9, 9}}}

	err := out.send(steps)
	if !errors.Is(err, errBusFault) {
		t.Fatalf("err = %v, want the bus fault", err)
	}

	if last := bus.events[len(bus.events)-1]; last != "ce=1" {
		t.Fatalf("chip enable not released after the failure: %q", bus.events)
	}

	if !timing.slow || *warns != 1 {
		t.Fatalf("slow = %v, warnings = %d; want the fallback and one warning", timing.slow, *warns)
	}

	bus.events = nil

	err = out.send(steps)
	if err != nil {
		t.Fatal(err)
	}

	settle := "sleep " + legacyLineSettle.String()

	want := []string{
		"dc=0", settle, "ce=0", settle, "write 2", settle, "ce=1", settle,
		"dc=1", settle, "ce=0", settle, "write 2", settle, "ce=1", settle,
	}
	if !slices.Equal(bus.events, want) {
		t.Fatalf("legacy events\n got %q\nwant %q", bus.events, want)
	}

	// Another failure in the slow mode stays quiet.
	bus.failWrite = bus.writes + 1

	err = out.send(steps)
	if err == nil || *warns != 1 {
		t.Fatalf("err = %v, warnings = %d; want a failure and still one warning", err, *warns)
	}
}

// The fallback is kept with the Framebuffer's timing, so a panel opened again after a
// failure starts slow.
func TestWireFallbackOutlivesTheWire(t *testing.T) {
	t.Parallel()

	timing := &lineTiming{slow: false}
	first, _, _ := testWire(&fakeBus{events: nil, failWrite: 1, writes: 0, written: 0}, timing)

	err := first.send([]wireStep{{data: false, buf: oledInit()}})
	if err == nil {
		t.Fatal("want the failure")
	}

	bus := &fakeBus{events: nil, failWrite: 0, writes: 0, written: 0}
	second, slept, warns := testWire(bus, timing)

	err = second.send([]wireStep{{data: false, buf: oledInit()}})
	if err != nil {
		t.Fatal(err)
	}

	if *slept != 4*legacyLineSettle || *warns != 0 {
		t.Fatalf("slept %v, warnings %d; want the legacy 40 ms and no new warning", *slept, *warns)
	}
}

// A short write is a failure, not a frame half sent in silence.
func TestWireShortWrite(t *testing.T) {
	t.Parallel()

	out, _, _ := testWire(&fakeBus{events: nil, failWrite: 0, writes: 0, written: 0}, &lineTiming{slow: false})
	out.bus = shortBus{}

	err := out.send([]wireStep{{data: true, buf: []byte{1, 2, 3}}})
	if !errors.Is(err, errShortWrite) {
		t.Fatalf("err = %v, want a short write", err)
	}

	if out.dc != "" {
		t.Fatalf("dc = %q after a failure, want unknown", out.dc)
	}
}

type shortBus struct{}

func (shortBus) setDC(gpioLevel) error { return nil }

func (shortBus) setCE(gpioLevel) error { return nil }

func (shortBus) write(buf []byte) (int, error) { return len(buf) - 1, nil }
