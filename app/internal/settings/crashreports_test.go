// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/settings"
)

// Crash reports are off unless the owner turns them on: a file from before them reads as
// off, and a file that never had them on does not grow the field (ADR 0126).
func TestCrashReportsOffByDefaultAndPersist(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")

	err := os.WriteFile(path, []byte(`{"schemaVersion":1,"name":"Kitchen","language":"nl"}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	store := settings.Open(path)
	if c := store.Get().CrashReports; c.Enabled || len(c.Handled) != 0 {
		t.Fatalf("an older file reads crash reports as %+v", c)
	}

	err = store.SetLanguage("de")
	if err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(path); strings.Contains(string(b), "crashReports") {
		t.Fatalf("a file without the opt-in grew the field:\n%s", b)
	}

	err = store.Update(func(next *settings.Settings) error {
		next.CrashReports.Enabled = true
		next.CrashReports.Handle("panic-0123456789ab")

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	c := settings.Open(path).Get().CrashReports
	if !c.Enabled || !c.IsHandled("panic-0123456789ab") || c.IsHandled("kernel-0123456789ab") {
		t.Errorf("after reopening: %+v", c)
	}
}

func TestHandleKeepsTheNewestCrashesOnce(t *testing.T) {
	t.Parallel()

	var reports settings.CrashReports
	for i := range settings.MaxHandledCrashes + 5 {
		reports.Handle(fmt.Sprintf("panic-%012d", i))
	}

	reports.Handle(fmt.Sprintf("panic-%012d", settings.MaxHandledCrashes+4)) // already there: not again

	if len(reports.Handled) != settings.MaxHandledCrashes {
		t.Fatalf("%d handled ids, want %d", len(reports.Handled), settings.MaxHandledCrashes)
	}

	if reports.IsHandled("panic-000000000004") || !reports.IsHandled("panic-000000000005") {
		t.Errorf("the oldest ids should go first: %v", reports.Handled[:2])
	}
}

// Get hands out a copy: a caller appending to it changes nothing stored.
func TestGetCopiesHandledCrashes(t *testing.T) {
	t.Parallel()

	store := settings.Open(filepath.Join(t.TempDir(), "settings.json"))

	err := store.Update(func(next *settings.Settings) error {
		next.CrashReports.Handle("panic-a")

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	got := store.Get()

	got.CrashReports.Handled[0] = "changed"
	if store.Get().CrashReports.Handled[0] != "panic-a" {
		t.Error("Get shares the handled list with the store")
	}
}
