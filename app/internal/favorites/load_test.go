// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package favorites_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/favorites"
)

func TestMissingFileStartsEmptyAndWritable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "favorites.json")

	store := favorites.Open(path)

	err := store.LoadErr()
	if err != nil || len(store.All()) != 0 {
		t.Fatalf("Open = %v, %v; want empty and no error", store.All(), err)
	}

	err = store.Add(favorites.Entry{Source: "TUNEIN", Name: "", Location: "s1", Art: ""})
	if err != nil {
		t.Fatal(err)
	}
}

// favorites.json as a directory (EISDIR): nothing may be written over it, a restored
// backup included.
func TestUnreadableDirectoryRefusesWrites(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "favorites.json")

	err := os.Mkdir(path, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	store := favorites.Open(path)

	err = store.LoadErr()
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("LoadErr = %v, want ErrUnreadable", err)
	}

	err = store.Add(favorites.Entry{Source: "TUNEIN", Name: "", Location: "s1", Art: ""})
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Add = %v, want ErrUnreadable", err)
	}

	err = store.Replace([]favorites.Entry{{Source: "TUNEIN", Name: "", Location: "s1", Art: ""}})
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Replace = %v, want ErrUnreadable", err)
	}

	if got := store.All(); len(got) != 0 {
		t.Fatalf("a refused write changed memory: %v", got)
	}
}

func TestUnreadableFileIsNotOverwritten(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}

	path := filepath.Join(t.TempDir(), "favorites.json")

	saved := []byte(`[{"source":"TUNEIN","name":"Radio 1","location":"s1","art":""}]`)

	err := os.WriteFile(path, saved, 0o000)
	if err != nil {
		t.Fatal(err)
	}

	store := favorites.Open(path)

	err = store.Add(favorites.Entry{Source: "TUNEIN", Name: "", Location: "s2", Art: ""})
	if !errors.Is(err, atomicjson.ErrUnreadable) {
		t.Fatalf("Add = %v, want ErrUnreadable", err)
	}

	err = os.Chmod(path, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	if data, _ := os.ReadFile(path); !bytes.Equal(data, saved) {
		t.Fatalf("unreadable favorites overwritten with %s", data)
	}
}

func TestCorruptFileIsPreserved(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "favorites.json")

	err := os.WriteFile(path, []byte("not json"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	store := favorites.Open(path)

	err = store.LoadErr()
	if !errors.Is(err, atomicjson.ErrCorrupt) {
		t.Fatalf("LoadErr = %v, want ErrCorrupt", err)
	}

	got, readErr := os.ReadFile(path + ".corrupt")
	if readErr != nil || string(got) != "not json" {
		t.Fatalf("corrupt copy = %q, %v", got, readErr)
	}

	err = store.Add(favorites.Entry{Source: "TUNEIN", Name: "", Location: "s1", Art: ""})
	if err != nil {
		t.Fatalf("Add after quarantine: %v", err)
	}
}

// A list longer than Limit (saved before the list offered one, or hand-edited) loads whole.
// Setting a favorite leaves that one alone, so nothing stays behind out of sight.
func TestOverLimitFileLoadsWholeAndAddLeavesOne(t *testing.T) {
	t.Parallel()

	path := overLimitFile(t)

	store := favorites.Open(path)
	if got := len(store.All()); got != favorites.Limit+3 {
		t.Fatalf("loaded %d favorites, want %d", got, favorites.Limit+3)
	}

	err := store.Add(favorites.Entry{Source: "TUNEIN", Name: "", Location: "s2", Art: ""})
	if err != nil {
		t.Fatalf("Add = %v", err)
	}

	if got := locations(favorites.Open(path).All()); got != "s2" {
		t.Fatalf("on disk %q, want %q", got, "s2")
	}
}

// Removing the favorite of a longer list empties it; removing one behind it drops only that.
func TestOverLimitFileRemoveTheFavoriteEmptiesIt(t *testing.T) {
	t.Parallel()

	path := overLimitFile(t)

	store := favorites.Open(path)

	err := store.Remove("TUNEIN", "s2")
	if err != nil {
		t.Fatalf("Remove = %v", err)
	}

	if got := locations(favorites.Open(path).All()); got != "s0 s1 s3" {
		t.Fatalf("after removing one behind the favorite: %q", got)
	}

	err = store.Remove("TUNEIN", "s0")
	if err != nil {
		t.Fatalf("Remove = %v", err)
	}

	if got := favorites.Open(path).All(); len(got) != 0 {
		t.Fatalf("after removing the favorite: %+v, want an empty list", got)
	}
}

// overLimitFile writes Limit+3 favorites, s0 first.
func overLimitFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "favorites.json")

	list := make([]favorites.Entry, favorites.Limit+3)
	for i := range list {
		list[i] = favorites.Entry{Source: "TUNEIN", Name: "", Location: fmt.Sprintf("s%d", i), Art: ""}
	}

	data, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

func locations(all []favorites.Entry) string {
	got := make([]string, 0, len(all))
	for _, e := range all {
		got = append(got, e.Location)
	}

	return strings.Join(got, " ")
}
