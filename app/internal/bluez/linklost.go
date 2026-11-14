// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package bluez

import (
	"github.com/godbus/dbus/v5"

	"github.com/stein155/soundtap/app/internal/safego"
)

// reasonTimeout is BlueZ's Device1.Disconnected reason for a link that was lost (the phone
// walked out of range) rather than closed by either side.
const reasonTimeout = "org.bluez.Reason.Timeout"

// OnLinkLost sets what runs when a connected device's link times out. Stock retries the
// paired list then; a phone whose user disconnected it on purpose (reason Remote) is left
// alone.
func (a *Agent) OnLinkLost(f func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.linkLost = f
}

// deviceDisconnected handles Device1.Disconnected(reason, message), BlueZ 5.68 and later.
func (a *Agent) deviceDisconnected(s *dbus.Signal) {
	if len(s.Body) == 0 || s.Body[0] != reasonTimeout {
		return
	}
	a.mu.Lock()
	f := a.linkLost
	a.mu.Unlock()
	if f == nil {
		return
	}
	a.log.Info("bluetooth link lost", "device", s.Path)
	safego.Go("bluez.linkLost", func() { f() })
}
