// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package favorites keeps the speaker's saved favorites: what the six presets have no room
// for. Stock kept them in Bose's cloud; here they live on the speaker, in the order they
// were saved.
package favorites

import (
	"errors"
	"log/slog"
	"sync"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

// Limit is how many favorites the list offers: one, beside the six presets. A new one takes
// its place.
const Limit = 1

// MaxStored is the most a list may hold: stock's 100 stored items less the six presets, which
// a list saved before the list offered one, or its backup, can still hold. Such a list loads
// and restores whole, the first entry being the favorite; the next Add or Remove of the
// favorite leaves it at Limit or less.
const MaxStored = 94

// fileMode matches the recent list's: what the user listens to is theirs.
const fileMode = 0o600

// Entry is one favorite. Location is a TuneIn guide id, a stream URL or a Spotify URI.
type Entry struct {
	Source   string `json:"source"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Art      string `json:"art"`
}

// Store holds the list and its backing file.
type Store struct {
	mu      sync.Mutex
	entries []Entry
	path    string
	// loadErr is what Open found wrong with the file. When it wraps
	// atomicjson.ErrUnreadable every write is refused with it.
	loadErr error
}

// Open loads the list from path. Only a missing file starts an empty list. A malformed
// one is moved aside (path.corrupt) and the list starts empty; one that cannot be read
// leaves the list empty and read-only, so the next save cannot replace favorites that
// may still be intact on disk. A list longer than Limit (saved before the list offered one,
// or hand-edited) loads whole and unchanged; the first of it is the favorite.
func Open(path string) *Store {
	s := &Store{path: path}
	s.loadErr = atomicjson.Load(path, &s.entries)
	atomicjson.LogLoad(slog.Default(), "favorites", path, s.loadErr)

	return s
}

// LoadErr is what Open found wrong with the file, nil when it loaded or was absent.
func (s *Store) LoadErr() error { return s.loadErr }

// save persists next, then makes it the list. The caller holds mu.
func (s *Store) save(next []Entry) error {
	if errors.Is(s.loadErr, atomicjson.ErrUnreadable) {
		return s.loadErr
	}

	err := atomicjson.Write(s.path, next, fileMode)
	if err != nil {
		return err
	}

	s.entries = next

	return nil
}

// Add makes e the favorite: the list becomes e alone. Entries a longer list kept behind the
// first, which nothing shows and nothing could remove, go with it.
func (s *Store) Add(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.entries) == 1 && same(s.entries[0], e) {
		return nil
	}

	return s.save([]Entry{e})
}

// same says whether a and b are one favorite: the same thing from the same source.
func same(a, b Entry) bool { return a.Source == b.Source && a.Location == b.Location }

// Remove drops the entry for source+location, if there is one. Removing the favorite, the
// first entry, empties the list: what a longer list held behind it was out of sight.
func (s *Store) Remove(source, location string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	gone := Entry{Source: source, Location: location}
	if len(s.entries) > 0 && same(s.entries[0], gone) {
		return s.save([]Entry{})
	}

	next := make([]Entry, 0, len(s.entries))
	for _, x := range s.entries {
		if !same(x, gone) {
			next = append(next, x)
		}
	}

	if len(next) == len(s.entries) {
		return nil
	}

	return s.save(next)
}

// Replace makes list the whole list, as a restored backup does. A hand-edited file can name
// one favorite twice; the first stays.
func (s *Store) Replace(list []Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := map[[2]string]bool{}

	next := make([]Entry, 0, len(list))
	for _, e := range list {
		key := [2]string{e.Source, e.Location}
		if !seen[key] {
			seen[key] = true

			next = append(next, e)
		}
	}

	return s.save(next)
}

// All returns a copy of the list.
func (s *Store) All() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Entry, len(s.entries))
	copy(out, s.entries)

	return out
}
