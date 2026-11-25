// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package stm32amp

import (
	"errors"
	"log/slog"
	"math"
	"sync"
	"testing"
	"time"
)

var errNack = errors.New("nack")

// fakeBus is amp controller: [0x00, reg] sets the read pointer, [reg, v] writes.
type fakeBus struct {
	regs   map[byte][]byte
	ptr    byte
	writes [][]byte
	fail   int
}

func (b *fakeBus) Write(msg []byte) error {
	if b.fail > 0 {
		b.fail--

		return errNack
	}

	if msg[0] == 0x00 {
		b.ptr = msg[1]

		return nil
	}

	b.writes = append(b.writes, append([]byte(nil), msg...))
	b.regs[msg[0]] = append([]byte(nil), msg[1:]...)

	return nil
}

func (b *fakeBus) Read(p []byte) error {
	copy(p, b.regs[b.ptr])

	return nil
}

func newAmp(regs map[byte][]byte) (*Amp, *fakeBus) {
	bus := &fakeBus{regs: regs, ptr: 0, writes: nil, fail: 0}
	amp := New(bus)
	amp.sleep = func(time.Duration) {}

	return amp, bus
}

func float(g float32) []byte {
	u := math.Float32bits(g)

	return []byte{byte(u), byte(u >> 8), byte(u >> 16), byte(u >> 24)}
}

func TestReadSetsThePointerFirst(t *testing.T) {
	t.Parallel()

	amp, _ := newAmp(map[byte][]byte{RegVersion: []byte("1.0.2.182\x00")})

	v, err := amp.Version()
	if err != nil || v != "1.0.2.182" {
		t.Fatalf("Version = %q, %v", v, err)
	}
}

func TestReadRetries(t *testing.T) {
	t.Parallel()

	amp, bus := newAmp(map[byte][]byte{RegAmpState: {1}})
	bus.fail = 3

	ampOn, err := amp.On()
	if err != nil || !ampOn {
		t.Fatalf("On = %v, %v after three NACKs", ampOn, err)
	}

	bus.fail = 5

	_, err = amp.On()
	if err == nil {
		t.Fatal("five NACKs did not fail")
	}
}

func TestOnlyStocksRegistersAreWritable(t *testing.T) {
	t.Parallel()

	amp, bus := newAmp(map[byte][]byte{})
	for _, reg := range []byte{0x04, 0x05, 0x10, 0x12, 0x14, 0x15, 0x20, 0x7f} {
		err := amp.Write(reg, 1)
		if !errors.Is(err, ErrNotWritable) {
			t.Fatalf("Write(0x%02x) = %v", reg, err)
		}
	}

	if len(bus.writes) != 0 {
		t.Fatalf("refused writes reached the bus: %v", bus.writes)
	}

	err := amp.Write(RegI2SClocksValid, 1)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAttenuation(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		raw  []byte
		want float64
	}{{float(0.5), 0.5}, {float(1), 1}, {float(1.7), 1}, {[]byte{0, 0, 0, 0}, 1}, {float(-0.2), 0}} {
		amp, _ := newAmp(map[byte][]byte{RegThermalAttenuation: testCase.raw})

		gain, _ := amp.Attenuation()
		if math.Abs(gain-testCase.want) > 1e-6 {
			t.Fatalf("attenuation %v = %v, want %v", testCase.raw, gain, testCase.want)
		}
	}
}

func TestUnmuteWaitsForTheAmplifier(t *testing.T) {
	t.Parallel()

	amp, bus := newAmp(map[byte][]byte{RegAmpState: {0}, RegInterruptStatus: {0}})
	now := time.Unix(0, 0)
	sup := &Supervisor{
		Amp: amp, OnFault: nil, OnAttenuation: nil, Log: slog.New(slog.DiscardHandler),
		mu: sync.Mutex{}, on: false, onSince: time.Time{}, fault: false, thermal: 0,
		now: func() time.Time { return now },
	}
	sup.Step()

	if sup.MayUnmute() {
		t.Fatal("unmute allowed while the amplifier is off")
	}

	bus.regs[RegAmpState] = []byte{1}

	sup.Step()

	if sup.MayUnmute() {
		t.Fatal("unmute allowed the moment the amplifier came on")
	}

	now = now.Add(100 * time.Millisecond)

	if !sup.MayUnmute() {
		t.Fatal("unmute refused 100 ms after the amplifier came on")
	}

	bus.regs[RegInterruptStatus] = []byte{0x10}

	sup.Step()

	if sup.MayUnmute() {
		t.Fatal("unmute allowed with amp fault")
	}
}

func TestSupervisorFaultAndThermal(t *testing.T) {
	t.Parallel()

	amp, _ := newAmp(map[byte][]byte{RegAmpState: {1}, RegInterruptStatus: {0}, RegThermalAttenuation: float(1)})
	faults, gains := 0, []float64{}
	sup := &Supervisor{
		Amp: amp, OnFault: func() { faults++ }, OnAttenuation: func(gain float64) { gains = append(gains, gain) },
		Log: slog.New(slog.DiscardHandler),
		mu:  sync.Mutex{}, on: false, onSince: time.Time{}, fault: false, thermal: 1, now: nil,
	}
	sup.Step()
	sup.StepThermal()

	ampOn, fault, _ := sup.State()
	if !ampOn || fault || faults != 0 || len(gains) != 0 {
		t.Fatalf("quiet controller: on %v fault %v faults %d gains %v", ampOn, fault, faults, gains)
	}

	amp.bus.(*fakeBus).regs[RegInterruptStatus] = []byte{0x11}
	amp.bus.(*fakeBus).regs[RegThermalAttenuation] = float(0.25)

	sup.Step()
	sup.StepThermal()
	sup.Step()
	sup.StepThermal()

	if faults != 1 || len(gains) != 1 || gains[0] != 0.25 {
		t.Fatalf("faults %d gains %v, want one fault and one 0.25", faults, gains)
	}
}
