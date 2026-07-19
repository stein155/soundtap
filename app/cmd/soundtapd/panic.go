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
var panicLog string

// Every goroutine's panic, in this package and in internal/, goes to panicLog.
func init() { safego.SetRecorder(writePanic) }

// goSafe runs fn in its own goroutine through safego.Go: a panic is written to panicLog
// and raised again. The supervisor restarting soundtapd stays the recovery, the file is
// so the stack outlives it (ADR 0052). Before, the trace went to a console nobody reads.
func goSafe(name string, fn func()) { safego.Go(name, fn) }

func writePanic(name string, r any, stack []byte) {
	if panicLog == "" {
		return
	}
	const max = 32 << 10 // one trace; /mnt/nv is small and this is rewritten per crash
	if len(stack) > max {
		stack = stack[:max]
	}
	_ = os.WriteFile(panicLog, fmt.Appendf(nil, "%s goroutine %q: panic: %v\n\n%s",
		time.Now().UTC().Format(time.RFC3339), name, r, stack), 0o644)
}
