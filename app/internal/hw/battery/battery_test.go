// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package battery

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeBus struct {
	words  map[byte]uint16
	name   string
	fail   bool
	writes [][]byte
}

func (b *fakeBus) ReadWord(cmd byte) (uint16, error) {
	if b.fail {
		return 0, errors.New("nack")
	}
	return b.words[cmd], nil
}

func (b *fakeBus) ReadBlock(cmd byte) ([]byte, error) {
	if cmd != RegMfgName {
		return nil, errors.New("unexpected block read")
	}
	return []byte(b.name), nil
}

func (b *fakeBus) WriteBlock(cmd byte, data []byte) error {
	b.writes = append(b.writes, append([]byte{cmd}, data...))
	return nil
}

type fakeLine struct{ v, writes int }

func (l *fakeLine) Read() (int, error) { return l.v, nil }
func (l *fakeLine) Write(v int) error  { l.v = v; l.writes++; return nil }

func pack(volt, rsoc, status, fault uint16) *fakeBus {
	return &fakeBus{name: "BOSE_ICC", words: map[byte]uint16{
		RegTemperature: 2982, RegVoltage: volt, RegCurrent: 0xff9c, RegRSOC: rsoc,
		RegStatus: status, RegFault: fault,
	}}
}

func monitor(bus Bus, dc, en *fakeLine, ship bool) *Monitor {
	return New(Config{Bus: bus, DCPresent: dc, ChargeEn: en, ChargeActiveHigh: true, AllowShip: ship,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Sleep: func(time.Duration) {}})
}

func TestReadsThePack(t *testing.T) {
	m := monitor(pack(11800, 64, statusDischarging, 0), &fakeLine{v: 1}, &fakeLine{}, false)
	m.Step("BOSE_ICC")
	s := m.Status()
	if !s.Present || s.Level != 64 || s.MilliVolts != 11800 || s.MilliAmps != -100 || s.OnDC {
		t.Fatalf("status %+v", s)
	}
	if s.Celsius < 25 || s.Celsius > 25.1 {
		t.Fatalf("temperature %.2f, want 25.05", s.Celsius)
	}
}

func TestWithoutShipPermissionTheGaugeIsNeverWritten(t *testing.T) {
	bus, en := pack(11800, 1, statusDischarging, 0x0101), &fakeLine{v: 1}
	m := monitor(bus, &fakeLine{v: 0}, en, false)
	m.Step("BOSE_ICC")
	if len(bus.writes) != 0 {
		t.Fatalf("wrote the gauge without permission: %v", bus.writes)
	}
	if en.v != 0 {
		t.Fatal("charging with a pack fault")
	}
}

func TestChargesOnDCUntilFull(t *testing.T) {
	en := &fakeLine{}
	m := monitor(pack(12100, 80, 0, 0), &fakeLine{v: 0}, en, true)
	m.Step("BOSE_ICC")
	if !m.Status().Charging || en.v != 1 {
		t.Fatalf("not charging on DC: %+v line %d", m.Status(), en.v)
	}
	m.cfg.Bus = pack(12600, 100, statusFullyCharged, 0)
	m.Step("BOSE_ICC")
	if m.Status().Charging || en.v != 0 {
		t.Fatalf("still charging when full: line %d", en.v)
	}
}

func TestActiveLowChargeEnable(t *testing.T) {
	en := &fakeLine{v: 1}
	m := monitor(pack(12100, 80, 0, 0), &fakeLine{v: 0}, en, true)
	m.cfg.ChargeActiveHigh = false
	m.Step("BOSE_ICC")
	if en.v != 0 {
		t.Fatalf("active-low enable written %d, want 0", en.v)
	}
}

func TestAFaultStopsChargingAndShips(t *testing.T) {
	bus, en := pack(12000, 50, 0, 0x0042), &fakeLine{v: 1}
	m := monitor(bus, &fakeLine{v: 0}, en, true)
	m.Step("BOSE_ICC")
	m.Step("BOSE_ICC")
	if en.v != 0 {
		t.Fatal("charging with a pack fault")
	}
	want := append([]byte{ShipCommand}, ShipPayload...)
	if len(bus.writes) != 1 || !bytes.Equal(bus.writes[0], want) {
		t.Fatalf("SHIP writes %v, want one %v", bus.writes, want)
	}
}

func TestCriticalOnBatteryShipsButNotOnDC(t *testing.T) {
	bus := pack(10000, 1, statusDischarging, 0)
	m := monitor(bus, &fakeLine{v: 0}, &fakeLine{}, true) // DC present
	m.Step("BOSE_ICC")
	if len(bus.writes) != 0 {
		t.Fatal("shipped on DC")
	}
	m.cfg.DCPresent = &fakeLine{v: 1}
	m.Step("BOSE_ICC")
	if len(bus.writes) != 1 {
		t.Fatal("did not ship a critically discharged pack on battery")
	}
}

func TestOverVoltageShipsAfterOneSecond(t *testing.T) {
	bus := pack(13200, 90, 0, 0)
	now := time.Unix(0, 0)
	m := monitor(bus, &fakeLine{v: 0}, &fakeLine{}, true)
	m.cfg.Now = func() time.Time { return now }
	m.Step("BOSE_ICC")
	if len(bus.writes) != 0 {
		t.Fatal("shipped at the first over-voltage reading")
	}
	now = now.Add(time.Second)
	m.Step("BOSE_ICC")
	if len(bus.writes) != 1 {
		t.Fatal("did not ship after a second above 13 V")
	}
}

