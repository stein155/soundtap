// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package abl

import (
	"bytes"
	"testing"
)

func TestRegistrationIsStocks(t *testing.T) {
	t.Parallel()

	msgs := RegistrationMessages(false)
	if !bytes.Equal(msgs[0], []byte{2, 1, 4, 0, rcBootDelay, 1, 0, 0x0d}) {
		t.Fatalf("boot delay % x", msgs[0])
	}

	if len(msgs[1]) != 0x2e || !bytes.Equal(msgs[1][4:11], []byte{rcState, 1, 0, 1, 3, 0, 1}) {
		t.Fatalf("state registration % x", msgs[1])
	}
	// Preset 1, a press: no hold time, flags 0x8c with 0x04 set.
	if !bytes.Equal(msgs[2], []byte{2, 1, 4, 0, rcKeyRegistration, 1, 10, 1, 1, 0x8c, 0x11, 1, 5, 1}) {
		t.Fatalf("preset 1 registration % x", msgs[2])
	}
	// Preset 1 held: command 7, flags 0x1c, 1500 ms.
	store := msgs[2+12]
	if !bytes.Equal(store[4:], []byte{rcKeyRegistration, 1, 12, 13, 7, 0x1c, 0x11, 1, 5, 1, 0x05, 0xdc}) {
		t.Fatalf("store preset 1 registration % x", store)
	}

	last := msgs[len(msgs)-1]
	if !bytes.Equal(last, []byte{2, 1, 4, 0, rcDone, 1, 0, 1, 0, 18}) {
		t.Fatalf("registration done % x", last)
	}

	if n := len(RegistrationMessages(true)); n != len(msgs)+2 {
		t.Fatalf("with Bluetooth %d messages, want %d", n, len(msgs)+2)
	}
}

// rcFrame is a remote-control message from the host in room 3.
func rcFrame(t *testing.T, body ...byte) []byte {
	t.Helper()

	f, err := P2PDatagram(3, Broadcast, PortRemote, PortRemote, append([]byte{2, 1, 4, 0}, body...))
	if err != nil {
		t.Fatal(err)
	}

	return f
}

func TestAWaveGetsTheKeysAndItsRemoteWorks(t *testing.T) {
	t.Parallel()

	host := newFakeHost(3, ZoneAdapter, 20, 0x30c0, 0)
	bus := newRig(host)

	type key struct {
		key    string
		preset int
		store  bool
	}

	var keys []key

	selected := -1
	bus.s.OnKey = func(k string, p int, s bool) { keys = append(keys, key{k, p, s}) }
	bus.s.OnSelect = func(p int) { selected = p }
	bus.run(400)

	var regs int

	for _, frame := range bus.line.written {
		d, err := ParseP2PDatagram(frame)
		if err == nil && d.DstPort == PortRemote {
			if typ, _, ok := rcMessage(d.Data); ok && typ == rcKeyRegistration {
				regs++
			}
		}
	}

	if regs != len(rcKeys()) {
		t.Fatalf("%d key registrations, want %d", regs, len(rcKeys()))
	}
	// Preset 2 held (store), then its release (preset 2 again: skipped), then next track.
	host.out = append(host.out, rcFrame(t, rcCommand, 1, 4, 8), rcFrame(t, rcCommand, 1, 4, 2),
		rcFrame(t, rcCommand, 1, 4, 0x0d), rcFrame(t, rcCommand, 1, 4, 3))

	bus.run(80)

	want := []key{{"", 2, true}, {KeyNext, 0, false}, {"", 3, false}}
	if len(keys) != len(want) {
		t.Fatalf("keys %+v, want %+v", keys, want)
	}

	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys %+v, want %+v", keys, want)
		}
	}
	// The host picks the adapter with preset 4 from its own buttons.
	host.out = append(host.out, rcFrame(t, rcSourceSelection, 2, 4, rcActivate, 4))

	bus.run(40)

	if selected != 4 {
		t.Fatalf("selected preset %d, want 4", selected)
	}
}

func TestOldHostsGetNoRegistration(t *testing.T) {
	t.Parallel()

	host := newFakeHost(1, ZoneOff, 0, 0, 0x03)
	bus := newRig(host)
	bus.run(400)

	for _, frame := range bus.line.written {
		d, err := ParseP2PDatagram(frame)
		if err == nil && d.DstPort == PortRemote {
			t.Fatalf("an SA-4 got a remote-control message: % x", d.Data)
		}
	}
}
