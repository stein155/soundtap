// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package store is a tiny JSON-file preset store (speaker.PresetSlots slots), safe for
// concurrent HTTP handlers.
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
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

// fileMode is presets.json's mode: the web UI's presets are no secret.
const fileMode fs.FileMode = 0o644

// slotRangeError refuses a slot outside 1..speaker.PresetSlots.
type slotRangeError struct{}

func (slotRangeError) Error() string {
	return fmt.Sprintf("slot must be 1..%d", speaker.PresetSlots)
}

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
	opened := &Store{path: path, mu: sync.Mutex{}, m: map[int]Preset{}, closed: false, loadErr: nil}

	var list []Preset

	opened.loadErr = atomicjson.Load(path, &list)
	for _, preset := range list {
		if preset.Slot >= 1 && preset.Slot <= speaker.PresetSlots {
			opened.m[preset.Slot] = preset
		}
	}

	return opened, opened.loadErr
}

// LoadErr is what Open found wrong with the file, nil when it loaded or was absent.
func (s *Store) LoadErr() error { return s.loadErr }

// All returns the presets sorted by slot.
func (s *Store) All() []Preset {
	s.mu.Lock()
	defer s.mu.Unlock()

	return bySlot(s.m)
}

// Get returns the preset on a slot.
func (s *Store) Get(slot int) (Preset, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.m[slot]

	return p, ok
}

// Set saves a preset and persists.
func (s *Store) Set(preset Preset) error { //nolint:gocritic // callers pass presets by value
	if preset.Slot < 1 || preset.Slot > speaker.PresetSlots {
		return slotRangeError{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	next := make(map[int]Preset, len(s.m)+1)
	maps.Copy(next, s.m)

	next[preset.Slot] = preset

	err := s.persistLocked(next)
	if err != nil {
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

	err := s.persistLocked(next)
	if err != nil {
		return err
	}

	s.m = next

	return nil
}

// Swap exchanges what two slots hold, in one write: a power cut leaves the old pair or
// the new one, never a preset on both slots and its neighbour gone. An empty slot
// swaps too, which moves the other one.
func (s *Store) Swap(first, second int) error {
	if first < 1 || first > speaker.PresetSlots || second < 1 || second > speaker.PresetSlots {
		return slotRangeError{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	next := make(map[int]Preset, len(s.m))
	for slot, preset := range s.m {
		switch slot {
		case first:
			slot = second
		case second:
			slot = first
		default: // every other slot keeps its preset
		}

		preset.Slot = slot
		next[slot] = preset
	}

	err := s.persistLocked(next)
	if err != nil {
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

	err := atomicjson.Write(s.path, bySlot(presets), fileMode)
	if err != nil {
		return fmt.Errorf("save presets: %w", err)
	}

	return nil
}

// bySlot lists the presets in slot order. Every key is a valid slot: Open drops the
// others and Set and Swap refuse them.
func bySlot(presets map[int]Preset) []Preset {
	out := make([]Preset, 0, len(presets))

	for slot := 1; slot <= speaker.PresetSlots; slot++ {
		if preset, ok := presets[slot]; ok {
			out = append(out, preset)
		}
	}

	return out
}
