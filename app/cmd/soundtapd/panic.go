// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
)

// panicLog keeps the last crash under -home, which survives the restart that follows.
// GET /api/debug shows it (web.PanicLogName).
//
//nolint:gochecknoglobals // process-wide state: main sets it from -home, every goroutine's recorder reads it
var panicLog string

// maxPanicTrace is one trace; /mnt/nv is small and this is rewritten per crash.
const maxPanicTrace = 32 << 10

// panicLogMode lets the web server's user read the crash.
const panicLogMode = 0o644

// Every goroutine's panic, in this package and in internal/, goes to panicLog.
//
//nolint:gochecknoinits // the recorder is in place before any goroutine can panic, in tests too
func init() { safego.SetRecorder(writePanic) }

// goSafe runs fn in its own goroutine through safego.Go: a panic is written to panicLog
// and raised again. The supervisor restarting soundtapd stays the recovery, the file is
// so the stack outlives it (ADR 0052). Before, the trace went to a console nobody reads.
func goSafe(name string, fn func()) { safego.Go(name, fn) }

func writePanic(name string, recovered any, stack []byte) {
	if panicLog == "" {
		return
	}

	if len(stack) > maxPanicTrace {
		stack = stack[:maxPanicTrace]
	}

	_ = os.WriteFile(panicLog, fmt.Appendf(nil, "%s goroutine %q: panic: %v\n\n%s",
		time.Now().UTC().Format(time.RFC3339), name, recovered, stack), panicLogMode)
}
