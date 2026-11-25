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

// Why a backup is refused. Each is wrapped with the field and value it is about.
var (
	errNotBackup          = errors.New("not a SoundTap backup")
	errBackupVersion      = errors.New("backup version")
	errNotLanguage        = errors.New("is not a language code")
	errBadChannel         = errors.New("is not stereo, left, right or mono")
	errAutoOffTooLong     = errors.New("minutes is longer than")
	errVolumeRange        = errors.New("is outside 0..100")
	errTooManySourceNames = errors.New("more source names than the speaker has sources")
	errBadSourceName      = errors.New("bad source or account")
	errTooManyFavorites   = errors.New("more favorites than a speaker keeps")
	errTooManyPresets     = errors.New("more presets than the speaker has slots")
	errOutside            = errors.New("is outside")
	errSlotTaken          = errors.New("two presets claim slot")
	errNoUPnPLocation     = errors.New("has no valid UPnP location")
	errNoSpotifyURI       = errors.New("has no valid Spotify URI")
	errNoStationID        = errors.New("has no valid station id")
	errUnsupportedSource  = errors.New("has unsupported source")
)

// maxSourceNameRunes is the longest source or account a source name may carry.
const maxSourceNameRunes = 128

// maxSourceNames is the most source names a backup may carry.
const maxSourceNames = 16

// backupMigrations[v-1] upgrades a backup from version v to v+1. Empty while version 1
// is the only format; a bump appends its step here.
func backupMigrations() []func(*backup) { return []func(*backup){} }

// validate rejects a file that is not a SoundTap backup, and any field that would be
// refused if it were typed into the UI. Validation happens before anything is written
// so a bad file cannot leave the speaker half-restored.
func (b *backup) validate() error {
	if b.Kind != backupKind {
		return fmt.Errorf("%w (kind %q)", errNotBackup, b.Kind)
	}

	if b.Version < 1 || b.Version > backupVersion {
		return fmt.Errorf("%w %d, this speaker reads versions 1 to %d", errBackupVersion, b.Version, backupVersion)
	}

	for ; b.Version < backupVersion; b.Version++ {
		backupMigrations()[b.Version-1](b)
	}

	err := b.Settings.validate()
	if err != nil {
		return err
	}

	err = validateFavorites(b.Favorites)
	if err != nil {
		return err
	}

	return validatePresets(b.Presets)
}

// validate checks the settings a backup carries, as the settings API would.
func (bs *backupSettings) validate() error { //nolint:gocognit,cyclop // moved unchanged
	if bs.Name != "" {
		_, err := settings.NormalizeName(bs.Name)
		if err != nil {
			return fmt.Errorf("name: %w", err)
		}
	}

	if bs.Language != "" && !settings.ValidLanguage(bs.Language) {
		return fmt.Errorf("language %q %w", bs.Language, errNotLanguage)
	}

	if d := bs.Display; d != nil && *d != (settings.Display{ClockOff: false, Hour12: false, Timezone: ""}) {
		_, err := settings.NormalizeDisplay(*d)
		if err != nil {
			return fmt.Errorf("display: %w", err)
		}
	}

	if c := bs.Channel; c != nil && *c != "" && !slices.Contains(channels(), *c) {
		return fmt.Errorf("channel %q %w", *c, errBadChannel)
	}

	if n := bs.AuxName; n != nil && strings.TrimSpace(*n) != "" {
		_, err := settings.NormalizeName(*n)
		if err != nil {
			return fmt.Errorf("auxName: %w", err)
		}
	}

	if a := bs.Alarm; a != nil {
		err := a.Validate()
		if err != nil {
			return fmt.Errorf("alarm: %w", err)
		}
	}
	// As PUT /api/settings and /api/volume refuse them. Bass is left to the
	// live clamp: its range is the model's, a backup may come from another model, and the
	// prefs saver stores the clamped value seconds later.
	if a := bs.Audio; a.AutoOff > settings.MaxAutoOff {
		return fmt.Errorf("autoOff %d %w %d", a.AutoOff, errAutoOffTooLong, settings.MaxAutoOff)
	}

	if a := bs.Audio; a.HasVolume && (a.Volume < 0 || a.Volume > 100) {
		return fmt.Errorf("volume %d %w", a.Volume, errVolumeRange)
	}

	err := bs.MQTT.stored().Validate()
	if err != nil {
		return fmt.Errorf("mqtt: %w", err)
	}

	return validateSourceNames(bs.SourceNames)
}

func validateSourceNames(names []settings.SourceName) error {
	if len(names) > maxSourceNames {
		return errTooManySourceNames
	}

	for _, name := range names {
		_, err := settings.NormalizeName(name.Name)
		if err != nil {
			return fmt.Errorf("source name: %w", err)
		}

		for _, v := range []string{name.Source, name.Account} {
			if v == "" || !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxSourceNameRunes ||
				strings.IndexFunc(v, unicode.IsControl) >= 0 {
				return fmt.Errorf("source name for %q: %w", name.Source, errBadSourceName)
			}
		}
	}

	return nil
}

func validateFavorites(favs []favorites.Entry) error {
	if len(favs) > favorites.MaxStored {
		return errTooManyFavorites
	}

	for _, e := range favs {
		err := checkFavorite(e)
		if err != nil {
			return err
		}
	}

	return nil
}

func validatePresets(presets []store.Preset) error {
	if len(presets) > speaker.PresetSlots {
		return errTooManyPresets
	}

	seen := map[int]bool{}

	for _, preset := range presets {
		if preset.Slot < 1 || preset.Slot > speaker.PresetSlots {
			return fmt.Errorf("preset slot %d %w 1..%d", preset.Slot, errOutside, speaker.PresetSlots)
		}

		if seen[preset.Slot] {
			return fmt.Errorf("%w %d", errSlotTaken, preset.Slot)
		}

		seen[preset.Slot] = true

		err := speaker.CheckPreset(preset.Name, preset.Logo)
		if err != nil {
			return fmt.Errorf("preset %d: %w", preset.Slot, err)
		}

		err = checkBackupPresetSource(&preset)
		if err != nil {
			return err
		}
	}

	return nil
}

// checkBackupPresetSource checks that a preset names a source the speaker plays and a
// location that source accepts.
func checkBackupPresetSource(preset *store.Preset) error {
	switch preset.Source {
	case sourceUPnP:
		u, err := url.Parse(preset.Location)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("preset %d %w", preset.Slot, errNoUPnPLocation)
		}
	case sourceSpotify:
		if !validSpotifyPreset(preset.Location) {
			return fmt.Errorf("preset %d %w", preset.Slot, errNoSpotifyURI)
		}
	case "", sourceTuneIn:
		// No DNS here: a stream URL is checked again when it plays.
		if !validStationID(preset.StationID) && !validOwnStream(preset.StationID) {
			return fmt.Errorf("preset %d %w", preset.Slot, errNoStationID)
		}
	default:
		return fmt.Errorf("preset %d %w", preset.Slot, errUnsupportedSource)
	}

	return nil
}
