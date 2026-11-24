// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package stm32amp talks to the SoundTouch SA-5's amplifier controller, an STM32 at 0x1d on
// the module's I2C1, as stock's snd-burnspa driver and APServer do
// (docs/hardware/sa5/README.md).
//
// A write is one I2C message [reg, value...], little endian, no checksum. A read sets the
// read pointer with [0x00, reg], then reads the register's size in a separate message (a
// STOP between, not a repeated start), 200 us after each step, up to five rounds.
//
// Stock writes two registers and reads the rest: i2sClocksValid (0x06) and sourceSelect
// (0x11). Write refuses every other register. The STM32's own bootloader at 0x1e, its update
// flag and every register stock never writes are out of reach: a half-run flash leaves the
// amplifier without firmware.
package stm32amp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
)

// Address is the controller's 7-bit I2C address. 0x1e is its bootloader: never used.
const Address = 0x1d

// Registers (stock snd-burnspa's register table).
const (
	RegVersion            = 0x01 // 64-byte string
	RegAmpState           = 0x04 // 1 = the amplifier is on
	RegI2SClocksValid     = 0x06 // the host's McASP clocks run (1) or are about to stop (0)
	RegThermalAttenuation = 0x10 // IEEE-754 single: the gain the host must apply
	RegSourceSelect       = 0x11 // analogue input, '1'..'3'
	RegInterruptStatus    = 0x12 // bit 0 amp state changed, bit 4 amp fault
)

const (
	interruptFault = 0x10
	stepDelay      = 200 * time.Microsecond
	maxRounds      = 5
	ampPoll        = 100 * time.Millisecond
	thermalPoll    = time.Second
)

// writable are the registers stock writes; Write refuses all others.
var writable = map[byte]int{RegI2SClocksValid: 1, RegSourceSelect: 1}

// sizes are the registers this package reads.
var sizes = map[byte]int{RegVersion: 64, RegAmpState: 1, RegI2SClocksValid: 1,
	RegThermalAttenuation: 4, RegSourceSelect: 1, RegInterruptStatus: 1}

// ErrNotWritable is a write to a register stock never writes.
var ErrNotWritable = errors.New("stm32amp: register not writable")

// Bus is the I2C device at Address: one message per call.
type Bus interface {
	Write(p []byte) error
	Read(p []byte) error
}

// Amp is the controller on a Bus.
type Amp struct {
	bus   Bus
	mu    sync.Mutex
	sleep func(time.Duration)
}

// New is the controller on bus.
func New(bus Bus) *Amp { return &Amp{bus: bus, sleep: time.Sleep} }

