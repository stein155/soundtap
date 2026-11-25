// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultWhenFileAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	s := Open(path)
	if got := s.Get().Language; got != "en" {
		t.Fatalf("default Language = %q, want \"en\"", got)
	}
}

func TestSetLanguageAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	s := Open(path)

	err := s.SetLanguage("nl")
	if err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}

	if got := s.Get().Language; got != "nl" {
		t.Fatalf("Language = %q, want \"nl\"", got)
	}

	// Reopen from the same path; the language must have persisted.
	s2 := Open(path)
	if got := s2.Get().Language; got != "nl" {
		t.Fatalf("reopened Language = %q, want \"nl\"", got)
	}
}

func TestSetNameValidatesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	s := Open(path)

	err := s.SetName("  Living Room  ")
	if err != nil {
		t.Fatalf("SetName: %v", err)
	}

	if got := Open(path).Get().Name; got != "Living Room" {
		t.Fatalf("reopened Name = %q, want Living Room", got)
	}

	for _, name := range []string{"", "bad\nname", string(make([]rune, 65))} {
		err := s.SetName(name)
		if err == nil {
			t.Errorf("SetName(%q) succeeded", name)
		}
	}

	if got := s.Get().Name; got != "Living Room" {
		t.Fatalf("Name after rejected writes = %q", got)
	}
}

func TestSetLanguageEmptyIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	s := Open(path)

	err := s.SetLanguage("fr")
	if err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	// Empty code must not overwrite the existing language.
	err = s.SetLanguage("")
	if err != nil {
		t.Fatalf("SetLanguage(\"\"): %v", err)
	}

	if got := s.Get().Language; got != "fr" {
		t.Fatalf("Language = %q after empty set, want \"fr\"", got)
	}
}

func TestOpenCorruptFileFallsBackToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	err := os.WriteFile(path, []byte("{not valid json"), 0o644)
	if err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	// A corrupt file falls back to defaults but must not be silently discarded:
	// it is preserved as .corrupt so MQTT credentials and other settings can be
	// recovered instead of vanishing.
	s := Open(path)
	if got := s.Get().Language; got != "en" {
		t.Fatalf("corrupt-file Language = %q, want default \"en\"", got)
	}

	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("corrupt file not preserved as .corrupt: %v", err)
	}
}

func TestRepeatedCorruptionKeepsTheLastTwo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, bad := range []string{"first", "second", "third"} {
		err := os.WriteFile(path, []byte(bad), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		Open(path)
	}

	if _, err := os.Stat(path + ".corrupt.2"); err == nil {
		t.Fatal("a third copy was kept")
	}

	for file, want := range map[string]string{
		path + ".corrupt":   "third",
		path + ".corrupt.1": "second",
	} {
		if got, err := os.ReadFile(file); err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want %q", file, got, err, want)
		}
	}
}

func TestFailedWriteDoesNotMutateMemory(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "missing", "settings.json"))

	err := s.SetLanguage("nl")
	if err == nil {
		t.Fatal("SetLanguage succeeded with missing parent")
	}

	if got := s.Get().Language; got != "en" {
		t.Fatalf("Language = %q after failed write, want en", got)
	}
}

func TestFailedNameWriteDoesNotMutateMemory(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "missing", "settings.json"))

	err := s.SetName("Kitchen")
	if err == nil {
		t.Fatal("SetName succeeded with missing parent")
	}

	if got := s.Get().Name; got != "" {
		t.Fatalf("Name = %q after failed write, want empty", got)
	}
}

func TestCorruptSettingsCanBeReplacedAfterQuarantine(t *testing.T) {
	dir := t.TempDir()

	path := filepath.Join(dir, "settings.json")

	err := os.WriteFile(path, []byte("{not json"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	st := Open(path)
	// The unreadable original must be preserved, not discarded.
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("corrupt file not preserved: %v", err)
	}

	// Any ordinary write may create a clean replacement while retaining the backup.
	err = st.SetName("Keuken")
	if err != nil {
		t.Fatal(err)
	}

	if next := Open(path); next.Get().Name != "Keuken" {
		t.Fatalf("name after restart = %q, want Keuken", next.Get().Name)
	}
}

// Volume and bass must survive a restart, and "saved as 0" must be distinguishable
// from "never saved" or a muted speaker would silently come back at the default.
func TestAudioPrefsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	first := Open(path)
	if got := first.Get().Audio; got.HasVolume {
		t.Fatalf("fresh store already claims a saved volume: %+v", got)
	}

	err := first.SetAudio(Audio{Volume: 0, Bass: -20, HasVolume: true})
	if err != nil {
		t.Fatal(err)
	}

	reopened := Open(path).Get().Audio
	if !reopened.HasVolume || reopened.Volume != 0 || reopened.Bass != -20 {
		t.Fatalf("after restart = %+v, want volume 0 / bass -20", reopened)
	}
}

func TestAudioPrefsIgnoreRemovedTreble(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	old := `{"audio":{"volume":42,"bass":-4,"treble":30,"hasVolume":true,"autoOff":45}}`

	err := os.WriteFile(path, []byte(old), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	got := Open(path).Get().Audio
	if !got.HasVolume || got.Volume != 42 || got.Bass != -4 || got.AutoOff != 45 {
		t.Fatalf("old settings loaded as %+v", got)
	}
}

// SetAudio is called from a sampling loop that knows nothing about auto-off, so it
// must not clobber the separately-owned timeout.
func TestSetAudioPreservesAutoOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	st := Open(path)

	err := st.SetAutoOff(45)
	if err != nil {
		t.Fatal(err)
	}

	err = st.SetAudio(Audio{Volume: 60, HasVolume: true})
	if err != nil {
		t.Fatal(err)
	}

	if got := Open(path).Get().Audio; got.AutoOff != 45 || got.Volume != 60 {
		t.Fatalf("got autoOff=%d volume=%d, want 45 / 60", got.AutoOff, got.Volume)
	}
}

