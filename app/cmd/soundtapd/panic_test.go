// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/safego"
)

func TestPanicIsWrittenToPanicLogAndRaisedAgain(t *testing.T) {
	panicLog = filepath.Join(t.TempDir(), "panic.log")
	defer func() { panicLog = "" }()
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Fatalf("re-raised %v, want boom", r)
			}
		}()
		defer safego.Recover("player")
		panic("boom")
	}()
	b, err := os.ReadFile(panicLog)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, `goroutine "player": panic: boom`) || !strings.Contains(s, "panic_test.go") {
		t.Fatalf("panic log = %q", s)
	}
}
