// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

package hw

import "errors"

// errNoIRDevice is the receiver on a machine that is not the speaker.
var errNoIRDevice = errors.New("ir: only supported on the speaker (linux)")

// openIRDevice is unavailable off Linux. The Bose ir.ko chardev only exists on the
// speaker, and syscall.Select's signature differs per platform; the stub keeps the
// package building on a dev machine, where the synth is exercised through
// irSource fakes instead. runIR then just logs "not available" and idles.
func openIRDevice(string) (irSource, error) { //nolint:ireturn // the linux build returns two receiver types behind irSource
	return nil, errNoIRDevice
}
