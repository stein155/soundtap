// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"log/slog"
	"os"
)

// logLevel is set from SOUNDTAP_LOG_LEVEL at start-up; info when unset.
var (
	logLevel slog.LevelVar
	logOpts  = &slog.HandlerOptions{Level: &logLevel}
)

// limitedFile writes until left runs out, then stops silently: a side copy of a log
// that also goes to stderr, so nothing is lost but the copy's tail.
type limitedFile struct {
	f    *os.File
	left int
}

func (l *limitedFile) Write(p []byte) (int, error) {
	if l.left <= 0 {
		return len(p), nil
	}
	n := min(len(p), l.left)
	l.left -= n
	_, err := l.f.Write(p[:n])
	return len(p), err
}
