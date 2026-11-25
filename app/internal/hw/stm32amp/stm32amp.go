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
	"encoding/binary"
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

// Register sizes in bytes (stock snd-burnspa's register table).
const (
	versionLen     = 64 // RegVersion: the version string
	attenuationLen = 4  // RegThermalAttenuation: an IEEE-754 single
	byteLen        = 1  // every other register
)

// writable reports whether stock writes reg; Write refuses all others.
func writable(reg byte) bool {
	return reg == RegI2SClocksValid || reg == RegSourceSelect
}

// size is the length of a register this package reads, false for any other.
func size(reg byte) (int, bool) {
	switch reg {
	case RegVersion:
		return versionLen, true
	case RegThermalAttenuation:
		return attenuationLen, true
	case RegAmpState, RegI2SClocksValid, RegSourceSelect, RegInterruptStatus:
		return byteLen, true
	default:
		return 0, false
	}
}

// ErrNotWritable is a write to a register stock never writes.
var ErrNotWritable = errors.New("stm32amp: register not writable")

// errUnknownRegister is a read of a register outside the register table.
var errUnknownRegister = errors.New("stm32amp: register unknown")

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
func New(bus Bus) *Amp { return &Amp{bus: bus, mu: sync.Mutex{}, sleep: time.Sleep} }

// Read is register reg, its table size long.
func (a *Amp) Read(reg byte) ([]byte, error) {
	length, ok := size(reg)
	if !ok {
		return nil, fmt.Errorf("%w: 0x%02x", errUnknownRegister, reg)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	buf := make([]byte, length)

	var err error

	for range maxRounds {
		a.sleep(stepDelay)

		err = a.bus.Write([]byte{0x00, reg})
		if err != nil {
			continue
		}

		a.sleep(stepDelay)

		err = a.bus.Read(buf)
		if err == nil {
			return buf, nil
		}
	}

	return nil, fmt.Errorf("stm32amp: reading 0x%02x: %w", reg, err)
}

// Write sets one of the registers stock writes.
func (a *Amp) Write(reg, value byte) error {
	if !writable(reg) {
		return fmt.Errorf("%w: 0x%02x", ErrNotWritable, reg)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	err := a.bus.Write([]byte{reg, value})
	a.sleep(stepDelay)

	if err != nil {
		return fmt.Errorf("stm32amp: writing 0x%02x: %w", reg, err)
	}

	return nil
}

// Version is the controller's firmware version string.
func (a *Amp) Version() (string, error) {
	raw, err := a.Read(RegVersion)
	if err != nil {
		return "", err
	}

	for i, c := range raw {
		if c == 0 || c == 0xff {
			raw = raw[:i]

			break
		}
	}

	return string(raw), nil
}

// On reports whether the amplifier says it is on (ampState 1).
func (a *Amp) On() (bool, error) {
	raw, err := a.Read(RegAmpState)

	return err == nil && raw[0] == 1, err
}

// Faulted reports whether the interrupt status has the fault bit.
func (a *Amp) Faulted() (bool, error) {
	raw, err := a.Read(RegInterruptStatus)

	return err == nil && raw[0]&interruptFault != 0, err
}

// Attenuation is the thermal gain the controller asks for, clamped to 0..1 (1: none).
func (a *Amp) Attenuation() (float64, error) {
	raw, err := a.Read(RegThermalAttenuation)
	if err != nil {
		return 1, err
	}

	gain := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw)))
	if math.IsNaN(gain) || gain > 1 || gain == 0 && raw[0]|raw[1]|raw[2]|raw[3] == 0 {
		// An unset register reads 0: no attenuation rather than silence.
		return 1, nil
	}

	return math.Max(gain, 0), nil
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

// State is the controller as last read: whether the amplifier is on, whether it faulted and
// the thermal gain.
func (s *Supervisor) State() (bool, bool, float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.on, s.fault, s.thermal
}

// Run supervises until ctx ends, then tells the controller the clocks are about to stop.
func (s *Supervisor) Run(ctx context.Context) {
	if s.Log == nil {
		s.Log = slog.Default()
	}

	version, err := s.Amp.Version()
	if err == nil {
		s.Log.Info("SA-5 amplifier controller", "version", version)
	} else {
		s.Log.Warn("SA-5 amplifier controller does not answer", "err", err)
	}

	err = s.Amp.Write(RegI2SClocksValid, 1)
	if err != nil {
		s.Log.Warn("SA-5: i2sClocksValid", "err", err)
	}
	defer func() {
		err := s.Amp.Write(RegI2SClocksValid, 0)
		if err != nil {
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
		s.Step()

		if time.Since(lastThermal) >= thermalPoll {
			s.StepThermal()

			lastThermal = time.Now()
		}

		select {
		case <-ctx.Done():
			return
		case <-amp.C:
		}
	}
}

// Step reads the amplifier's state and fault once. Exported for tests.
func (s *Supervisor) Step() {
	ampOn, _ := s.Amp.On()
	fault, err := s.Amp.Faulted()
	s.mu.Lock()

	newFault := err == nil && fault && !s.fault
	if ampOn && !s.on {
		s.onSince = s.clock()()
	}

	s.on = ampOn
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
}

// StepThermal reads the thermal attenuation once. Exported for tests.
func (s *Supervisor) StepThermal() {
	gain, err := s.Amp.Attenuation()
	if err != nil {
		return
	}

	s.mu.Lock()
	changed := gain != s.thermal
	s.thermal = gain
	s.mu.Unlock()

	if changed && s.OnAttenuation != nil {
		s.OnAttenuation(gain)
	}
}

func (s *Supervisor) clock() func() time.Time {
	if s.now != nil {
		return s.now
	}

	return time.Now
}
