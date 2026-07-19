// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

//go:build !linux

package main

import (
	"errors"
	"log/slog"
)

// setPipeSize is a no-op off Linux; F_SETPIPE_SZ is Linux-only and the daemon only
// ships on the speaker. Keeping the stub lets the package build on a dev machine.
func setPipeSize(any, int, string, *slog.Logger) {}

// pipeQueued has no portable equivalent; multiroom sync then counts the pipes as empty.
func pipeQueued(uintptr) (int, error) { return 0, errors.ErrUnsupported }