func TestUnreadablePackTurnsTheChargerOff(t *testing.T) {
	bus, en := pack(0, 0, 0, 0), &fakeLine{v: 1}
	bus.fail = true
	m := monitor(bus, &fakeLine{v: 0}, en, true)
	m.Step("BOSE_ICC")
	if en.v != 0 || m.Status().Present || len(bus.writes) != 0 {
		t.Fatalf("unreadable pack: line %d status %+v writes %v", en.v, m.Status(), bus.writes)
	}
}

func TestUnknownPackIsNotMonitored(t *testing.T) {
	bus, en := pack(12000, 50, 0, 0), &fakeLine{v: 1}
	bus.name = "ACME"
	m := monitor(bus, &fakeLine{v: 0}, en, true)
	if err := m.Run(t.Context()); !errors.Is(err, ErrUnknownPack) {
		t.Fatalf("Run = %v, want ErrUnknownPack", err)
	}
	if en.v != 0 {
		t.Fatal("charge enable left on for an unknown pack")
	}
}

// noFault is a pack without Bose's vendor fault register.
type noFault struct{ *fakeBus }

func (b noFault) ReadWord(cmd byte) (uint16, error) {
	if cmd == RegFault {
		return 0, errors.New("nack")
	}
	return b.fakeBus.ReadWord(cmd)
}

func TestAPackWithoutTheFaultRegisterStillCharges(t *testing.T) {
	en := &fakeLine{}
	m := monitor(noFault{pack(12000, 60, 0, 0)}, &fakeLine{v: 0}, en, false)
	m.Step("BOSE_SANYO")
	if s := m.Status(); !s.Present || !s.Charging || en.v != 1 {
		t.Fatalf("status %+v, line %d", s, en.v)
	}
}

func TestBlockPayload(t *testing.T) {
	raw := append([]byte{10}, "BOSE_SANYO\xff\xff\xff"...)
	if got := string(blockPayload(raw)); got != "BOSE_SANYO" {
		t.Fatalf("payload = %q", got)
	}
	if got := blockPayload([]byte{0xff, 'a', 'b'}); string(got) != "ab" {
		t.Fatalf("a count past the read = %q", got)
	}
	if got := blockPayload(nil); got != nil {
		t.Fatalf("nothing read = %q", got)
	}
}

// The second above 13 V is one without a break: an over-voltage reading before the pack went
// silent, or before DC was pulled, does not make the next single one ship the pack.
func TestOverVoltageSecondStartsAgainAfterABreak(t *testing.T) {
	bus, dc := pack(13200, 90, 0, 0), &fakeLine{v: 0}
	now := time.Unix(0, 0)
	m := monitor(bus, dc, &fakeLine{}, true)
	m.cfg.Now = func() time.Time { return now }
	m.Step("BOSE_ICC")
	bus.fail = true
	now = now.Add(time.Minute)
	m.Step("BOSE_ICC")
	bus.fail = false
	now = now.Add(time.Minute)
	m.Step("BOSE_ICC")
	dc.v = 1
	now = now.Add(time.Minute)
	m.Step("BOSE_ICC")
	dc.v = 0
	now = now.Add(time.Minute)
	m.Step("BOSE_ICC")
	if len(bus.writes) != 0 {
		t.Fatalf("shipped on single over-voltage readings minutes apart: %v", bus.writes)
	}
	now = now.Add(time.Second)
	m.Step("BOSE_ICC")
	if len(bus.writes) != 1 {
		t.Fatal("did not ship after a second above 13 V")
	}
}

// An error bit in the status word alone stops charging but never ships: a transient one
// would cut the power until DC returns. Only Bose's fault code ships.
func TestAStatusErrorAloneDoesNotShip(t *testing.T) {
	bus, en := pack(12000, 50, 0x0003, 0), &fakeLine{v: 1}
	m := monitor(bus, &fakeLine{v: 0}, en, true)
	m.Step("BOSE_ICC")
	if en.v != 0 {
		t.Fatal("charging with an error in the status word")
	}
	if len(bus.writes) != 0 {
		t.Fatalf("shipped on a status error alone: %v", bus.writes)
	}
}

type brokenLine struct{}

func (brokenLine) Read() (int, error) { return 0, errors.New("gpio gone") }
func (brokenLine) Write(int) error    { return nil }

// A DC line that cannot be read says nothing about the power: low charge never ships then.
func TestAnUnreadableDCLineNeverShipsForLowCharge(t *testing.T) {
	bus := pack(10000, 1, statusDischarging, 0)
	m := New(Config{Bus: bus, DCPresent: brokenLine{}, ChargeEn: &fakeLine{}, AllowShip: true,
		Log: m0().cfg.Log, Now: m0().cfg.Now, Sleep: m0().cfg.Sleep})
	m.Step("BOSE_ICC")
	if len(bus.writes) != 0 {
		t.Fatal("shipped with the DC line unreadable")
	}
}

func m0() *Monitor { return monitor(pack(0, 0, 0, 0), &fakeLine{}, &fakeLine{}, true) }
