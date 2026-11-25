// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

//nolint:paralleltest // changes package-wide state other tests read: latinFold
func TestDHCPHostname(t *testing.T) {
	for input, want := range map[string]string{
		"Keuken":                       "keuken",
		"Living Room 2":                "living-room-2",
		"  --Zolder!!  ":               "zolder",
		"Ronald's kamer":               "ronald-s-kamer",
		"キッチン":                         "",
		"Küche":                        "kuche",
		"Salle à manger":               "salle-a-manger",
		"Łazienka Żółta":               "lazienka-zolta",
		strings.Repeat("a", 70):        strings.Repeat("a", 63),
		strings.Repeat("a", 62) + " b": strings.Repeat("a", 62),
	} {
		if got := dhcpHostname(input); got != want {
			t.Errorf("dhcpHostname(%q) = %q, want %q", input, got, want)
		}
	}
}

//nolint:paralleltest // swaps package variables other tests read: hostnameFile, setHostname
func TestPublishHostname(t *testing.T) {
	dir := t.TempDir()
	oldFile, oldSet := hostnameFile, setHostname

	t.Cleanup(func() { hostnameFile, setHostname = oldFile, oldSet })

	hostnameFile = filepath.Join(dir, "hostname")

	var set string

	setHostname = func(b []byte) error {
		set = string(b)

		return nil
	}

	err := publishHostname("Werk Kamer")
	if err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(hostnameFile)
	if err != nil || string(b) != "werk-kamer\n" || set != "werk-kamer" {
		t.Fatalf("hostname file %q (%v), set %q", b, err, set)
	}
}

//nolint:paralleltest // swaps package variables other tests read: hostnameFile, setHostname
func TestPublishHostnameSkipsUnchangedFile(t *testing.T) {
	dir := t.TempDir()
	oldFile, oldSet := hostnameFile, setHostname

	t.Cleanup(func() { hostnameFile, setHostname = oldFile, oldSet })

	hostnameFile = filepath.Join(dir, "hostname")
	setHostname = func([]byte) error { return nil }

	err := publishHostname("Keuken")
	if err != nil {
		t.Fatal(err)
	}

	past := time.Unix(1, 0)

	err = os.Chtimes(hostnameFile, past, past)
	if err != nil {
		t.Fatal(err)
	}

	err = publishHostname("Keuken")
	if err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(hostnameFile)
	if err != nil || !st.ModTime().Equal(past) {
		t.Fatalf("unchanged name rewrote the file: %v %v", st, err)
	}
}

//nolint:paralleltest // swaps package variables other tests read: hostnameFile, setHostname
func TestPublishHostnameDropsOldNameForUnusableName(t *testing.T) {
	dir := t.TempDir()
	oldFile, oldSet := hostnameFile, setHostname

	t.Cleanup(func() { hostnameFile, setHostname = oldFile, oldSet })

	hostnameFile = filepath.Join(dir, "hostname")
	setHostname = func([]byte) error { return nil }

	err := publishHostname("Keuken")
	if err != nil {
		t.Fatal(err)
	}

	err = publishHostname("キッチン")
	if err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(hostnameFile)
	if !os.IsNotExist(err) {
		t.Fatalf("old room name survived a rename to a name with no DNS characters: %v", err)
	}

	err = publishHostname("キッチン")
	if err != nil {
		t.Fatalf("no file to drop reported %v", err)
	}
}

// A hostname that cannot be kept for init.d is reported, not dropped.
//
//nolint:paralleltest // swaps package variables other tests read: hostnameFile, setHostname
func TestPublishHostnameReportsWriteFailure(t *testing.T) {
	dir := t.TempDir()
	oldFile, oldSet := hostnameFile, setHostname

	t.Cleanup(func() { hostnameFile, setHostname = oldFile, oldSet })

	blocker := filepath.Join(dir, "not-a-dir")

	err := os.WriteFile(blocker, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	hostnameFile = filepath.Join(blocker, "hostname")
	setHostname = func([]byte) error { return nil }

	err = publishHostname("Keuken")
	if err == nil {
		t.Fatal("a hostname file that cannot be written reported no error")
	}
}
