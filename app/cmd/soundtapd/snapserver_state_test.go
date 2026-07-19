// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapserverStateKeptOnlyWhenMoreThanLastSeenChanges(t *testing.T) {
	run, keep := t.TempDir(), filepath.Join(t.TempDir(), "keep")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(run, snapserverState), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	kept := func() string {
		data, _ := os.ReadFile(filepath.Join(keep, snapserverState))
		return string(data)
	}
	a := `{"id":"x","lastSeen":{"sec":1,"usec":2},"volume":{"percent":100}}`
	b := `{"id":"x","lastSeen":{"sec":31,"usec":7},"volume":{"percent":100}}`
	c := `{"id":"x","lastSeen":{"sec":61,"usec":7},"volume":{"percent":40}}`

	var last []byte
	var err error
	write(a)
	if last, err = keepSnapserverOnce(run, keep, last); err != nil || kept() != a {
		t.Fatalf("first state not kept: %v %q", err, kept())
	}
	write(b)
	if last, err = keepSnapserverOnce(run, keep, last); err != nil || kept() != a {
		t.Fatalf("a lastSeen-only change reached flash: %v %q", err, kept())
	}
	write(c)
	if _, err = keepSnapserverOnce(run, keep, last); err != nil || kept() != c {
		t.Fatalf("a volume change was not kept: %v %q", err, kept())
	}

	// A boot seeds tmpfs from flash; a soundtapd restart keeps the newer tmpfs copy.
	fresh := filepath.Join(t.TempDir(), "run")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	seedSnapserverState(fresh, keep, log)
	if data, _ := os.ReadFile(filepath.Join(fresh, snapserverState)); string(data) != c {
		t.Fatalf("boot did not seed from flash: %q", data)
	}
	write(b)
	seedSnapserverState(run, keep, log)
	if data, _ := os.ReadFile(filepath.Join(run, snapserverState)); string(data) != b {
		t.Fatalf("restart overwrote the live copy: %q", data)
	}

	// A factory reset removes the kept copy and restarts soundtapd: tmpfs must not
	// bring the old clients back.
	if err := os.Remove(filepath.Join(keep, snapserverState)); err != nil {
		t.Fatal(err)
	}
	seedSnapserverState(run, keep, log)
	if _, err := os.Stat(filepath.Join(run, snapserverState)); !os.IsNotExist(err) {
		t.Fatalf("live copy survived a reset: %v", err)
	}
}
