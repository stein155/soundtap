// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

// presets.json as a directory (EISDIR): an empty, read-only store and the reason, so
// soundtapd starts but cannot write empty presets over the owner's.
func TestUnreadableDirectoryRefusesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if !errors.Is(err, atomicjson.ErrUnreadable) || s == nil || len(s.All()) != 0 {
		t.Fatalf("Open = %v, %v; want an empty store and ErrUnreadable", s, err)
	}
	if !errors.Is(s.LoadErr(), atomicjson.ErrUnreadable) {
		t.Fatalf("LoadErr = %v, want ErrUnreadable", s.LoadErr())
	}
	for name, write := range map[string]func() error{
		"Set":    func() error { return s.Set(Preset{Slot: 1, StationID: "s1", Name: "x"}) },
		"Remove": func() error { return s.Remove(1) },
		"Swap":   func() error { return s.Swap(1, 2) },
	} {
		if err := write(); !errors.Is(err, atomicjson.ErrUnreadable) {
			t.Errorf("%s = %v, want ErrUnreadable", name, err)
		}
	}
	if _, ok := s.Get(1); ok {
		t.Fatal("a refused Set changed memory")
	}
}

func TestUnreadableFileIsNotOverwritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	path := filepath.Join(t.TempDir(), "presets.json")
	saved := []byte(`[{"slot":1,"stationId":"s1","name":"Radio 1"}]`)
	if err := os.WriteFile(path, saved, 0o000); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Open = %v, want ErrUnreadable", err)
	}
	if err := s.Remove(1); !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Remove = %v, want ErrUnreadable", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != string(saved) {
		t.Fatalf("unreadable presets overwritten with %s", data)
	}
}

func TestMissingFileHasNoLoadError(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "presets.json"))
	if err != nil || s.LoadErr() != nil {
		t.Fatalf("Open = %v, LoadErr = %v; want nil for a missing file", err, s.LoadErr())
	}
}

func TestCorruptFileIsWritableAfterQuarantine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if !errors.Is(err, atomicjson.ErrCorrupt) || errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Open = %v, want ErrCorrupt only", err)
	}
	if err := s.Set(Preset{Slot: 1, StationID: "s1", Name: "x"}); err != nil {
		t.Fatalf("Set after quarantine: %v", err)
	}
}
