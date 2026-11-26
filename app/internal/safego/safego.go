// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package safego starts goroutines whose panic is recorded before it ends the process
// (ADR 0052). Every goroutine in internal/ starts through Go; TestNoBareGoStatements
// fails on a bare go statement anywhere else.
package safego

import (
	"log/slog"
	"runtime/debug"
	"sync/atomic"
)

// Recorder keeps a panic's stack where it outlives the restart that follows: soundtapd
// writes it to <home>/panic.log, which GET /api/debug shows.
type Recorder func(name string, r any, stack []byte)

//nolint:gochecknoglobals // process-wide state: one recorder for every goroutine Go starts
var recorder atomic.Pointer[Recorder]

// SetRecorder installs where a panic is recorded; nil records it in the log only.
func SetRecorder(rec Recorder) {
	if rec == nil {
		recorder.Store(nil)

		return
	}

	recorder.Store(&rec)
}

// Go runs fn in its own goroutine. A panic is logged, recorded and raised again: the
// supervisor restarting soundtapd stays the recovery, because a speaker missing a
// subsystem with no sign of it is worse than a visible restart (ADR 0052).
func Go(name string, fn func()) {
	go func() {
		defer Recover(name)

		fn()
	}()
}

// Recover records a panic in progress and raises it again. It must be deferred
// directly, as recover only works there: `defer safego.Recover("main")`.
func Recover(name string) {
	recovered := recover() //nolint:revive // Recover is the deferred function: callers defer it directly
	if recovered == nil {
		return
	}

	stack := debug.Stack()

	slog.Error("goroutine panicked", "goroutine", name, "panic", recovered)

	if rec := recorder.Load(); rec != nil {
		(*rec)(name, recovered, stack)
	}

	panic(recovered)
}
