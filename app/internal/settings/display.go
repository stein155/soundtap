// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

// Display configures the OLED standby screen (ST20 only; the ST10 has no panel): the time
// alone, as stock's.
//
// The clock needs a timezone: the zone of the browser that saved these settings, as stock
// shows its clock without a place. The speaker never guesses where it stands (the rootfs
// has no zone and a public-IP lookup is not something a radio should do on its own).
// Without a zone the standby screen stays dark. The zero value is that state.
//
// Files from before 0.1.0 may still hold city, lat, lon and weatherOn from the weather
// line that is gone, and brightness from a dimming setting that went the way of stock's
// (stock does not dim its clock; it walks against burn-in, as this one does); the loader
// ignores them.
type Display struct {
	ClockOff bool   `json:"clockOff"`
	Hour12   bool   `json:"hour12"`
	Timezone string `json:"timezone"` // IANA zone of the browser that saved the settings
}

// ClockOn reports whether the standby screen shows the clock.
func (d Display) ClockOn() bool { return !d.ClockOff && d.Timezone != "" }
