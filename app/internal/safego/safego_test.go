// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package safego

import (
	"strings"
	"testing"
)

// A panic reaches the recorder with its stack and is raised again (ADR 0052).
func TestRecoverRecordsAndPanicsAgain(t *testing.T) {
	var name string
	var stack []byte
	SetRecorder(func(n string, r any, s []byte) { name, stack = n, s })
	t.Cleanup(func() { SetRecorder(nil) })
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Fatalf("raised %v, want boom", r)
			}
		}()
		defer Recover("worker")
		panic("boom")
	}()
	if name != "worker" || !strings.Contains(string(stack), "safego_test.go") {
		t.Fatalf("recorded %q with stack %q", name, stack)
	}
}

// Go runs fn on its own goroutine.
func TestGoRunsFn(t *testing.T) {
	done := make(chan struct{})
	Go("test", func() { close(done) })
	<-done
}
