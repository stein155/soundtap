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

// Limit is stock's: 100 stored items, six of them presets.
const Limit = 94

// fileMode matches the recent list's: what the user listens to is theirs.
const fileMode = 0o600

// ErrFull says the list is at Limit.
var ErrFull = errors.New("favorites list is full")

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
// may still be intact on disk. A list longer than Limit (hand-edited) loads whole: Add
// refuses until it is shorter, and nothing on it is dropped.
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
	if err := atomicjson.Write(s.path, next, fileMode); err != nil {
		return err
	}
	s.entries = next
	return nil
}

// Add puts e at the end of the list. What is already there keeps its place.
func (s *Store) Add(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.entries {
		if x.Source == e.Source && x.Location == e.Location {
			return nil
		}
	}
	if len(s.entries) >= Limit {
		return ErrFull
	}
	return s.save(append(s.entries[:len(s.entries):len(s.entries)], e))
}

// Remove drops the entry for source+location, if there is one.
func (s *Store) Remove(source, location string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]Entry, 0, len(s.entries))
	for _, x := range s.entries {
		if x.Source != source || x.Location != location {
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
