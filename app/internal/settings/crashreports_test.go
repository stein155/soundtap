// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Crash reports are off unless the owner turns them on: a file from before them reads as
// off, and a file that never had them on does not grow the field (ADR 0126).
func TestCrashReportsOffByDefaultAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	err := os.WriteFile(path, []byte(`{"schemaVersion":1,"name":"Kitchen","language":"nl"}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := Open(path)
	if c := s.Get().CrashReports; c.Enabled || len(c.Handled) != 0 {
		t.Fatalf("an older file reads crash reports as %+v", c)
	}

	err = s.SetLanguage("de")
	if err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(path); strings.Contains(string(b), "crashReports") {
		t.Fatalf("a file without the opt-in grew the field:\n%s", b)
	}

	err = s.Update(func(next *Settings) error {
		next.CrashReports.Enabled = true
		next.CrashReports.Handle("panic-0123456789ab")

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	c := Open(path).Get().CrashReports
	if !c.Enabled || !c.IsHandled("panic-0123456789ab") || c.IsHandled("kernel-0123456789ab") {
		t.Errorf("after reopening: %+v", c)
	}
}

func TestHandleKeepsTheNewestCrashesOnce(t *testing.T) {
	var c CrashReports
	for i := range MaxHandledCrashes + 5 {
		c.Handle(fmt.Sprintf("panic-%012d", i))
	}

	c.Handle(fmt.Sprintf("panic-%012d", MaxHandledCrashes+4)) // already there: not again

	if len(c.Handled) != MaxHandledCrashes {
		t.Fatalf("%d handled ids, want %d", len(c.Handled), MaxHandledCrashes)
	}

	if c.IsHandled("panic-000000000004") || !c.IsHandled("panic-000000000005") {
		t.Errorf("the oldest ids should go first: %v", c.Handled[:2])
	}
}

// Get hands out a copy: a caller appending to it changes nothing stored.
func TestGetCopiesHandledCrashes(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "settings.json"))

	err := s.Update(func(next *Settings) error {
		next.CrashReports.Handle("panic-a")

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	got := s.Get()

	got.CrashReports.Handled[0] = "changed"
	if s.Get().CrashReports.Handled[0] != "panic-a" {
		t.Error("Get shares the handled list with the store")
	}
}
