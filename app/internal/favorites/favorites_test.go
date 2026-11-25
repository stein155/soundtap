// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package favorites_test

import (
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/favorites"
)

func TestAddReplacesTheFavoriteAndSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")

	s := favorites.Open(path)
	for _, loc := range []string{"s1", "s2", "s2"} {
		err := s.Add(favorites.Entry{Source: "TUNEIN", Name: loc, Location: loc})
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
	s := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))

	e := favorites.Entry{Source: "TUNEIN", Location: "s1"}

	err := s.Replace([]favorites.Entry{e, e})
	if err != nil {
		t.Fatal(err)
	}

	if got := len(s.All()); got != 1 {
		t.Fatalf("got %d entries", got)
	}
}

func TestRemoveMatchesSourceAndLocation(t *testing.T) {
	s := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))

	both := []favorites.Entry{{Source: "TUNEIN", Location: "s1"}, {Source: "SPOTIFY", Location: "s1"}}

	err := s.Replace(both)
	if err != nil {
		t.Fatal(err)
	}

	err = s.Remove("SPOTIFY", "s1")
	if err != nil {
		t.Fatal(err)
	}

	all := s.All()
	if len(all) != 1 || all[0].Source != "TUNEIN" {
		t.Fatalf("got %+v", all)
	}
}
