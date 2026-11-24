// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package stm32amp

import (
	"errors"
	"io"
	"log/slog"
	"math"
	"testing"
	"time"
)

// fakeBus is a controller: [0x00, reg] sets the read pointer, [reg, v] writes.
type fakeBus struct {
	regs   map[byte][]byte
	ptr    byte
	writes [][]byte
	fail   int
}

func (b *fakeBus) Write(p []byte) error {
	if b.fail > 0 {
		b.fail--
		return errors.New("nack")
	}
	if p[0] == 0x00 {
		b.ptr = p[1]
		return nil
	}
	b.writes = append(b.writes, append([]byte(nil), p...))
	b.regs[p[0]] = append([]byte(nil), p[1:]...)
	return nil
}

func (b *fakeBus) Read(p []byte) error { copy(p, b.regs[b.ptr]); return nil }

func newAmp(regs map[byte][]byte) (*Amp, *fakeBus) {
	bus := &fakeBus{regs: regs}
	a := New(bus)
	a.sleep = func(time.Duration) {}
	return a, bus
}

func float(g float32) []byte {
	u := math.Float32bits(g)
	return []byte{byte(u), byte(u >> 8), byte(u >> 16), byte(u >> 24)}
}

func TestReadSetsThePointerFirst(t *testing.T) {
	a, _ := newAmp(map[byte][]byte{RegVersion: []byte("1.0.2.182\x00")})
	v, err := a.Version()
	if err != nil || v != "1.0.2.182" {
		t.Fatalf("Version = %q, %v", v, err)
	}
}

func TestReadRetries(t *testing.T) {
	a, bus := newAmp(map[byte][]byte{RegAmpState: {1}})
	bus.fail = 3
	if on, err := a.On(); err != nil || !on {
		t.Fatalf("On = %v, %v after three NACKs", on, err)
	}
	bus.fail = 5
	if _, err := a.On(); err == nil {
		t.Fatal("five NACKs did not fail")
	}
}

func TestOnlyStocksRegistersAreWritable(t *testing.T) {
	a, bus := newAmp(map[byte][]byte{})
	for _, reg := range []byte{0x04, 0x05, 0x10, 0x12, 0x14, 0x15, 0x20, 0x7f} {
		if err := a.Write(reg, 1); !errors.Is(err, ErrNotWritable) {
			t.Fatalf("Write(0x%02x) = %v", reg, err)
		}
	}
	if len(bus.writes) != 0 {
		t.Fatalf("refused writes reached the bus: %v", bus.writes)
	}
	if err := a.Write(RegI2SClocksValid, 1); err != nil {
		t.Fatal(err)
	}
}

func TestAttenuation(t *testing.T) {
	for _, c := range []struct {
		raw  []byte
		want float64
	}{{float(0.5), 0.5}, {float(1), 1}, {float(1.7), 1}, {[]byte{0, 0, 0, 0}, 1}, {float(-0.2), 0}} {
		a, _ := newAmp(map[byte][]byte{RegThermalAttenuation: c.raw})
		if g, _ := a.Attenuation(); math.Abs(g-c.want) > 1e-6 {
			t.Fatalf("attenuation %v = %v, want %v", c.raw, g, c.want)
		}
	}
}

func TestUnmuteWaitsForTheAmplifier(t *testing.T) {
	a, bus := newAmp(map[byte][]byte{RegAmpState: {0}, RegInterruptStatus: {0}})
	now := time.Unix(0, 0)
	s := &Supervisor{Amp: a, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), now: func() time.Time { return now }}
	s.Step(false)
	if s.MayUnmute() {
		t.Fatal("unmute allowed while the amplifier is off")
	}
	bus.regs[RegAmpState] = []byte{1}
	s.Step(false)
	if s.MayUnmute() {
		t.Fatal("unmute allowed the moment the amplifier came on")
	}
	now = now.Add(100 * time.Millisecond)
	if !s.MayUnmute() {
		t.Fatal("unmute refused 100 ms after the amplifier came on")
	}
	bus.regs[RegInterruptStatus] = []byte{0x10}
	s.Step(false)
	if s.MayUnmute() {
		t.Fatal("unmute allowed with a fault")
	}
}

func TestSupervisorFaultAndThermal(t *testing.T) {
	a, _ := newAmp(map[byte][]byte{RegAmpState: {1}, RegInterruptStatus: {0}, RegThermalAttenuation: float(1)})
	faults, gains := 0, []float64{}
	s := &Supervisor{Amp: a, OnFault: func() { faults++ }, OnAttenuation: func(g float64) { gains = append(gains, g) },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), thermal: 1}
	s.Step(true)
	if on, fault, _ := s.State(); !on || fault || faults != 0 || len(gains) != 0 {
		t.Fatalf("quiet controller: on %v fault %v faults %d gains %v", on, fault, faults, gains)
	}
	a.bus.(*fakeBus).regs[RegInterruptStatus] = []byte{0x11}
	a.bus.(*fakeBus).regs[RegThermalAttenuation] = float(0.25)
	s.Step(true)
	s.Step(true)
	if faults != 1 || len(gains) != 1 || gains[0] != 0.25 {
		t.Fatalf("faults %d gains %v, want one fault and one 0.25", faults, gains)
	}
}
