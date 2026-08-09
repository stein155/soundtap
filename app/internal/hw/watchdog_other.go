// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

package hw

import (
	"errors"
	"os"
	"time"
)

func setWatchdogTimeout(*os.File, time.Duration) error { return errors.New("not linux") }