// Read is register reg, its table size long.
func (a *Amp) Read(reg byte) ([]byte, error) {
	n, ok := sizes[reg]
	if !ok {
		return nil, fmt.Errorf("stm32amp: register 0x%02x unknown", reg)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	buf := make([]byte, n)
	var err error
	for range maxRounds {
		a.sleep(stepDelay)
		if err = a.bus.Write([]byte{0x00, reg}); err != nil {
			continue
		}
		a.sleep(stepDelay)
		if err = a.bus.Read(buf); err == nil {
			return buf, nil
		}
	}
	return nil, fmt.Errorf("stm32amp: reading 0x%02x: %w", reg, err)
}

// Write sets one of the registers stock writes.
func (a *Amp) Write(reg byte, v byte) error {
	if _, ok := writable[reg]; !ok {
		return fmt.Errorf("%w: 0x%02x", ErrNotWritable, reg)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	err := a.bus.Write([]byte{reg, v})
	a.sleep(stepDelay)
	return err
}

// Version is the controller's firmware version string.
func (a *Amp) Version() (string, error) {
	b, err := a.Read(RegVersion)
	if err != nil {
		return "", err
	}
	for i, c := range b {
		if c == 0 || c == 0xff {
			b = b[:i]
			break
		}
	}
	return string(b), nil
}

// On reports whether the amplifier says it is on (ampState 1).
func (a *Amp) On() (bool, error) {
	b, err := a.Read(RegAmpState)
	return err == nil && b[0] == 1, err
}

// Faulted reports whether the interrupt status has the fault bit.
func (a *Amp) Faulted() (bool, error) {
	b, err := a.Read(RegInterruptStatus)
	return err == nil && b[0]&interruptFault != 0, err
}

// Attenuation is the thermal gain the controller asks for, clamped to 0..1 (1: none).
func (a *Amp) Attenuation() (float64, error) {
	b, err := a.Read(RegThermalAttenuation)
	if err != nil {
		return 1, err
	}
	g := float64(math.Float32frombits(uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24))
	if math.IsNaN(g) || g > 1 || g == 0 && b[0]|b[1]|b[2]|b[3] == 0 {
		// An unset register reads 0: no attenuation rather than silence.
		return 1, nil
	}
	return math.Max(g, 0), nil
}

// Supervisor tells the controller the clocks run, then watches its state, fault and thermal
// attenuation as stock's APServer does: ampState every 100 ms, the attenuation every second.
type Supervisor struct {
	Amp *Amp
	// OnFault is called once when the controller reports a fault (the caller mutes).
	OnFault func()
	// OnAttenuation is called with each new thermal gain.
	OnAttenuation func(g float64)
	Log           *slog.Logger

	mu      sync.Mutex
	on      bool
	onSince time.Time
	fault   bool
	thermal float64
	now     func() time.Time
}

// unmuteSettle is how long stock waits after ampState first reads on before it unmutes.
const unmuteSettle = 100 * time.Millisecond

// MayUnmute reports whether the amplifier has been on for stock's settle time and has no
// fault: stock's APServer unmutes 100 ms after ampState reads 1, never before.
func (s *Supervisor) MayUnmute() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on && !s.fault && s.clock()().Sub(s.onSince) >= unmuteSettle
}

func (s *Supervisor) clock() func() time.Time {
	if s.now != nil {
		return s.now
	}
	return time.Now
}

// State is the controller as last read.
func (s *Supervisor) State() (on, fault bool, thermal float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on, s.fault, s.thermal
}

// Run supervises until ctx ends, then tells the controller the clocks are about to stop.
func (s *Supervisor) Run(ctx context.Context) {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	if v, err := s.Amp.Version(); err == nil {
		s.Log.Info("SA-5 amplifier controller", "version", v)
	} else {
		s.Log.Warn("SA-5 amplifier controller does not answer", "err", err)
	}
	if err := s.Amp.Write(RegI2SClocksValid, 1); err != nil {
		s.Log.Warn("SA-5: i2sClocksValid", "err", err)
	}
	defer func() {
		if err := s.Amp.Write(RegI2SClocksValid, 0); err != nil {
			s.Log.Warn("SA-5: i2sClocksValid off", "err", err)
		}
	}()
	s.mu.Lock()
	s.thermal = 1
	s.mu.Unlock()
	amp := time.NewTicker(ampPoll)
	defer amp.Stop()
	lastThermal := time.Time{}
	for {
		s.Step(time.Since(lastThermal) >= thermalPoll)
		if time.Since(lastThermal) >= thermalPoll {
			lastThermal = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-amp.C:
		}
	}
}

// Step reads the state once, and the attenuation when thermal. Exported for tests.
func (s *Supervisor) Step(thermal bool) {
	on, _ := s.Amp.On()
	fault, err := s.Amp.Faulted()
	s.mu.Lock()
	newFault := err == nil && fault && !s.fault
	if on && !s.on {
		s.onSince = s.clock()()
	}
	s.on = on
	if newFault {
		s.fault = true
	}
	s.mu.Unlock()
	if newFault {
		s.Log.Error("SA-5 amplifier fault")
		if s.OnFault != nil {
			s.OnFault()
		}
	}
	if !thermal {
		return
	}
	g, err := s.Amp.Attenuation()
	if err != nil {
		return
	}
	s.mu.Lock()
	changed := g != s.thermal
	s.thermal = g
	s.mu.Unlock()
	if changed && s.OnAttenuation != nil {
		s.OnAttenuation(g)
	}
}
