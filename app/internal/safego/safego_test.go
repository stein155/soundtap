// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package safego_test

import (
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/safego"
)

// A panic reaches the recorder with its stack and is raised again (ADR 0052).
//
//nolint:paralleltest // installs the process-wide recorder, which every goroutine Go starts reads
func TestRecoverRecordsAndPanicsAgain(t *testing.T) {
	var (
		name  string
		stack []byte
	)

	safego.SetRecorder(func(n string, _ any, s []byte) { name, stack = n, s })
	t.Cleanup(func() { safego.SetRecorder(nil) })
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Fatalf("raised %v, want boom", r)
			}
		}()
		defer safego.Recover("worker")

		panic("boom")
	}()

	if name != "worker" || !strings.Contains(string(stack), "safego_test.go") {
		t.Fatalf("recorded %q with stack %q", name, stack)
	}
}

// Go runs fn on its own goroutine.
func TestGoRunsFn(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})

	safego.Go("test", func() { close(done) })
	<-done
}
