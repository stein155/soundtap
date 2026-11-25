// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package battery

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"
	"time"
)

var (
	errNack            = errors.New("nack")
	errUnexpectedBlock = errors.New("unexpected block read")
	errGPIOGone        = errors.New("gpio gone")
)

type fakeBus struct {
	words  map[byte]uint16
	name   string
	fail   bool
	writes [][]byte
}

func (b *fakeBus) ReadWord(cmd byte) (uint16, error) {
	if b.fail {
		return 0, errNack
	}

	return b.words[cmd], nil
}

func (b *fakeBus) ReadBlock(cmd byte) ([]byte, error) {
	if cmd != RegMfgName {
		return nil, errUnexpectedBlock
	}

	return []byte(b.name), nil
}

func (b *fakeBus) WriteBlock(cmd byte, data []byte) error {
	b.writes = append(b.writes, append([]byte{cmd}, data...))

	return nil
}

type fakeLine struct{ v, writes int }

func (l *fakeLine) Read() (int, error) { return l.v, nil }
func (l *fakeLine) Write(v int) error {
	l.v = v
	l.writes++

	return nil
}

func pack(volt, rsoc, status, fault uint16) *fakeBus {
	return &fakeBus{name: "BOSE_ICC", fail: false, writes: nil, words: map[byte]uint16{
		RegTemperature: 2982, RegVoltage: volt, RegCurrent: 0xff9c, RegRSOC: rsoc,
		RegStatus: status, RegFault: fault,
	}}
}

// line is a GPIO line that reads level.
func line(level int) *fakeLine { return &fakeLine{v: level, writes: 0} }

func monitor(bus Bus, dcLine, enable *fakeLine, ship bool) *Monitor {
	return New(Config{
		Bus: bus, DCPresent: dcLine, ChargeEn: enable, ChargeActiveHigh: true, AllowShip: ship,
		Log: slog.New(slog.DiscardHandler), Now: nil, Sleep: func(time.Duration) {},
	})
}

func TestReadsThePack(t *testing.T) {
	t.Parallel()

	mon := monitor(pack(11800, 64, statusDischarging, 0), line(1), line(0), false)
	mon.Step("BOSE_ICC")

	status := mon.Status()
	if !status.Present || status.Level != 64 || status.MilliVolts != 11800 || status.MilliAmps != -100 ||
		status.OnDC {
		t.Fatalf("status %+v", status)
	}

	if status.Celsius < 25 || status.Celsius > 25.1 {
		t.Fatalf("temperature %.2f, want 25.05", status.Celsius)
	}
}

func TestWithoutShipPermissionTheGaugeIsNeverWritten(t *testing.T) {
	t.Parallel()

	bus, enable := pack(11800, 1, statusDischarging, 0x0101), line(1)
	mon := monitor(bus, line(0), enable, false)
	mon.Step("BOSE_ICC")

	if len(bus.writes) != 0 {
		t.Fatalf("wrote the gauge without permission: %v", bus.writes)
	}

	if enable.v != 0 {
		t.Fatal("charging with a pack fault")
	}
}

func TestChargesOnDCUntilFull(t *testing.T) {
	t.Parallel()

	enable := line(0)
	mon := monitor(pack(12100, 80, 0, 0), line(0), enable, true)
	mon.Step("BOSE_ICC")

	if !mon.Status().Charging || enable.v != 1 {
		t.Fatalf("not charging on DC: %+v line %d", mon.Status(), enable.v)
	}

	mon.cfg.Bus = pack(12600, 100, statusFullyCharged, 0)
	mon.Step("BOSE_ICC")

	if mon.Status().Charging || enable.v != 0 {
		t.Fatalf("still charging when full: line %d", enable.v)
	}
}

func TestActiveLowChargeEnable(t *testing.T) {
	t.Parallel()

	enable := line(1)
	mon := monitor(pack(12100, 80, 0, 0), line(0), enable, true)
	mon.cfg.ChargeActiveHigh = false
	mon.Step("BOSE_ICC")

	if enable.v != 0 {
		t.Fatalf("active-low enable written %d, want 0", enable.v)
	}
}

func TestAFaultStopsChargingAndShips(t *testing.T) {
	t.Parallel()

	bus, enable := pack(12000, 50, 0, 0x0042), line(1)
	mon := monitor(bus, line(0), enable, true)
	mon.Step("BOSE_ICC")
	mon.Step("BOSE_ICC")

	if enable.v != 0 {
		t.Fatal("charging with a pack fault")
	}

	want := append([]byte{ShipCommand}, ShipPayload...)
	if len(bus.writes) != 1 || !bytes.Equal(bus.writes[0], want) {
		t.Fatalf("SHIP writes %v, want one %v", bus.writes, want)
	}
}

func TestCriticalOnBatteryShipsButNotOnDC(t *testing.T) {
	t.Parallel()

	bus := pack(10000, 1, statusDischarging, 0)
	mon := monitor(bus, line(0), line(0), true) // DC present
	mon.Step("BOSE_ICC")

	if len(bus.writes) != 0 {
		t.Fatal("shipped on DC")
	}

	mon.cfg.DCPresent = line(1)
	mon.Step("BOSE_ICC")

	if len(bus.writes) != 1 {
		t.Fatal("did not ship a critically discharged pack on battery")
	}
}

