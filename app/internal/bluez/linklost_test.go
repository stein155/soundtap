// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bluez

import (
	"log/slog"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestLinkLostOnlyOnTimeout(t *testing.T) {
	a := New(slog.New(slog.DiscardHandler), func() string { return "x" })
	hit := make(chan struct{}, 4)
	a.OnLinkLost(func() { hit <- struct{}{} })
	sig := func(reason string) {
		a.deviceDisconnected(&dbus.Signal{Path: adapterPath + "/dev_X", Name: deviceIface + ".Disconnected", Body: []any{reason, ""}})
	}
	sig("org.bluez.Reason.Remote")
	sig("org.bluez.Reason.Local")
	a.deviceDisconnected(&dbus.Signal{Name: deviceIface + ".Disconnected"})
	sig(reasonTimeout)
	select {
	case <-hit:
	case <-time.After(time.Second):
		t.Fatal("timeout did not call OnLinkLost")
	}
	select {
	case <-hit:
		t.Fatal("a non-timeout disconnect called OnLinkLost")
	case <-time.After(50 * time.Millisecond):
	}
}

// The signal reaches the agent over the bus, from BlueZ.
func TestLinkLostOverTheBus(t *testing.T) {
	f := newFake(t)
	a, _, _ := start(t, f)
	hit := make(chan struct{}, 1)
	a.OnLinkLost(func() { hit <- struct{}{} })
	f.emit(adapterPath+"/dev_X", deviceIface+".Disconnected", reasonTimeout, "Connection timed out")
	select {
	case <-hit:
	case <-time.After(5 * time.Second):
		t.Fatal("a Disconnected signal from BlueZ did not call OnLinkLost")
	}
}
