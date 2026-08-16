// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

package oled

import "errors"

// The spidev panel transport is Linux-only: it needs the SPI_IOC_* ioctls and
// sysfs GPIO, neither of which exists off the speaker. This stub keeps the
// package building on a dev machine, matching the pattern already used by
// cmd/soundtapd's pipesize_other.go and pdeathsig_other.go. The pure wire-format
// helpers stay in spidev.go so they are tested on every platform.

var errNoSPI = errors.New("oled: spidev transport requires Linux")

// spiOLED carries only the field the portable code in oled.go reads. latest is
// always nil here, so Draw's snapshot path falls through as it does before the
// first successful frame on hardware.
type spiOLED struct {
	latest []byte
}

func openSPIOLED(string, string) (*spiOLED, error) { return nil, errNoSPI }

func (o *spiOLED) draw([]byte) error { return errNoSPI }
