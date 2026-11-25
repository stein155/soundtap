// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import "testing"

// The filter has to catch the lines a speaker with no shell cannot otherwise be asked
// about, and drop ordinary boot noise. The Bluetooth timeout below is the real message
// from this hardware, and the line before it is what distinguishes a command that was
// never transmitted from a controller that never answered.
func TestKernelMessageFilterKeepsWhatMatters(t *testing.T) {
	t.Parallel()

	keep := []string{
		"[    9.13] Bluetooth: hci0: Reading TI version information failed (-110)",
		"[    9.11] Bluetooth: hci0: command 0x1001 tx timeout",
		"[    1.57] nand: using OMAP_ECC_BCH8_CODE_HW ECC scheme",
		"[    2.01] UBIFS (ubi0:0): mounted UBI device 0",
		"[    3.44] wlcore: firmware booted",
		"[    4.10] asoc-simple-card sound: ASoC: failed to init link",
	}
	drop := []string{
		"[    0.00] Booting Linux on physical CPU 0x0",
		"[    0.12] clocksource: timer1 mult 0x1",
		"[    0.98] random: crng init done",
	}

	for _, line := range keep {
		if !kernelMessageWanted(line) {
			t.Errorf("dropped a line that answers a hardware question: %q", line)
		}
	}

	for _, line := range drop {
		if kernelMessageWanted(line) {
			t.Errorf("kept ordinary boot noise: %q", line)
		}
	}
}
