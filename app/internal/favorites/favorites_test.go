// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package favorites_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stein155/soundtap/app/internal/favorites"
)

func TestAddKeepsOrderAndPlaceAndSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	s := favorites.Open(path)
	for _, loc := range []string{"s1", "s2", "s1"} {
		if err := s.Add(favorites.Entry{Source: "TUNEIN", Name: loc, Location: loc}); err != nil {
			t.Fatal(err)
		}
	}
	all := favorites.Open(path).All()
	if len(all) != 2 || all[0].Location != "s1" || all[1].Location != "s2" {
		t.Fatalf("got %+v", all)
	}
}

func TestAddStopsAtTheLimit(t *testing.T) {
	s := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))
	for i := range favorites.Limit {
		if err := s.Add(favorites.Entry{Source: "TUNEIN", Location: fmt.Sprintf("s%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	err := s.Add(favorites.Entry{Source: "TUNEIN", Location: "s999"})
	if !errors.Is(err, favorites.ErrFull) {
		t.Fatalf("got %v", err)
	}
}

func TestReplaceDropsDuplicates(t *testing.T) {
	s := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))
	e := favorites.Entry{Source: "TUNEIN", Location: "s1"}
	if err := s.Replace([]favorites.Entry{e, e}); err != nil {
		t.Fatal(err)
	}
	if got := len(s.All()); got != 1 {
		t.Fatalf("got %d entries", got)
	}
}

func TestRemoveMatchesSourceAndLocation(t *testing.T) {
	s := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))
	for _, source := range []string{"TUNEIN", "SPOTIFY"} {
		if err := s.Add(favorites.Entry{Source: source, Location: "s1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Remove("SPOTIFY", "s1"); err != nil {
		t.Fatal(err)
	}
	all := s.All()
	if len(all) != 1 || all[0].Source != "TUNEIN" {
		t.Fatalf("got %+v", all)
	}
}
