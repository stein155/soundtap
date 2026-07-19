// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package store is a tiny JSON-file preset store (speaker.PresetSlots slots), safe for
// concurrent HTTP handlers.
package store

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// Preset is one saved station on a slot (1..speaker.PresetSlots).
type Preset struct {
	Slot      int    `json:"slot"`
	StationID string `json:"stationId"` // TuneIn guide id
	Source    string `json:"source,omitempty"`
	Type      string `json:"type,omitempty"`
	Location  string `json:"location,omitempty"`
	Name      string `json:"name"`
	Logo      string `json:"logo,omitempty"`
}

// Store persists presets to a JSON file.
type Store struct {
	path   string
	mu     sync.Mutex
	m      map[int]Preset
	closed bool
	// loadErr is what Open found wrong with the file. When it wraps
	// atomicjson.ErrUnreadable every write is refused with it.
	loadErr error
}

// ErrClosed is returned by writes after Close.
var ErrClosed = errors.New("preset store closed")

// Close stops all later writes, so a factory reset that deletes the file cannot
// have it written back by a save still in flight.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// Open loads (or starts) a store at path. It always returns a usable store: soundtapd
// exits on nothing here, because a crash loop before boot is confirmed sends the speaker
// back to stock. Only a missing file starts empty without an error. A corrupt file is
// moved aside to path.corrupt (the next write would overwrite it) and the store starts
// empty and writable; an unreadable one (EIO, EACCES, a directory) leaves the store
// empty and read-only, since a write would replace presets that may still be on disk.
// Either way the error says what happened (atomicjson.ErrCorrupt or ErrUnreadable).
func Open(path string) (*Store, error) {
	s := &Store{path: path, m: map[int]Preset{}}
	var list []Preset
	s.loadErr = atomicjson.Load(path, &list)
	for _, p := range list {
		if p.Slot >= 1 && p.Slot <= speaker.PresetSlots {
			s.m[p.Slot] = p
		}
	}
	return s, s.loadErr
}

// LoadErr is what Open found wrong with the file, nil when it loaded or was absent.
func (s *Store) LoadErr() error { return s.loadErr }

// All returns the presets sorted by slot.
func (s *Store) All() []Preset {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Preset, 0, len(s.m))
	for _, p := range s.m {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// Get returns the preset on a slot.
func (s *Store) Get(slot int) (Preset, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.m[slot]
	return p, ok
}

// Set saves a preset and persists.
func (s *Store) Set(p Preset) error {
	if p.Slot < 1 || p.Slot > speaker.PresetSlots {
		return fmt.Errorf("slot must be 1..%d", speaker.PresetSlots)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[int]Preset, len(s.m)+1)
	for slot, current := range s.m {
		next[slot] = current
	}
	next[p.Slot] = p
	if err := s.persistLocked(next); err != nil {
		return err
	}
	s.m = next
	return nil
}

// Remove clears a slot and persists.
func (s *Store) Remove(slot int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[int]Preset, len(s.m))
	for currentSlot, current := range s.m {
		if currentSlot != slot {
			next[currentSlot] = current
		}
	}
	if err := s.persistLocked(next); err != nil {
		return err
	}
	s.m = next
	return nil
}

// Swap exchanges what two slots hold, in one write: a power cut leaves the old pair or
// the new one, never a preset on both slots and its neighbour gone. An empty slot
// swaps too, which moves the other one.
func (s *Store) Swap(a, b int) error {
	if a < 1 || a > speaker.PresetSlots || b < 1 || b > speaker.PresetSlots {
		return fmt.Errorf("slot must be 1..%d", speaker.PresetSlots)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[int]Preset, len(s.m))
	for slot, p := range s.m {
		switch slot {
		case a:
			slot = b
		case b:
			slot = a
		}
		p.Slot = slot
		next[slot] = p
	}
	if err := s.persistLocked(next); err != nil {
		return err
	}
	s.m = next
	return nil
}

func (s *Store) persistLocked(presets map[int]Preset) error {
	if s.closed {
		return ErrClosed
	}
	if errors.Is(s.loadErr, atomicjson.ErrUnreadable) {
		return s.loadErr
	}
	out := make([]Preset, 0, len(presets))
	for _, p := range presets {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return atomicjson.Write(s.path, out, 0o644)
}
