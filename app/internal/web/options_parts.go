// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"

	"github.com/stein155/soundtap/app/internal/btctl"
)

// SoundTapdUpdate is where an uploaded app binary goes; see firmware.Handler.
type SoundTapdUpdate struct {
	Run, Base, Home string
	Restart         func()
}

// SetupOptions are the network setup and reset hooks.
type SetupOptions struct {
	SetupMode    func() error                  // drop into provisioning / setup AP
	WifiProfiles func() ([]WifiProfile, error) // the Wi-Fi networks the speaker remembers
	ForgetWifi   func(id string) error         // drop one of them
	// FactoryReset performs the reset the front-panel chord performs: state, pairings,
	// Wi-Fi, and a restart.
	FactoryReset func() error
}

// BluetoothOptions reach BlueZ: the system bus and soundtapd's pairing agent.
type BluetoothOptions struct {
	Bus         btctl.Bus
	Pair, Clear func(context.Context) error
	// Forget forgets one paired device by address and says whether it was connected;
	// nil is btctl.Forget on Bus.
	Forget func(ctx context.Context, address string) (connected bool, err error)
	// Paired says whether a device with this address is paired, before the press on the
	// speaker is asked for; nil is btctl.IsPaired on Bus, or no check without a Bus.
	Paired func(ctx context.Context, address string) (bool, error)
}

// PanelOptions is what the speaker's own front shows and asks.
type PanelOptions struct {
	// DisplayAvailable says whether an OLED panel is present, so the standby-screen page
	// is offered only where it does something.
	DisplayAvailable func() bool
	// Notice shows an event on the speaker's own display, in English (the display
	// translates it).
	Notice func(en string)
	// Confirm waits for a key press before a dangerous route runs.
	Confirm DeviceConfirm
}

// ChangeHooks hear what the API changed that runs elsewhere.
type ChangeHooks struct {
	Name      func(string) // restart name-advertising receivers after a rename
	Receivers func()       // an integration was switched on or off
}
