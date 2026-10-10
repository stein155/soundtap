// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/store"
)

// backupMigrations[v-1] upgrades a backup from version v to v+1. Empty while version 1
// is the only format; a bump appends its step here.
var backupMigrations = []func(*backup){}

// validate rejects a file that is not a SoundTap backup, and any field that would be
// refused if it were typed into the UI. Validation happens before anything is written
// so a bad file cannot leave the speaker half-restored.
func (b *backup) validate() error {
	if b.Kind != backupKind {
		return fmt.Errorf("not a SoundTap backup (kind %q)", b.Kind)
	}
	if b.Version < 1 || b.Version > backupVersion {
		return fmt.Errorf("backup version %d, this speaker reads versions 1 to %d", b.Version, backupVersion)
	}
	for ; b.Version < backupVersion; b.Version++ {
		backupMigrations[b.Version-1](b)
	}
	if err := b.Settings.validate(); err != nil {
		return err
	}
	if err := validateFavorites(b.Favorites); err != nil {
		return err
	}
	return validatePresets(b.Presets)
}

// validate checks the settings a backup carries, as the settings API would.
func (bs *backupSettings) validate() error { //nolint:gocognit,cyclop // moved unchanged
	if bs.Name != "" {
		if _, err := settings.NormalizeName(bs.Name); err != nil {
			return fmt.Errorf("name: %w", err)
		}
	}
	if bs.Language != "" && !settings.ValidLanguage(bs.Language) {
		return fmt.Errorf("language %q is not a language code", bs.Language)
	}
	if d := bs.Display; d != nil && *d != (settings.Display{}) {
		if _, err := settings.NormalizeDisplay(*d); err != nil {
			return fmt.Errorf("display: %w", err)
		}
	}
	if c := bs.Channel; c != nil && *c != "" && !slices.Contains(channels, *c) {
		return fmt.Errorf("channel %q is not stereo, left, right or mono", *c)
	}
	if n := bs.AuxName; n != nil && strings.TrimSpace(*n) != "" {
		if _, err := settings.NormalizeName(*n); err != nil {
			return fmt.Errorf("auxName: %w", err)
		}
	}
	if a := bs.Alarm; a != nil {
		if err := a.Validate(); err != nil {
			return fmt.Errorf("alarm: %w", err)
		}
	}
	// As PUT /api/settings and /api/volume refuse them. Bass is left to the
	// live clamp: its range is the model's, a backup may come from another model, and the
	// prefs saver stores the clamped value seconds later.
	if a := bs.Audio; a.AutoOff > settings.MaxAutoOff {
		return fmt.Errorf("autoOff %d minutes is longer than %d", a.AutoOff, settings.MaxAutoOff)
	}
	if a := bs.Audio; a.HasVolume && (a.Volume < 0 || a.Volume > 100) {
		return fmt.Errorf("volume %d is outside 0..100", a.Volume)
	}
	if err := bs.MQTT.stored().Validate(); err != nil {
		return fmt.Errorf("mqtt: %w", err)
	}
	return validateSourceNames(bs.SourceNames)
}

func validateSourceNames(names []settings.SourceName) error {
	if len(names) > 16 {
		return errors.New("more source names than the speaker has sources")
	}
	for _, n := range names {
		if _, err := settings.NormalizeName(n.Name); err != nil {
			return fmt.Errorf("source name: %w", err)
		}
		for _, v := range []string{n.Source, n.Account} {
			if v == "" || !utf8.ValidString(v) || utf8.RuneCountInString(v) > 128 || strings.IndexFunc(v, unicode.IsControl) >= 0 {
				return fmt.Errorf("source name for %q: bad source or account", n.Source)
			}
		}
	}
	return nil
}

func validateFavorites(favs []favorites.Entry) error {
	if len(favs) > favorites.Limit {
		return errors.New("more favorites than a speaker keeps")
	}
	for _, e := range favs {
		if err := checkFavorite(e); err != nil {
			return err
		}
	}
	return nil
}

func validatePresets(presets []store.Preset) error {
	if len(presets) > speaker.PresetSlots {
		return errors.New("more presets than the speaker has slots")
	}
	seen := map[int]bool{}
	for _, p := range presets {
		if p.Slot < 1 || p.Slot > speaker.PresetSlots {
			return fmt.Errorf("preset slot %d is outside 1..%d", p.Slot, speaker.PresetSlots)
		}
		if seen[p.Slot] {
			return fmt.Errorf("two presets claim slot %d", p.Slot)
		}
		seen[p.Slot] = true
		if err := speaker.CheckPreset(p.Name, p.Logo); err != nil {
			return fmt.Errorf("preset %d: %w", p.Slot, err)
		}
		if err := checkBackupPresetSource(p); err != nil {
			return err
		}
	}
	return nil
}

// checkBackupPresetSource checks that a preset names a source the speaker plays and a
// location that source accepts.
func checkBackupPresetSource(p store.Preset) error {
	switch p.Source {
	case sourceUPnP:
		u, err := url.Parse(p.Location)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("preset %d has no valid UPnP location", p.Slot)
		}
	case sourceSpotify:
		if !validSpotifyPreset(p.Location) {
			return fmt.Errorf("preset %d has no valid Spotify URI", p.Slot)
		}
	case "", sourceTuneIn:
		// No DNS here: a stream URL is checked again when it plays.
		if !validStationID(p.StationID) && !validOwnStream(p.StationID) {
			return fmt.Errorf("preset %d has no valid station id", p.Slot)
		}
	default:
		return fmt.Errorf("preset %d has unsupported source", p.Slot)
	}
	return nil
}
