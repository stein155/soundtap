// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package recent maintains a bounded, persistent list of recently-played sources.
//
// When a source stops or is preempted, the caller records an Entry. The list is
// bounded at 50 entries (newest first) and written atomically to a JSON file so
// it survives a reboot.
package recent

import (
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

// maxEntries is the cap on the ring. The API returns at most this many.
const maxEntries = 50

// fileMode is deliberately tighter than the preset store's: this is a log of what
// the user listened to and when, and nothing outside soundtapd reads it.
const fileMode = 0o600

// Entry is one recently-played source.
type Entry struct {
	Source    string    `json:"source"`
	Name      string    `json:"name"`
	Location  string    `json:"location"` // empty for non-replayable sources
	Art       string    `json:"art"`
	StoppedAt time.Time `json:"stoppedAt"`
}

// Store holds the recent list and its backing file.
type Store struct {
	mu      sync.Mutex
	entries []Entry // newest first
	path    string
	// loadErr is what Open found wrong with the file. When it wraps
	// atomicjson.ErrUnreadable every write is refused with it.
	loadErr error
}

// Open loads an existing recent list from path. Only a missing file starts an empty
// list. A malformed one is moved aside (path.corrupt) and the list starts empty; one
// that cannot be read leaves the list empty and read-only, so the next stop cannot
// replace a history that may still be intact on disk.
func Open(path string) *Store {
	s := &Store{path: path}

	var loaded []Entry

	s.loadErr = atomicjson.Load(path, &loaded)
	atomicjson.LogLoad(slog.Default(), "recent list", path, s.loadErr)

	if len(loaded) > maxEntries {
		loaded = loaded[:maxEntries]
	}

	s.entries = loaded

	return s
}

// LoadErr is what Open found wrong with the file, nil when it loaded or was absent.
func (s *Store) LoadErr() error { return s.loadErr }

// write persists next and makes it the list. The caller holds mu.
func (s *Store) write(next []Entry) error {
	if errors.Is(s.loadErr, atomicjson.ErrUnreadable) {
		return s.loadErr
	}
	// atomicjson fsyncs before the rename: a speaker's normal "off" is a power cut,
	// and a plain tmp+rename can leave a zero-length file behind one.
	err := atomicjson.Write(s.path, next, fileMode)
	if err != nil {
		return err
	}

	s.entries = next

	return nil
}

// Record adds e to the front of the list, deduplicating by (source, location) and
// capping at maxEntries, then persists it.
func (s *Store) Record(e Entry) error {
	if e.Source == "" || e.Source == "STANDBY" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Newest first, dropping any earlier entry for the same source+location so one
	// station cannot fill the list.
	next := make([]Entry, 0, min(len(s.entries)+1, maxEntries))

	next = append(next, e)
	for _, x := range s.entries {
		if len(next) == maxEntries {
			break
		}

		if x.Source == e.Source && x.Location == e.Location {
			// A replay that reports no picture keeps the one looked up before: that
			// lookup runs once per station, so a lost logo would not come back.
			if next[0].Art == "" {
				next[0].Art = x.Art
			}

			continue
		}

		next = append(next, x)
	}

	return s.write(next)
}

// SetArt gives the entry for source+location its artwork if it has none yet, and
// persists the list. An entry that is gone or already has art is left alone.
func (s *Store) SetArt(source, location, art string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := slices.IndexFunc(s.entries, func(x Entry) bool {
		return x.Source == source && x.Location == location && x.Art == ""
	})
	if i < 0 {
		return nil
	}

	next := slices.Clone(s.entries)
	next[i].Art = art

	return s.write(next)
}

// Remove drops the entry for source+location, if there is one, and persists the list.
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

	return s.write(next)
}

// LastStation returns the newest TuneIn station in the list: what POWER wakes into
// when standby left nothing to replay.
func (s *Store) LastStation() (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, e := range s.entries {
		if e.Source == "TUNEIN" && e.Location != "" {
			return e, true
		}
	}

	return Entry{}, false
}

// All returns a copy of the list, newest first.
func (s *Store) All() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Entry, len(s.entries))
	copy(out, s.entries)

	return out
}
