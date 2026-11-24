// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import "time"

// How long a handler waits on something outside this process. The web UI gives up on
// its request too, so each of these has to end before the UI's own deadline for the
// same call: a handler that outlasts its caller reports "failed" for work the speaker
// then finishes. TestDeadlinesEndBeforeTheWebUIGivesUp holds the two
// sides together; the UI's numbers are named beside each one.
const (
	// TuneIn, a media server and BlueZ answer in their own time.
	// The UI allows SLOW_READ_TIMEOUT_MS.
	slowReadDeadline = 12 * time.Second
	// Starting a station or storing a preset. The UI allows WRITE_TIMEOUT_MS.
	writeDeadline = 12 * time.Second
	// The UI allows SPOTIFY_TIMEOUT_MS.
	spotifyDeadline = 15 * time.Second
	// The UI allows BLUETOOTH_CLEAR_TIMEOUT_MS.
	bluetoothClearDeadline = 30 * time.Second
	// A site survey runs for many seconds. The UI allows WIFI_SCAN_TIMEOUT_MS.
	wifiScanDeadline = 80 * time.Second

	// The ones below run after the key press, so the UI allows PRESS_WINDOW_MS plus
	// the constant named, and the device proxy ConfirmWindow plus the deadline plus
	// pressSlack.

	// Joining a network. On the ST20 bco.Connect waits for the serial port and then
	// polls up to 45 s for the association; a shorter deadline expires mid-join and
	// reports a correct password as a failure. The UI allows WIFI_JOIN_TIMEOUT_MS.
	wifiJoinDeadline = 60 * time.Second
	// The steps of a restore or a factory reset. The UI allows RESTORE_TIMEOUT_MS.
	restoreDeadline = 20 * time.Second
)
