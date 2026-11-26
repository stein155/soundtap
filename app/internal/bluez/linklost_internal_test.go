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
	t.Parallel()

	agent := New(slog.New(slog.DiscardHandler), func() string { return "x" })
	hit := make(chan struct{}, 4)

	agent.OnLinkLost(func() { hit <- struct{}{} })

	sig := func(reason string) {
		agent.deviceDisconnected(&dbus.Signal{
			Sender: "", Path: adapterPath + "/dev_X", Name: deviceIface + ".Disconnected",
			Body: []any{reason, ""}, Sequence: 0,
		})
	}
	sig("org.bluez.Reason.Remote")
	sig("org.bluez.Reason.Local")
	agent.deviceDisconnected(&dbus.Signal{
		Sender: "", Path: "", Name: deviceIface + ".Disconnected", Body: nil, Sequence: 0,
	})
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
	t.Parallel()

	fake := newFake(t)
	agent, _, _ := start(t, fake)
	hit := make(chan struct{}, 1)

	agent.OnLinkLost(func() { hit <- struct{}{} })
	fake.emit(adapterPath+"/dev_X", deviceIface+".Disconnected", reasonTimeout, "Connection timed out")

	select {
	case <-hit:
	case <-time.After(5 * time.Second):
		t.Fatal("a Disconnected signal from BlueZ did not call OnLinkLost")
	}
}
