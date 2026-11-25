// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package recent_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/recent"
)

func TestMissingFileStartsEmptyAndWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent.json")

	s := recent.Open(path)

	err := s.LoadErr()
	if err != nil || len(s.All()) != 0 {
		t.Fatalf("Open = %v, %v; want empty and no error", s.All(), err)
	}

	err = s.Record(recent.Entry{Source: "TUNEIN", Location: "s1"})
	if err != nil {
		t.Fatal(err)
	}

	if got := recent.Open(path).All(); len(got) != 1 {
		t.Fatalf("after reopen = %v, want one entry", got)
	}
}

// recent.json as a directory (EISDIR): the list starts empty, but no write may go
// through, and the reason is on the store for the support bundle.
func TestUnreadableDirectoryRefusesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent.json")

	err := os.Mkdir(path, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	s := recent.Open(path)

	err = s.LoadErr()
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("LoadErr = %v, want ErrUnreadable", err)
	}

	err = s.Record(recent.Entry{Source: "TUNEIN", Location: "s1"})
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Record = %v, want ErrUnreadable", err)
	}

	if got := s.All(); len(got) != 0 {
		t.Fatalf("a refused Record changed memory: %v", got)
	}

	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("recent path = %v, %v; want the directory untouched", info, err)
	}
}

// The history the daemon could not read (EACCES here, EIO on a failing NAND) is still
// there once the file reads again: the next stop did not replace it.
func TestUnreadableFileIsNotOverwritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}

	path := filepath.Join(t.TempDir(), "recent.json")

	history := []byte(`[{"source":"TUNEIN","name":"Radio 1","location":"s1"}]`)

	err := os.WriteFile(path, history, 0o000)
	if err != nil {
		t.Fatal(err)
	}

	s := recent.Open(path)

	err = s.Record(recent.Entry{Source: "TUNEIN", Location: "s2"})
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Record = %v, want ErrUnreadable", err)
	}

	err = s.Remove("TUNEIN", "s1")
	if err != nil {
		t.Fatalf("Remove of an entry not in memory = %v, want a no-op", err)
	}

	err = os.Chmod(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if data, _ := os.ReadFile(path); !bytes.Equal(data, history) {
		t.Fatalf("unreadable history overwritten with %s", data)
	}
}

// A malformed list is kept as recent.json.corrupt rather than dropped, and the store
// starts empty and writable.
func TestCorruptFileIsPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent.json")

	err := os.WriteFile(path, []byte(`[{"source":"TUNEIN"`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	s := recent.Open(path)

	err = s.LoadErr()
	if !errors.Is(err, atomicjson.ErrCorrupt) {
		t.Fatalf("LoadErr = %v, want ErrCorrupt", err)
	}

	if got, err := os.ReadFile(path + ".corrupt"); err != nil || string(got) != `[{"source":"TUNEIN"` {
		t.Fatalf("corrupt copy = %q, %v", got, err)
	}

	err = s.Record(recent.Entry{Source: "TUNEIN", Location: "s1"})
	if err != nil {
		t.Fatalf("Record after quarantine: %v", err)
	}
}
