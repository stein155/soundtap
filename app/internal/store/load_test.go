// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package store_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/store"
)

// presets.json as a directory (EISDIR): an empty, read-only store and the reason, so
// soundtapd starts but cannot write empty presets over the owner'presets.
func TestUnreadableDirectoryRefusesWrites(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "presets.json")

	err := os.Mkdir(path, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	presets, err := store.Open(path)
	if !errors.Is(err, atomicjson.ErrUnreadable) || presets == nil || len(presets.All()) != 0 {
		t.Fatalf("Open = %v, %v; want an empty store and ErrUnreadable", presets, err)
	}

	if !errors.Is(presets.LoadErr(), atomicjson.ErrUnreadable) {
		t.Fatalf("LoadErr = %v, want ErrUnreadable", presets.LoadErr())
	}

	for name, write := range map[string]func() error{
		"Set":    func() error { return presets.Set(preset(1, "s1", "x")) },
		"Remove": func() error { return presets.Remove(1) },
		"Swap":   func() error { return presets.Swap(1, 2) },
	} {
		err := write()
		if !errors.Is(err, atomicjson.ErrUnreadable) {
			t.Errorf("%s = %v, want ErrUnreadable", name, err)
		}
	}

	if _, ok := presets.Get(1); ok {
		t.Fatal("a refused Set changed memory")
	}
}

func TestUnreadableFileIsNotOverwritten(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}

	path := filepath.Join(t.TempDir(), "presets.json")

	saved := []byte(`[{"slot":1,"stationId":"s1","name":"Radio 1"}]`)

	err := os.WriteFile(path, saved, 0o000)
	if err != nil {
		t.Fatal(err)
	}

	presets, err := store.Open(path)
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Open = %v, want ErrUnreadable", err)
	}

	err = presets.Remove(1)
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Remove = %v, want ErrUnreadable", err)
	}

	err = os.Chmod(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if data, _ := os.ReadFile(path); !bytes.Equal(data, saved) {
		t.Fatalf("unreadable presets overwritten with %s", data)
	}
}

func TestMissingFileHasNoLoadError(t *testing.T) {
	t.Parallel()

	presets, err := store.Open(filepath.Join(t.TempDir(), "presets.json"))
	if err != nil || presets.LoadErr() != nil {
		t.Fatalf("Open = %v, LoadErr = %v; want nil for a missing file", err, presets.LoadErr())
	}
}

func TestCorruptFileIsWritableAfterQuarantine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "presets.json")

	err := os.WriteFile(path, []byte("{"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	presets, err := store.Open(path)
	if !errors.Is(err, atomicjson.ErrCorrupt) || errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Open = %v, want ErrCorrupt only", err)
	}

	err = presets.Set(preset(1, "s1", "x"))
	if err != nil {
		t.Fatalf("Set after quarantine: %v", err)
	}
}