func TestOverVoltageShipsAfterOneSecond(t *testing.T) {
	t.Parallel()

	bus := pack(13200, 90, 0, 0)
	now := time.Unix(0, 0)
	mon := monitor(bus, line(0), line(0), true)
	mon.cfg.Now = func() time.Time { return now }
	mon.Step("BOSE_ICC")

	if len(bus.writes) != 0 {
		t.Fatal("shipped at the first over-voltage reading")
	}

	now = now.Add(time.Second)

	mon.Step("BOSE_ICC")

	if len(bus.writes) != 1 {
		t.Fatal("did not ship after a second above 13 V")
	}
}

func TestUnreadablePackTurnsTheChargerOff(t *testing.T) {
	t.Parallel()

	bus, enable := pack(0, 0, 0, 0), line(1)
	bus.fail = true
	mon := monitor(bus, line(0), enable, true)
	mon.Step("BOSE_ICC")

	if enable.v != 0 || mon.Status().Present || len(bus.writes) != 0 {
		t.Fatalf("unreadable pack: line %d status %+v writes %v", enable.v, mon.Status(), bus.writes)
	}
}

func TestUnknownPackIsNotMonitored(t *testing.T) {
	t.Parallel()

	bus, enable := pack(12000, 50, 0, 0), line(1)
	bus.name = "ACME"

	mon := monitor(bus, line(0), enable, true)

	err := mon.Run(t.Context())
	if !errors.Is(err, ErrUnknownPack) {
		t.Fatalf("Run = %v, want ErrUnknownPack", err)
	}

	if enable.v != 0 {
		t.Fatal("charge enable left on for an unknown pack")
	}
}

// noFault is a pack without Bose's vendor fault register.
type noFault struct{ *fakeBus }

func (b noFault) ReadWord(cmd byte) (uint16, error) {
	if cmd == RegFault {
		return 0, errNack
	}

	return b.fakeBus.ReadWord(cmd)
}

func TestAPackWithoutTheFaultRegisterStillCharges(t *testing.T) {
	t.Parallel()

	enable := line(0)
	mon := monitor(noFault{pack(12000, 60, 0, 0)}, line(0), enable, false)
	mon.Step("BOSE_SANYO")

	if s := mon.Status(); !s.Present || !s.Charging || enable.v != 1 {
		t.Fatalf("status %+v, line %d", s, enable.v)
	}
}

func TestBlockPayload(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	bus, dcLine := pack(13200, 90, 0, 0), line(0)
	now := time.Unix(0, 0)
	mon := monitor(bus, dcLine, line(0), true)
	mon.cfg.Now = func() time.Time { return now }
	mon.Step("BOSE_ICC")

	bus.fail = true
	now = now.Add(time.Minute)

	mon.Step("BOSE_ICC")

	bus.fail = false
	now = now.Add(time.Minute)

	mon.Step("BOSE_ICC")

	dcLine.v = 1
	now = now.Add(time.Minute)

	mon.Step("BOSE_ICC")

	dcLine.v = 0
	now = now.Add(time.Minute)

	mon.Step("BOSE_ICC")

	if len(bus.writes) != 0 {
		t.Fatalf("shipped on single over-voltage readings minutes apart: %v", bus.writes)
	}

	now = now.Add(time.Second)

	mon.Step("BOSE_ICC")

	if len(bus.writes) != 1 {
		t.Fatal("did not ship after a second above 13 V")
	}
}

// An error bit in the status word alone stops charging but never ships: a transient one
// would cut the power until DC returns. Only Bose's fault code ships.
func TestAStatusErrorAloneDoesNotShip(t *testing.T) {
	t.Parallel()

	bus, enable := pack(12000, 50, 0x0003, 0), line(1)
	mon := monitor(bus, line(0), enable, true)
	mon.Step("BOSE_ICC")

	if enable.v != 0 {
		t.Fatal("charging with an error in the status word")
	}

	if len(bus.writes) != 0 {
		t.Fatalf("shipped on a status error alone: %v", bus.writes)
	}
}

type brokenLine struct{}

func (brokenLine) Read() (int, error) { return 0, errGPIOGone }
func (brokenLine) Write(int) error    { return nil }

// A DC line that cannot be read says nothing about the power: low charge never ships then.
func TestAnUnreadableDCLineNeverShipsForLowCharge(t *testing.T) {
	t.Parallel()

	bus := pack(10000, 1, statusDischarging, 0)
	mon := New(Config{
		Bus: bus, DCPresent: brokenLine{}, ChargeEn: line(0), ChargeActiveHigh: false, AllowShip: true,
		Log: m0().cfg.Log, Now: m0().cfg.Now, Sleep: m0().cfg.Sleep,
	})
	mon.Step("BOSE_ICC")

	if len(bus.writes) != 0 {
		t.Fatal("shipped with the DC line unreadable")
	}
}

func m0() *Monitor { return monitor(pack(0, 0, 0, 0), line(0), line(0), true) }
