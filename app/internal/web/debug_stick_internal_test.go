// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import "testing"

// The debug bundle names the stick gesture's result from soundtapd's kernel command line.
func TestStickResultReadsTheCommandLine(t *testing.T) {
	t.Parallel()

	for cmdline, want := range map[string]string{
		"root=ubi0:rootfs variant=binky soundtap.recovery=-19": "-19 (gesture seen, no USB stick found)",
		"soundtap.recovery=0 ro":                               "0 (installed from the stick)",
		"soundtap.recovery=-110":                               "-110 (gesture seen, error -110)",
		"root=ubi0:rootfs variant=ginger":                      "no gesture seen",
		"":                                                     "no gesture seen",
	} {
		if got := stickResult(cmdline); got != want {
			t.Errorf("stickResult(%q) = %q, want %q", cmdline, got, want)
		}
	}
}
