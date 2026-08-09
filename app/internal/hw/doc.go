// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package hw drives a speaker's own hardware from userspace: the front-panel keypad (a
// GPIO matrix or a key device), the infrared receiver, the lamps, the amplifier's mute,
// power and fault lines, and the hardware watchdog. soundtapd wires each piece per model
// (internal/hw/model) to the rest of the daemon through small interfaces: KeySink takes
// key edges, LampStatus says what the lamps show.
package hw
