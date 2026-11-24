// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package battery watches the SoundTouch Portable's battery pack and its charger, as stock's
// BatteryMonitor does (docs/hardware/portable/README.md).
//
// The pack is a smart battery at SMBus address 0x0b on the SCM's I2C0 (stock taigan_scm.dtb
// batmon@0b, no compatible: stock reads it from userspace over /dev/i2c-0). Words are little
// endian, without PEC. The charger has no interface of its own on the host: one GPIO says DC
// is present (gpio 27, low when it is) and one enables charging (gpio 104, active level in
// /mnt/nv/charger_active_level).
//
// The charge enable follows stock's rules: on with DC present until the pack says it is full,
// off when the pack faults, cannot be read or is over-voltage. SHIP mode, a block write that
// cuts the speaker's power until DC comes back, is off unless the owner allowed it: stock
// ships the pack on a pack fault, a critically discharged pack on battery and DC above 13 V.
// Nothing here writes any other gauge register: not BatteryMode (0x03), not
// ManufacturerAccess (0x00), not PEC.
package battery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Gauge registers stock reads (SBS command codes, plus Bose's 0x36 fault code).
const (
	RegTemperature = 0x08 // 0.1 K
	RegVoltage     = 0x09 // mV
	RegCurrent     = 0x0a // mA, signed
	RegRSOC        = 0x0d // %
	RegStatus      = 0x16 // SBS BatteryStatus
	RegMfgName     = 0x20 // block: the pack's "personality"
	RegFault       = 0x36 // Bose: non-zero is a fault the pack reports

	// ShipCommand with ShipPayload is stock's SHIP request, a block write.
	ShipCommand = 0x30
)

// ShipPayload is the four bytes stock writes with ShipCommand.
var ShipPayload = []byte("BOSE")

// BatteryStatus bits stock acts on.
const (
	statusFullyCharged = 0x20
	statusDischarging  = 0x40
	statusErrorMask    = 0x0f
)

// Thresholds from stock's BatteryMonitor.
const (
	overVoltageMV    = 13000 // with DC present, for overVoltageHold: ship
	overVoltageHold  = time.Second
	criticalRSOC     = 1  // % and below on battery: CriticallyDischarged
	lowRSOC          = 20 // % and below on battery: the low-battery warning
	pollInterval     = time.Second
	readTries        = 4
	readRetryBackoff = 5 * time.Millisecond
	makerRetry       = 5 * time.Second
)

// Personalities are the pack makers stock knows; with any other name it does not monitor.
var Personalities = []string{"BOSE_ICC", "BOSE_SANYO"}

// ErrUnknownPack says the pack's manufacturer name is none of Personalities.
var ErrUnknownPack = errors.New("battery: unknown pack")

// Bus is the pack's SMBus, at address 0x0b.
type Bus interface {
	ReadWord(cmd byte) (uint16, error)
	ReadBlock(cmd byte) ([]byte, error)
	WriteBlock(cmd byte, data []byte) error
}

// Line is one GPIO line: Read for DC present, Write for the charge enable.
type Line interface {
	Read() (int, error)
	Write(v int) error
}

// Status is the pack as last read, for /api/health and the panel.
type Status struct {
	Present    bool    `json:"present"`
	Maker      string  `json:"maker,omitempty"`
	Level      int     `json:"level"`      // RSOC, %
	MilliVolts int     `json:"milliVolts"` // pack voltage
	MilliAmps  int     `json:"milliAmps"`  // positive charging, negative discharging
	Celsius    float64 `json:"celsius"`    // pack temperature
	OnDC       bool    `json:"onDC"`       // the charger is plugged in
	Charging   bool    `json:"charging"`   // the charge enable line is on
	Full       bool    `json:"full"`       // the pack says FullyCharged
	Low        bool    `json:"low"`        // on battery at or below 20 %
	Fault      uint16  `json:"fault"`      // the pack's fault code, 0 for none
}

// Config wires a Monitor to its hardware.
type Config struct {
	Bus       Bus
	DCPresent Line // low while DC is present
	ChargeEn  Line
	// ChargeActiveHigh is the charge enable's active level (/mnt/nv/charger_active_level).
	ChargeActiveHigh bool
	// AllowShip lets the Monitor send the pack to SHIP mode as stock does; soundtapd always
	// sets it. Off (tests), it only logs.
	AllowShip bool
	Log       *slog.Logger
	// Now and Sleep are for tests.
	Now   func() time.Time
	Sleep func(time.Duration)
}

// Monitor polls the pack once a second and applies stock's charge rules when allowed.
type Monitor struct {
	cfg       Config
	mu        sync.Mutex
	status    Status
	overSince time.Time
	shipped   bool
}

// New is a Monitor for cfg.
func New(cfg Config) *Monitor {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = time.Sleep
	}
	return &Monitor{cfg: cfg}
}

