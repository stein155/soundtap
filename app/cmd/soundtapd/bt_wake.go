// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"os"
	"syscall"
)

// btWakeFIFO is the pipe bt.sh's poll loop sleeps on (read -t 10).
const btWakeFIFO = "/run/soundtap-bt.wake"

// wakeBluetooth nudges that loop so a volume change on the speaker reaches the phone now
// instead of at the next 10 s tick. Best effort: with no loop to read it (not a
// speaker, Bluetooth down) the non-blocking open fails and there is nothing to wake.
func wakeBluetooth(fifo string) {
	f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0) //nolint:gosec // a fixed path under /run
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString("\n")
}
