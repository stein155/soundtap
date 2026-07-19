// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package favorites_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/favorites"
)

func TestMissingFileStartsEmptyAndWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	s := favorites.Open(path)
	if err := s.LoadErr(); err != nil || len(s.All()) != 0 {
		t.Fatalf("Open = %v, %v; want empty and no error", s.All(), err)
	}
	if err := s.Add(favorites.Entry{Source: "TUNEIN", Location: "s1"}); err != nil {
		t.Fatal(err)
	}
}

// favorites.json as a directory (EISDIR): nothing may be written over it, a restored
// backup included.
func TestUnreadableDirectoryRefusesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	s := favorites.Open(path)
	if err := s.LoadErr(); !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("LoadErr = %v, want ErrUnreadable", err)
	}
	if err := s.Add(favorites.Entry{Source: "TUNEIN", Location: "s1"}); !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Add = %v, want ErrUnreadable", err)
	}
	if err := s.Replace([]favorites.Entry{{Source: "TUNEIN", Location: "s1"}}); !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Replace = %v, want ErrUnreadable", err)
	}
	if got := s.All(); len(got) != 0 {
		t.Fatalf("a refused write changed memory: %v", got)
	}
}

func TestUnreadableFileIsNotOverwritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	path := filepath.Join(t.TempDir(), "favorites.json")
	saved := []byte(`[{"source":"TUNEIN","name":"Radio 1","location":"s1","art":""}]`)
	if err := os.WriteFile(path, saved, 0o000); err != nil {
		t.Fatal(err)
	}
	s := favorites.Open(path)
	if err := s.Add(favorites.Entry{Source: "TUNEIN", Location: "s2"}); !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Add = %v, want ErrUnreadable", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != string(saved) {
		t.Fatalf("unreadable favorites overwritten with %s", data)
	}
}

func TestCorruptFileIsPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := favorites.Open(path)
	if err := s.LoadErr(); !errors.Is(err, atomicjson.ErrCorrupt) {
		t.Fatalf("LoadErr = %v, want ErrCorrupt", err)
	}
	if got, err := os.ReadFile(path + ".corrupt"); err != nil || string(got) != "not json" {
		t.Fatalf("corrupt copy = %q, %v", got, err)
	}
	if err := s.Add(favorites.Entry{Source: "TUNEIN", Location: "s1"}); err != nil {
		t.Fatalf("Add after quarantine: %v", err)
	}
}

// A hand-edited list longer than Limit used to load as empty, and the next Add then
// wrote that over it. It loads whole now; Add refuses until it is shorter.
func TestOverLimitFileLoadsWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	list := make([]favorites.Entry, favorites.Limit+3)
	for i := range list {
		list[i] = favorites.Entry{Source: "TUNEIN", Location: fmt.Sprintf("s%d", i)}
	}
	data, _ := json.Marshal(list)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	s := favorites.Open(path)
	if got := len(s.All()); got != favorites.Limit+3 {
		t.Fatalf("loaded %d favorites, want %d", got, favorites.Limit+3)
	}
	if err := s.Add(favorites.Entry{Source: "TUNEIN", Location: "new"}); !errors.Is(err, favorites.ErrFull) {
		t.Fatalf("Add = %v, want ErrFull", err)
	}
	if err := s.Remove("TUNEIN", "s0"); err != nil {
		t.Fatal(err)
	}
	if got := len(favorites.Open(path).All()); got != favorites.Limit+2 {
		t.Fatalf("after Remove %d on disk, want %d", got, favorites.Limit+2)
	}
}