// Status is the last reading.
func (m *Monitor) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Run polls until ctx ends. It returns ErrUnknownPack, as stock stops, when the pack names
// no known maker; the charge enable is left off then.
func (m *Monitor) Run(ctx context.Context) error {
	maker, err := m.maker()
	// A read that fails is tried again; only a pack that names another maker stops it.
	for err != nil && !errors.Is(err, ErrUnknownPack) {
		m.charge(false)
		m.cfg.Log.Warn("battery: no answer from the pack yet", "err", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(makerRetry):
		}
		maker, err = m.maker()
	}
	if err != nil {
		m.charge(false)
		return err
	}
	m.cfg.Log.Info("battery pack", "maker", maker, "ship", m.cfg.AllowShip)
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		m.Step(maker)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (m *Monitor) maker() (string, error) {
	var raw []byte
	err := m.retry(func() (e error) { raw, e = m.cfg.Bus.ReadBlock(RegMfgName); return })
	if err != nil {
		return "", fmt.Errorf("battery: reading the maker: %w", err)
	}
	name := strings.TrimRight(string(raw), "\x00 ")
	for _, p := range Personalities {
		if name == p {
			return name, nil
		}
	}
	return name, fmt.Errorf("%w %q", ErrUnknownPack, name)
}

// blockPayload is the data of an SMBus block read taken as plain bytes off the wire: the
// pack's count, then that many bytes. A count past what was read is cut to it.
func blockPayload(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	n := min(int(raw[0]), len(raw)-1)
	return append([]byte(nil), raw[1:1+n]...)
}

// Step reads the pack once and applies the rules. Exported for tests.
func (m *Monitor) Step(maker string) {
	s := Status{Maker: maker}
	dcKnown := false
	if v, err := m.cfg.DCPresent.Read(); err == nil {
		s.OnDC, dcKnown = v == 0, true
	}
	var temp, volt, cur, rsoc, status, fault uint16
	err := errors.Join(
		m.word(RegTemperature, &temp), m.word(RegVoltage, &volt), m.word(RegCurrent, &cur),
		m.word(RegRSOC, &rsoc), m.word(RegStatus, &status))
	// Bose's fault code is a vendor register: a pack without it still reports its errors in
	// the status word, so a failed read is no fault code rather than no pack.
	if m.word(RegFault, &fault) != nil {
		fault = 0
	}
	if err != nil {
		// Stock turns the charger off when the pack cannot be read; it never charges blind.
		m.cfg.Log.Warn("battery: pack unreadable, charger off", "err", err)
		s.Charging = m.charge(false)
		m.overSince = time.Time{}
		m.set(s)
		return
	}
	s.Present = true
	s.Celsius = float64(temp)/10 - 273.15
	s.MilliVolts = int(volt)
	s.MilliAmps = int(int16(cur))
	s.Level = int(rsoc)
	s.Full = status&statusFullyCharged != 0
	s.Fault = fault
	if status&statusErrorMask != 0 && s.Fault == 0 {
		s.Fault = status & statusErrorMask
	}
	s.Low = !s.OnDC && s.Level <= lowRSOC
	m.apply(&s, fault != 0, dcKnown)
	m.set(s)
}

// apply is stock's charge and SHIP rules for one reading. Only Bose's own fault code
// (packFault) ships the pack; an error bit in the status word alone stops charging, since a
// transient one would otherwise cut the power until DC returns. A reading whose DC line could
// not be read never ships for low charge: the speaker may well be on DC.
func (m *Monitor) apply(s *Status, packFault, dcKnown bool) {
	now := m.cfg.Now()
	if !s.OnDC || s.MilliVolts <= overVoltageMV {
		// The one second is of over-voltage without a break: a reading from minutes ago
		// must not make the next single high reading ship the pack.
		m.overSince = time.Time{}
	}
	switch {
	case packFault:
		s.Charging = m.charge(false)
		m.ship(fmt.Sprintf("pack fault 0x%04x", s.Fault))
	case s.Fault != 0:
		s.Charging = m.charge(false) // the status word's error bits: no charging, no SHIP
	case s.OnDC && s.MilliVolts > overVoltageMV:
		s.Charging = m.charge(false)
		if m.overSince.IsZero() {
			m.overSince = now
		} else if now.Sub(m.overSince) >= overVoltageHold {
			m.ship(fmt.Sprintf("%d mV with DC present", s.MilliVolts))
		}
	case dcKnown && !s.OnDC && s.Level <= criticalRSOC:
		s.Charging = m.charge(false)
		m.ship(fmt.Sprintf("critically discharged at %d %%", s.Level))
	default:
		s.Charging = m.charge(s.OnDC && !s.Full)
	}
}

func (m *Monitor) set(s Status) {
	m.mu.Lock()
	m.status = s
	m.mu.Unlock()
}

func (m *Monitor) word(cmd byte, out *uint16) error {
	return m.retry(func() (e error) { *out, e = m.cfg.Bus.ReadWord(cmd); return })
}

// retry is stock's: up to four tries, 5 ms apart.
func (m *Monitor) retry(f func() error) error {
	var err error
	for range readTries {
		if err = f(); err == nil {
			return nil
		}
		m.cfg.Sleep(readRetryBackoff)
	}
	return err
}

// charge drives the charge enable and reports whether charging is on.
func (m *Monitor) charge(on bool) bool {
	if m.cfg.ChargeEn == nil {
		return false
	}
	v := 0
	if on == m.cfg.ChargeActiveHigh {
		v = 1
	}
	if err := m.cfg.ChargeEn.Write(v); err != nil {
		m.cfg.Log.Warn("battery: charge enable", "err", err)
		return false
	}
	return on
}

// ship sends the pack to SHIP mode once, with the charger off, when the owner allowed it.
// Otherwise it only logs what stock would have done.
func (m *Monitor) ship(why string) {
	if m.shipped {
		return
	}
	if !m.cfg.AllowShip {
		m.cfg.Log.Warn("battery: stock would ship the pack now; SHIP is not allowed", "why", why)
		return
	}
	m.cfg.Log.Warn("battery: shipping the pack", "why", why)
	if err := m.cfg.Bus.WriteBlock(ShipCommand, ShipPayload); err != nil {
		m.cfg.Log.Error("battery: SHIP write", "err", err)
		return
	}
	m.shipped = true
}
