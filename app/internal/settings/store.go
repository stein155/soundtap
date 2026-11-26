// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"

	"github.com/stein155/soundtap/app/internal/atomicjson"
)

// Store is a JSON-file-backed settings store, safe for concurrent handlers.
type Store struct {
	path   string
	mu     sync.Mutex
	s      Settings
	closed bool
	// readOnly is set when the file is from a newer build (writing it back would drop
	// the fields this build does not know) or could not be read at all (writing would
	// replace it with defaults). Only Reset lifts it.
	readOnly error
	// loadErr is what Open found wrong with the file, for the support bundle.
	loadErr error
}

// ErrClosed is returned by writes after Close.
var ErrClosed = errors.New("settings store closed")

// Close stops all later writes. A factory reset closes the store before it deletes
// the file, so a save already on its way (the shutdown's last volume) cannot write
// the old settings back.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	return nil
}

// ErrNewerSchema marks a settings file written by a newer build (after an app rollback).
var ErrNewerSchema = errors.New("settings file is from a newer build")

// Why a settings file cannot be read.
var (
	errNotAnObject      = errors.New("settings file is not an object")
	errBadSchemaVersion = errors.New("bad schemaVersion")
)

// settingsFileMode is the settings file's permission: 0600, as the file holds the MQTT
// broker password in the clear.
const settingsFileMode = 0o600

// Migrate brings a raw settings file up to SchemaVersion. A file from a newer build is
// refused rather than read with its unknown fields dropped and then written back.
func Migrate(data []byte) (Settings, error) {
	return migrateWith(data, migrations())
}

// migrateWith is Migrate with the migrations given, so a test can add a pretend one.
func migrateWith(data []byte, migs []migration) (Settings, error) {
	var raw map[string]json.RawMessage

	err := json.Unmarshal(data, &raw)
	if err != nil {
		return Settings{}, fmt.Errorf("parse settings: %w", err)
	}

	if raw == nil {
		return Settings{}, errNotAnObject
	}

	version := 0
	if rawVersion, ok := raw["schemaVersion"]; ok {
		err = json.Unmarshal(rawVersion, &version)
		if err != nil || version < 0 {
			return Settings{}, fmt.Errorf("%w %s", errBadSchemaVersion, rawVersion)
		}
	}

	if version > len(migs) {
		return Settings{}, fmt.Errorf("%w: schema %d, this build reads up to %d", ErrNewerSchema, version, len(migs))
	}

	for ; version < len(migs); version++ {
		err = migs[version](raw)
		if err != nil {
			return Settings{}, fmt.Errorf("migrating settings %d -> %d: %w", version, version+1, err)
		}
	}

	raw["schemaVersion"] = json.RawMessage(strconv.Itoa(len(migs)))

	data, err = json.Marshal(raw)
	if err != nil {
		return Settings{}, fmt.Errorf("re-encoding migrated settings: %w", err)
	}

	var migrated Settings

	migrated.Language = "en"

	err = json.Unmarshal(data, &migrated)
	if err != nil {
		return migrated, fmt.Errorf("decode settings: %w", err)
	}

	return migrated, nil
}

// defaults is the settings of a speaker that never saved any.
func defaults() Settings {
	var fresh Settings

	fresh.SchemaVersion = SchemaVersion
	fresh.Language = "en"

	return fresh
}

// Open loads (or starts) a store at path. Only a missing file starts from defaults.
// A file that cannot be read leaves the store on defaults and read-only, so the next
// save cannot replace settings that may still be intact on disk.
func Open(path string) *Store {
	store := &Store{path: path, mu: sync.Mutex{}, s: defaults(), closed: false, readOnly: nil, loadErr: nil}

	data, found, err := atomicjson.Read(path)
	if err != nil {
		store.loadErr, store.readOnly = err, err
		atomicjson.LogLoad(slog.Default(), "settings", path, err)

		return store
	}

	if !found {
		return store
	}

	store.s, err = Migrate(data)
	if errors.Is(err, ErrNewerSchema) {
		// A rollback, not corruption: leave the file where the newer build will find
		// it, use the fields this build knows, and refuse writes.
		store.s = defaults()
		// Best effort on purpose: the store is read-only, and a field this build cannot
		// decode keeps its default rather than hiding the ones it can.
		_ = json.Unmarshal(data, &store.s)
		store.loadErr, store.readOnly = fmt.Errorf("%w; writes refused until a reset", err), err
		slog.Warn("settings are read-only until a reset", "path", path, "err", err)
	} else if err != nil {
		// A corrupt/truncated file must not silently disappear: preserve it so MQTT
		// credentials and other settings can be recovered. One that cannot be moved
		// aside makes the store read-only, since the next write would destroy it.
		store.s = defaults()

		store.loadErr = atomicjson.Quarantine(path, err)
		if errors.Is(store.loadErr, atomicjson.ErrUnreadable) {
			store.readOnly = store.loadErr
		}

		atomicjson.LogLoad(slog.Default(), "settings", path, store.loadErr)
	}

	return store
}

// LoadErr is what Open found wrong with the file, nil when it loaded or was absent.
// It stays as it was after a Reset: it describes this start, for the support bundle.
func (s *Store) LoadErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.loadErr
}

// Get returns a copy of the current settings.
func (s *Store) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := s.s
	out.SourceNames = append([]SourceName(nil), s.s.SourceNames...)
	out.CrashReports.Handled = slices.Clone(s.s.CrashReports.Handled)

	return out
}

// Reset clears the stored preferences, the speaker's name included. Keeping the name
// was the earlier behaviour and it was wrong: a factory reset that leaves the box
// answering to the name its previous owner gave it has not reset the box. soundtapd
// re-derives the out-of-box name from the serial on the next start, so the speaker
// still has a name — just not that one.
//
// Wi-Fi is not touched here at all: it lives beside this file and is cleared by the
// caller, which also restarts the daemon so the new identity takes effect.
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The newer-schema guard lifts only once the reset is on disk: a failed write
	// leaves the newer file in place, and the next ordinary save must not clobber it.
	guard := s.readOnly

	s.readOnly = nil

	fresh := defaults()

	err := s.replaceLocked(&fresh)
	if err != nil {
		s.readOnly = guard

		return err
	}

	RemoveQuarantine(s.path)

	return nil
}

// RemoveQuarantine deletes every corrupt copy Open kept of the settings at path
// (.corrupt, .corrupt.1): they hold the previous owner's MQTT password.
func RemoveQuarantine(path string) {
	err := atomicjson.RemoveQuarantine(path)
	if err != nil {
		slog.Error("remove settings backup", "path", path, "err", err)
	}
}

// replaceLocked atomically writes next before replacing the in-memory settings.
// Caller holds s.mu.
// 0600: the file holds the MQTT broker password in the clear.
func (s *Store) replaceLocked(next *Settings) error {
	if s.closed {
		return ErrClosed
	}

	if s.readOnly != nil {
		return s.readOnly
	}

	next.SchemaVersion = SchemaVersion

	err := atomicjson.Write(s.path, *next, settingsFileMode)
	if err != nil {
		return err //nolint:wrapcheck // atomicjson names the path and the step; the text reaches the web UI as is
	}

	s.s = *next

	return nil
}
