// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package settings persists SoundTap's own preferences, one versioned JSON file with
// migrations: the speaker's name and the names of its inputs, the UI language, the volume
// and bass restored at boot, the ST20 standby display, the alarm, the multiroom
// preferences, the integrations other apps play through, the stereo channel, the AUX
// input's name, the MQTT bridge and the crash report opt-in. Playback state, presets and
// favourites live elsewhere.
package settings

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// migration upgrades a raw settings object by one version.
type migration func(map[string]json.RawMessage) error

// migrations()[v] upgrades a raw settings object from version v to v+1, so a new format
// is one appended function plus its test. Files written before the version
// existed are version 0; 0 -> 1 only adds it.
func migrations() []migration {
	return []migration{
		0: func(map[string]json.RawMessage) error { return nil },
	}
}

// SchemaVersion is the settings file format this build writes.
//
//nolint:gochecknoglobals // other packages read it; counted off the migrations so the two cannot drift
var SchemaVersion = len(migrations())

// Settings is the persisted preference set.
type Settings struct {
	SchemaVersion int          `json:"schemaVersion"`
	Name          string       `json:"name"`        // user-visible speaker and receiver name
	SourceNames   []SourceName `json:"sourceNames"` // user-visible names for individual inputs/accounts
	Language      string       `json:"language"`    // UI language code, e.g. "en", "nl"
	MQTT          MQTT         `json:"mqtt"`        // MQTT bridge config
	Audio         Audio        `json:"audio"`       // volume and bass, restored at boot
	// Display is the ST20 standby screen. Absent from an older settings file, in which
	// case the zero value applies — see Display for why that zero value is deliberate.
	Display Display `json:"display"`
	// Alarm wakes the speaker with a preset. Read it through AlarmConfig: an older
	// settings file has no alarm at all, and its zero value is not the default.
	Alarm Alarm `json:"alarm"`
	// Multiroom holds the group preferences; see Multiroom for why it stores the
	// inverse of what the UI shows.
	Multiroom Multiroom `json:"multiroom"`
	// Integrations switches the receivers other apps play through.
	Integrations Integrations `json:"integrations"`
	// Channel is what this speaker plays of a stereo source: "" or "stereo" both
	// channels, "left" or "right" one of them, as half of a stereo pair, "mono"
	// both mixed.
	Channel string `json:"channel,omitempty"`
	// AuxName is what the AUX input is called where it plays; empty is "AUX".
	AuxName string `json:"auxName,omitempty"`
	// CrashReports is the owner's opt-in to being offered crash reports. Absent from an
	// older file, whose zero value is the default: off.
	CrashReports CrashReports `json:"crashReports,omitzero"`
}

// Multiroom is how this speaker treats the group it leads. The UI writes the same
// values to every speaker in the household, but each speaker acts on its own copy:
// there is no household store, and a speaker must not need its peers to know what to do.
//
// The UI's switch is "keep groups", default on. A JSON bool that is absent reads as
// false, so storing KeepGroups directly would turn every older settings file into
// "dissolve groups on standby". The stored field is the inverse, whose zero value is
// the default; KeepGroups is the only way the rest of the code reads it.
type Multiroom struct {
	DissolveGroups bool `json:"dissolveGroups"`
	// SyncVolume makes a volume change on a leader move its followers by the same step.
	SyncVolume bool `json:"syncVolume"`
}

// KeepGroups reports whether followers stay with this speaker when it goes to standby.
func (m Multiroom) KeepGroups() bool { return !m.DissolveGroups }

// Integrations are the receivers other apps play through: Spotify Connect (librespot)
// and AirPlay 2 (shairport-sync), which is also how a speaker joins Apple Home. Both are
// on by default; as with Multiroom, the stored fields are the inverse so a settings file
// without them keeps them on.
type Integrations struct {
	SpotifyOff bool `json:"spotifyOff"`
	AirPlayOff bool `json:"airplayOff"`
}

// Spotify reports whether Spotify Connect runs.
func (i Integrations) Spotify() bool { return !i.SpotifyOff }

// AirPlay reports whether the AirPlay receiver runs (where the model has one at all).
func (i Integrations) AirPlay() bool { return !i.AirPlayOff }

// SourceName is a user-visible name for one input, identified by stock's source and
// sourceAccount pair. Nothing sets it any more since the stock XML /nameSource route
// went; it stays in the stored settings and in backups so files from 0.1.0 still load.
type SourceName struct {
	Source  string `json:"source"`
	Account string `json:"account"`
	Name    string `json:"name"`
}

// Audio is the tuning the speaker comes back up with. Stock firmware keeps volume
// and bass across a power cut; without this every reboot loses the user's level.
//
// HasVolume distinguishes "never saved" from "deliberately saved as 0": a zero
// Volume alone would otherwise be indistinguishable from an old settings file and
// silently override the default.
type Audio struct {
	Volume    int  `json:"volume"`
	Bass      int  `json:"bass"`
	HasVolume bool `json:"hasVolume"`
	// AutoOff is the idle timeout in minutes before the speaker drops to standby.
	// 0 means "not configured" and uses the default (stock's 20 minutes); a negative
	// value disables auto-off. JSON cannot tell an absent int from a zero one, so
	// "off" needs a value of its own rather than borrowing zero.
	AutoOff int `json:"autoOff"`
}

// isBidiControl is a Unicode directional formatting character: invisible, and able to
// make a name read reversed or reorder the text around it in the page and on the OLED.
func isBidiControl(r rune) bool {
	return r == '\u061c' || r == '\u200e' || r == '\u200f' ||
		r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069'
}

// maxNameBytes is the longest name in bytes: one DNS-SD label.
const maxNameBytes = 63

// Why NormalizeName refuses a name; the text is what the web UI shows.
var (
	errNameNotUTF8 = errors.New("speaker name is not valid UTF-8")
	errNameEmpty   = errors.New("speaker name is empty")
	errNameTooLong = errors.New("speaker name must be at most 63 bytes")
	errNameControl = errors.New("speaker name contains a control character")
)

// NormalizeName validates a receiver name and returns its canonical form: trimmed, and
// without bidi controls.
func NormalizeName(name string) (string, error) {
	if !utf8.ValidString(name) { // before Map, which would turn bad bytes into U+FFFD
		return "", errNameNotUTF8
	}

	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if isBidiControl(r) {
			return -1
		}

		return r
	}, name))
	if name == "" {
		return "", errNameEmpty
	}
	// Bytes, not characters: the name is advertised as a DNS-SD instance name, one label
	// of at most 63 bytes, and Avahi refuses a longer one, which crash-looped librespot.
	if len(name) > maxNameBytes {
		return "", errNameTooLong
	}

	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errNameControl
		}
	}

	return name, nil
}
