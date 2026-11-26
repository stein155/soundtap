// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/settings"
)

func TestMissingFileStartsWritableWithoutLoadError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")

	store := settings.Open(path)

	err := store.LoadErr()
	if err != nil {
		t.Fatalf("LoadErr = %v for a missing file, want nil", err)
	}

	err = store.SetLanguage("nl")
	if err != nil {
		t.Fatalf("write to a fresh store: %v", err)
	}

	if got := settings.Open(path).Get().Language; got != "nl" {
		t.Fatalf("Language after reopen = %q, want nl", got)
	}
}

// A settings path that is a directory (EISDIR) is not "no settings": the store keeps
// its defaults in memory, refuses every write and reports why.
func TestUnreadableDirectoryMakesTheStoreReadOnly(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")

	err := os.Mkdir(path, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	store := settings.Open(path)

	err = store.LoadErr()
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("LoadErr = %v, want ErrUnreadable", err)
	}

	if got := store.Get().Language; got != "en" {
		t.Fatalf("Language = %q, want the default en", got)
	}

	err = store.SetLanguage("nl")
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("write = %v, want ErrUnreadable", err)
	}

	if got := store.Get().Language; got != "en" {
		t.Fatalf("a refused write changed memory to %q", got)
	}

	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("settings path = %v, %v; want the directory untouched", info, err)
	}
}

// A settings file the daemon cannot read (EACCES here, EIO on a failing NAND) must
// survive the next save: before, the store started over and that save replaced it.
func TestUnreadableFileIsNotOverwritten(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}

	path := filepath.Join(t.TempDir(), "settings.json")

	owner := []byte(`{"schemaVersion":1,"name":"Kitchen","mqtt":{"password":"SYNTHETIC"}}`)

	err := os.WriteFile(path, owner, 0o000)
	if err != nil {
		t.Fatal(err)
	}

	store := settings.Open(path)

	err = store.SetAudio(settings.Audio{Volume: 30, Bass: 0, HasVolume: true, AutoOff: 0})
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("write = %v, want ErrUnreadable", err)
	}

	err = os.Chmod(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if data, _ := os.ReadFile(path); !bytes.Equal(data, owner) {
		t.Fatalf("unreadable settings overwritten with %s", data)
	}

	if got := settings.Open(path).Get().Name; got != "Kitchen" {
		t.Fatalf("name once readable again = %q, want Kitchen", got)
	}
}

func TestCorruptFileReportsWhereItWent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")

	err := os.WriteFile(path, []byte("{broken"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	store := settings.Open(path)

	err = store.LoadErr()
	if !errors.Is(err, atomicjson.ErrCorrupt) || !strings.Contains(err.Error(), path+".corrupt") {
		t.Fatalf("LoadErr = %v, want ErrCorrupt naming the backup", err)
	}

	err = store.SetLanguage("nl")
	if err != nil {
		t.Fatalf("write after quarantine: %v", err)
	}
}

// A corrupt file that cannot be moved aside is the only copy left, so the store does
// not write over it.
func TestCorruptFileThatCannotBeMovedKeepsTheStoreReadOnly(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "settings.json")

	err := os.WriteFile(path, []byte("{broken"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{path + ".corrupt", path + ".corrupt.1"} {
		err = os.MkdirAll(filepath.Join(dir, "x"), 0o700)
		if err != nil {
			t.Fatal(err)
		}
	}

	store := settings.Open(path)

	err = store.SetLanguage("nl")
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("write = %v, want ErrUnreadable", err)
	}

	if data, _ := os.ReadFile(path); string(data) != "{broken" {
		t.Fatalf("file = %q, want it untouched", data)
	}
}
