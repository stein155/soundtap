// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package clocksync tells whether the clock has been set from NTP since this boot.
//
// After a boot the clock comes from the last save on flash, which after a power cut can
// be hours behind. Like stock, which shows its clock only after the first NTP sync, the
// standby clock and the alarm wait for that sync.
package clocksync

import "os"

// Path is written by clock.sh after its first good NTP sync. It is on tmpfs, so every
// boot starts without it. A variable for the tests.
var Path = "/run/soundtap/clock-synced"

// Synced reports whether NTP has set the clock since this boot.
func Synced() bool {
	_, err := os.Stat(Path)
	return err == nil
}