// Disabling auto-off needs a value of its own: zero means "not configured".
func TestSetAutoOffDisabledIsNotZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	st := Open(path)

	err := st.SetAutoOff(-5)
	if err != nil {
		t.Fatal(err)
	}

	if got := Open(path).Get().Audio.AutoOff; got >= 0 {
		t.Fatalf("disabled auto-off stored as %d, want negative", got)
	}
}

func TestOldFileMigratesToCurrentSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	err := os.WriteFile(path, []byte(`{"name":"Keuken","language":"nl"}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := Open(path).Get()
	if s.SchemaVersion != SchemaVersion || s.Name != "Keuken" || s.Language != "nl" {
		t.Fatalf("migrated = %+v", s)
	}
}

func TestMigrationsRunInOrder(t *testing.T) {
	saved := migrations
	defer func() { migrations = saved }()
	// A pretend version 2 that renames a field: a version 1 file gets it, a version 2
	// file does not.
	migrations = append(append([]func(map[string]json.RawMessage) error{}, saved...),
		func(raw map[string]json.RawMessage) error {
			raw["name"] = raw["oldName"]

			return nil
		})

	s, err := Migrate([]byte(`{"schemaVersion":1,"oldName":"Zolder"}`))
	if err != nil || s.Name != "Zolder" || s.SchemaVersion != 2 {
		t.Fatalf("v1 -> v2: %+v, %v", s, err)
	}

	s, err = Migrate([]byte(`{"schemaVersion":2,"name":"Keuken","oldName":"Zolder"}`))
	if err != nil || s.Name != "Keuken" {
		t.Fatalf("v2 read as is: %+v, %v", s, err)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	if _, err := Migrate([]byte(`{"schemaVersion":99}`)); err == nil {
		t.Fatal("a file from a newer build was accepted")
	}
}

// After an app rollback the newer build's file stays in place and untouched: this build
// reads what it knows and refuses writes until a reset.
func TestOpenLeavesANewerSchemaFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")

	newer := []byte(`{"schemaVersion":99,"name":"Kitchen","language":"nl","futureField":"keep"}`)

	err := os.WriteFile(path, newer, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := Open(path)
	if got := s.Get(); got.Name != "Kitchen" || got.Language != "nl" {
		t.Fatalf("settings read = %+v", got)
	}

	err = s.SetAudio(Audio{Volume: 25, HasVolume: true})
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("write err = %v, want ErrNewerSchema", err)
	}

	if data, _ := os.ReadFile(path); !bytes.Equal(data, newer) {
		t.Fatalf("file changed: %s", data)
	}

	err = s.Reset()
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
}

// A reset that fails to write keeps the newer-schema guard, so a later ordinary save
// cannot overwrite the newer build's file.
func TestFailedResetKeepsTheNewerSchemaGuard(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "home")

	err := os.Mkdir(dir, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "settings.json")

	newer := []byte(`{"schemaVersion":99,"futureField":"keep"}`)

	err = os.WriteFile(path, newer, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := Open(path)

	err = os.Rename(dir, dir+".gone")
	if err != nil {
		t.Fatal(err)
	}

	err = s.Reset()
	if err == nil {
		t.Fatal("reset into a missing directory succeeded")
	}

	err = os.Rename(dir+".gone", dir)
	if err != nil {
		t.Fatal(err)
	}

	err = s.SetAudio(Audio{Volume: 25, HasVolume: true})
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("write after a failed reset = %v, want ErrNewerSchema", err)
	}

	if data, _ := os.ReadFile(path); !bytes.Equal(data, newer) {
		t.Fatalf("newer file overwritten: %s", data)
	}
}

func TestNormalizeNameFitsOneDNSLabel(t *testing.T) {
	if _, err := NormalizeName(strings.Repeat("\U0001F50A", 16)); err == nil { // 64 bytes
		t.Fatal("a 64-byte name was accepted; Avahi refuses it as a service name")
	}

	if _, err := NormalizeName(strings.Repeat("é", 31) + "x"); err != nil { // 63 bytes
		t.Fatalf("63-byte name refused: %v", err)
	}
}

// Directional formatting characters are invisible and reorder text around the name.
func TestNormalizeNameStripsBidiControls(t *testing.T) {
	got, err := NormalizeName("\u202eKeuken\u2066 1\u2069\u200f")
	if err != nil || got != "Keuken 1" {
		t.Fatalf("NormalizeName = %q, %v; want \"Keuken 1\"", got, err)
	}

	if _, err := NormalizeName("\u202e\u200f"); err == nil {
		t.Fatal("a name of only bidi controls was accepted")
	}
}

// A reset removes every corrupt copy Open kept: they hold the old MQTT password.
func TestResetRemovesCorruptCopies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, p := range []string{path + ".corrupt", path + ".corrupt.1"} {
		err := os.WriteFile(p, []byte(`{"mqtt":{"password":"dummy-secret"`), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	s := Open(path)

	err := s.Reset()
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{path + ".corrupt", path + ".corrupt.1"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s remains after reset: %v", p, err)
		}
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reset settings not written: %v", err)
	}
}
