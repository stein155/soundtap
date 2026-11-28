// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package favorites_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/favorites"
)

func TestAddReplacesTheFavoriteAndSurvivesReopen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "favorites.json")

	store := favorites.Open(path)
	for _, loc := range []string{"s1", "s2", "s2"} {
		err := store.Add(favorites.Entry{Source: "TUNEIN", Name: loc, Location: loc, Art: ""})
		if err != nil {
			t.Fatal(err)
		}
	}

	all := favorites.Open(path).All()
	if len(all) != favorites.Limit || all[0].Location != "s2" {
		t.Fatalf("got %+v", all)
	}
}

func TestReplaceDropsDuplicates(t *testing.T) {
	t.Parallel()

	store := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))

	entry := favorites.Entry{Source: "TUNEIN", Name: "", Location: "s1", Art: ""}

	err := store.Replace([]favorites.Entry{entry, entry})
	if err != nil {
		t.Fatal(err)
	}

	if got := len(store.All()); got != 1 {
		t.Fatalf("got %d entries", got)
	}
}

func TestRemoveMatchesSourceAndLocation(t *testing.T) {
	t.Parallel()

	store := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))

	both := []favorites.Entry{
		{Source: "TUNEIN", Name: "", Location: "s1", Art: ""},
		{Source: "SPOTIFY", Name: "", Location: "s1", Art: ""},
	}

	err := store.Replace(both)
	if err != nil {
		t.Fatal(err)
	}

	err = store.Remove("SPOTIFY", "s1")
	if err != nil {
		t.Fatal(err)
	}

	all := store.All()
	if len(all) != 1 || all[0].Source != "TUNEIN" {
		t.Fatalf("got %+v", all)
	}
}

// A factory reset closes the list before it removes the file: a save still in flight
// must not write the previous owner's favorite back.
func TestClosedStoreWritesNothing(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "favorites.json")
	store := favorites.Open(path)

	err := store.Add(favorites.Entry{Source: "TUNEIN", Location: "s1", Name: "Radio 1", Art: ""})
	if err != nil {
		t.Fatal(err)
	}

	err = store.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}

	for name, write := range map[string]func() error{
		"Add": func() error {
			return store.Add(favorites.Entry{Source: "TUNEIN", Location: "s2", Name: "Radio 2", Art: ""})
		},
		"Remove":  func() error { return store.Remove("TUNEIN", "s1") },
		"Replace": func() error { return store.Replace(nil) },
	} {
		err = write()
		if !errors.Is(err, favorites.ErrClosed) {
			t.Errorf("%s after Close = %v, want ErrClosed", name, err)
		}

		_, err = os.Stat(path)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s after Close wrote the file back: %v", name, err)
		}
	}
}
