// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

//nolint:paralleltest // changes package-wide state other tests read: snapserverLastSeen
func TestSnapserverStateKeptOnlyWhenMoreThanLastSeenChanges(t *testing.T) {
	run, keep := t.TempDir(), filepath.Join(t.TempDir(), "keep")
	write := func(s string) {
		t.Helper()

		err := os.WriteFile(filepath.Join(run, snapserverState), []byte(s), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
	kept := func() string {
		data, _ := os.ReadFile(filepath.Join(keep, snapserverState))

		return string(data)
	}
	first := `{"id":"x","lastSeen":{"sec":1,"usec":2},"volume":{"percent":100}}`
	seenAgain := `{"id":"x","lastSeen":{"sec":31,"usec":7},"volume":{"percent":100}}`
	newVolume := `{"id":"x","lastSeen":{"sec":61,"usec":7},"volume":{"percent":40}}`

	var (
		last []byte
		err  error
	)

	write(first)

	last, err = keepSnapserverOnce(run, keep, last)
	if err != nil || kept() != first {
		t.Fatalf("first state not kept: %v %q", err, kept())
	}

	write(seenAgain)

	last, err = keepSnapserverOnce(run, keep, last)
	if err != nil || kept() != first {
		t.Fatalf("a lastSeen-only change reached flash: %v %q", err, kept())
	}

	write(newVolume)

	_, err = keepSnapserverOnce(run, keep, last)
	if err != nil || kept() != newVolume {
		t.Fatalf("a volume change was not kept: %v %q", err, kept())
	}

	// A boot seeds tmpfs from flash; a soundtapd restart keeps the newer tmpfs copy.
	fresh := filepath.Join(t.TempDir(), "run")
	log := slog.New(slog.DiscardHandler)
	seedSnapserverState(fresh, keep, log)

	if data, _ := os.ReadFile(filepath.Join(fresh, snapserverState)); string(data) != newVolume {
		t.Fatalf("boot did not seed from flash: %q", data)
	}

	write(seenAgain)
	seedSnapserverState(run, keep, log)

	if data, _ := os.ReadFile(filepath.Join(run, snapserverState)); string(data) != seenAgain {
		t.Fatalf("restart overwrote the live copy: %q", data)
	}

	// A factory reset removes the kept copy and restarts soundtapd: tmpfs must not
	// bring the old clients back.
	removeErr := os.Remove(filepath.Join(keep, snapserverState))
	if removeErr != nil {
		t.Fatal(removeErr)
	}

	seedSnapserverState(run, keep, log)

	_, statErr := os.Stat(filepath.Join(run, snapserverState))
	if !os.IsNotExist(statErr) {
		t.Fatalf("live copy survived a reset: %v", statErr)
	}
}
