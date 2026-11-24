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
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/speaker"
)

// migrations[v] upgrades a raw settings object from version v to v+1, so a new format
// is one appended function plus its test. Files written before the version
// existed are version 0; 0 -> 1 only adds it.
var migrations = []func(map[string]json.RawMessage) error{
	0: func(map[string]json.RawMessage) error { return nil },
}

// SchemaVersion is the settings file format this build writes.
var SchemaVersion = len(migrations)

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

// CrashReports is whether the web UI offers to report a crash it finds on the speaker,
// and the crashes the owner already reported or dismissed, which it does not offer again
// (ADR 0126). The speaker sends nothing: the owner opens and submits the issue.
type CrashReports struct {
	Enabled bool `json:"enabled"`
	// Handled are crash ids, oldest first, at most MaxHandledCrashes.
	Handled []string `json:"handled,omitempty"`
}

// MaxHandledCrashes bounds CrashReports.Handled: older ids go first. A crash id comes from
// the crash's own content, so one that fell off the list is only offered again if the same
// crash is still on the speaker.
const MaxHandledCrashes = 32

// IsHandled reports whether the owner already reported or dismissed crash id.
func (c CrashReports) IsHandled(id string) bool { return slices.Contains(c.Handled, id) }

// Handle records crash id as reported or dismissed.
func (c *CrashReports) Handle(id string) {
	if c.IsHandled(id) {
		return
	}
	c.Handled = append(c.Handled, id)
	if n := len(c.Handled) - MaxHandledCrashes; n > 0 {
		c.Handled = append([]string(nil), c.Handled[n:]...)
	}
}

// Alarm is the speaker's own wake-up alarm: at Time (24-hour "HH:MM", in the zone of
// Display.Timezone, else Timezone, else the system zone) it plays preset Slot. It lives
// on the speaker rather than in a browser because a browser tab is closed while its
// owner sleeps. Timezone is the zone of the browser that last saved the alarm: a speaker
// without a display has no town, and its system zone is UTC.
type Alarm struct {
	Enabled  bool   `json:"enabled"`
	Time     string `json:"time"`
	Slot     int    `json:"slot"`
	Timezone string `json:"timezone,omitempty"`
}

// DefaultAlarm is what an alarm that was never set looks like to the UI.
var DefaultAlarm = Alarm{Enabled: false, Time: "07:00", Slot: 1}

// ParseAlarmTime parses a 24-hour "HH:MM" and returns hour and minute. Exactly two
// digits each, so "7:00" and "07:00:00" are refused rather than guessed at.
func ParseAlarmTime(v string) (hour, minute int, err error) {
	if len(v) != 5 || v[2] != ':' {
		return 0, 0, fmt.Errorf("alarm time must be HH:MM")
	}
	for _, i := range []int{0, 1, 3, 4} {
		if v[i] < '0' || v[i] > '9' {
			return 0, 0, fmt.Errorf("alarm time must be HH:MM")
		}
	}
	hour = int(v[0]-'0')*10 + int(v[1]-'0')
	minute = int(v[3]-'0')*10 + int(v[4]-'0')
	if hour > 23 || minute > 59 {
		return 0, 0, fmt.Errorf("alarm time must be between 00:00 and 23:59")
	}
	return hour, minute, nil
}

// Validate reports whether a is an alarm the scheduler can act on.
func (a Alarm) Validate() error {
	if _, _, err := ParseAlarmTime(a.Time); err != nil {
		return err
	}
	if a.Slot < 1 || a.Slot > speaker.PresetSlots {
		return fmt.Errorf("alarm slot must be 1..%d", speaker.PresetSlots)
	}
	if a.Timezone != "" {
		if _, err := time.LoadLocation(a.Timezone); err != nil {
			return fmt.Errorf("unknown alarm timezone %q", a.Timezone)
		}
	}
	return nil
}

// AlarmConfig is the stored alarm with the defaults filled in for whatever an older
// settings file left out.
func (s Settings) AlarmConfig() Alarm {
	a := s.Alarm
	if a.Time == "" {
		a.Time = DefaultAlarm.Time
	}
	if a.Slot == 0 {
		a.Slot = DefaultAlarm.Slot
	}
	return a
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

// Display configures the OLED standby screen (ST20 only; the ST10 has no panel): the time
// alone, as stock's.
//
// The clock needs a timezone: the zone of the browser that saved these settings, as stock
// shows its clock without a place. The speaker never guesses where it stands (the rootfs
// has no zone and a public-IP lookup is not something a radio should do on its own).
// Without a zone the standby screen stays dark. The zero value is that state.
//
// Files from before 0.1.0 may still hold city, lat, lon and weatherOn from the weather
// line that is gone; the loader ignores them.
type Display struct {
	ClockOff bool   `json:"clockOff"`
	Hour12   bool   `json:"hour12"`
	Timezone string `json:"timezone"` // IANA zone of the browser that saved the settings
	// Brightness is the clock's peak in percent: 25, 50 or 80. 0 means the default, 80.
	Brightness int `json:"brightness,omitempty"`
}

// ClockOn reports whether the standby screen shows the clock.
func (d Display) ClockOn() bool { return !d.ClockOff && d.Timezone != "" }

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

// NormalizeName validates a receiver name and returns its canonical form: trimmed, and
// without bidi controls.
func NormalizeName(name string) (string, error) {
	if !utf8.ValidString(name) { // before Map, which would turn bad bytes into U+FFFD
		return "", fmt.Errorf("speaker name is not valid UTF-8")
	}
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if isBidiControl(r) {
			return -1
		}
		return r
	}, name))
	if name == "" {
		return "", fmt.Errorf("speaker name is empty")
	}
	// Bytes, not characters: the name is advertised as a DNS-SD instance name, one label
	// of at most 63 bytes, and Avahi refuses a longer one, which crash-looped librespot.
	if len(name) > 63 {
		return "", fmt.Errorf("speaker name must be at most 63 bytes")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("speaker name contains a control character")
		}
	}
	return name, nil
}

// MQTT is the persisted configuration for the MQTT bridge (see
// internal/mqttbridge). Password is stored in the clear in the local JSON, like the
// other on-box state — the file never leaves the speaker.
type MQTT struct {
	Enabled         bool   `json:"enabled"`
	Host            string `json:"host"`
	Port            int    `json:"port"`            // 0 -> 1883 (8883 with TLS)
	Username        string `json:"username"`        // optional
	Password        string `json:"password"`        // optional
	BaseTopic       string `json:"baseTopic"`       // default "soundtap/<deviceID>"
	DiscoveryPrefix string `json:"discoveryPrefix"` // default "homeassistant"
	TLS             bool   `json:"tls"`
}

// Limits on the MQTT settings: a host is at most a full DNS name, and a topic root
// leaves room for the levels the bridge adds below it.
const (
	maxMQTTHost  = 253
	maxMQTTTopic = 128
)

// Validate refuses MQTT settings the bridge cannot use: a port outside 1..65535 (0 is
// the default), no host while enabled, a host with whitespace or control characters, and
// a base topic or discovery prefix that is not a plain topic root. Empty topics are the
// defaults.
func (m MQTT) Validate() error {
	if m.Port < 0 || m.Port > 65535 {
		return fmt.Errorf("port %d is outside 1..65535", m.Port)
	}
	if m.Enabled && m.Host == "" {
		return errors.New("host is required when MQTT is on")
	}
	switch {
	case len(m.Host) > maxMQTTHost:
		return fmt.Errorf("host must be at most %d bytes", maxMQTTHost)
	case !utf8.ValidString(m.Host):
		return errors.New("host is not valid UTF-8")
	case strings.IndexFunc(m.Host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		return errors.New("host contains whitespace or a control character")
	}
	if err := validTopicRoot(m.BaseTopic); err != nil {
		return fmt.Errorf("base topic %w", err)
	}
	if err := validTopicRoot(m.DiscoveryPrefix); err != nil {
		return fmt.Errorf("discovery prefix %w", err)
	}
	return nil
}

// validTopicRoot checks a topic the bridge publishes and subscribes below: no
// wildcards, which would make every command filter match foreign topics; no control
// characters; and no '/' at either end, which leaves an empty level.
func validTopicRoot(t string) error {
	switch {
	case t == "":
		return nil
	case len(t) > maxMQTTTopic:
		return fmt.Errorf("must be at most %d bytes", maxMQTTTopic)
	case !utf8.ValidString(t):
		return errors.New("is not valid UTF-8")
	case strings.ContainsAny(t, "#+"):
		return errors.New("must not contain the wildcards # or +")
	case strings.IndexFunc(t, unicode.IsControl) >= 0:
		return errors.New("contains a control character")
	case strings.HasPrefix(t, "/") || strings.HasSuffix(t, "/"):
		return errors.New("must not start or end with /")
	}
	return nil
}

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

// Migrate brings a raw settings file up to SchemaVersion. A file from a newer build is
// refused rather than read with its unknown fields dropped and then written back.
func Migrate(data []byte) (Settings, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Settings{}, err
	}
	if raw == nil {
		return Settings{}, fmt.Errorf("settings file is not an object")
	}
	v := 0
	if rv, ok := raw["schemaVersion"]; ok {
		if err := json.Unmarshal(rv, &v); err != nil || v < 0 {
			return Settings{}, fmt.Errorf("bad schemaVersion %s", rv)
		}
	}
	if v > len(migrations) {
		return Settings{}, fmt.Errorf("%w: schema %d, this build reads up to %d", ErrNewerSchema, v, len(migrations))
	}
	for ; v < len(migrations); v++ {
		if err := migrations[v](raw); err != nil {
			return Settings{}, fmt.Errorf("migrating settings %d -> %d: %w", v, v+1, err)
		}
	}
	raw["schemaVersion"] = json.RawMessage(fmt.Sprint(len(migrations)))
	data, err := json.Marshal(raw)
	if err != nil {
		return Settings{}, fmt.Errorf("re-encoding migrated settings: %w", err)
	}
	s := Settings{Language: "en"}
	err = json.Unmarshal(data, &s)
	return s, err
}

// Open loads (or starts) a store at path. Only a missing file starts from defaults.
// A file that cannot be read leaves the store on defaults and read-only, so the next
// save cannot replace settings that may still be intact on disk.
func Open(path string) *Store {
	st := &Store{path: path, s: Settings{SchemaVersion: SchemaVersion, Language: "en"}}
	data, found, err := atomicjson.Read(path)
	if err != nil {
		st.loadErr, st.readOnly = err, err
		atomicjson.LogLoad(slog.Default(), "settings", path, err)
		return st
	}
	if !found {
		return st
	}
	if st.s, err = Migrate(data); errors.Is(err, ErrNewerSchema) {
		// A rollback, not corruption: leave the file where the newer build will find
		// it, use the fields this build knows, and refuse writes.
		st.s = Settings{SchemaVersion: SchemaVersion, Language: "en"}
		// Best effort on purpose: the store is read-only, and a field this build cannot
		// decode keeps its default rather than hiding the ones it can.
		_ = json.Unmarshal(data, &st.s)
		st.loadErr, st.readOnly = fmt.Errorf("%w; writes refused until a reset", err), err
		slog.Warn("settings are read-only until a reset", "path", path, "err", err)
	} else if err != nil {
		// A corrupt/truncated file must not silently disappear: preserve it so MQTT
		// credentials and other settings can be recovered. One that cannot be moved
		// aside makes the store read-only, since the next write would destroy it.
		st.s = Settings{SchemaVersion: SchemaVersion, Language: "en"}
		st.loadErr = atomicjson.Quarantine(path, err)
		if errors.Is(st.loadErr, atomicjson.ErrUnreadable) {
			st.readOnly = st.loadErr
		}
		atomicjson.LogLoad(slog.Default(), "settings", path, st.loadErr)
	}
	return st
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
	if err := s.replaceLocked(Settings{SchemaVersion: SchemaVersion, Language: "en"}); err != nil {
		s.readOnly = guard
		return err
	}
	RemoveQuarantine(s.path)
	return nil
}

// RemoveQuarantine deletes every corrupt copy Open kept of the settings at path
// (.corrupt, .corrupt.1): they hold the previous owner's MQTT password.
func RemoveQuarantine(path string) {
	if err := atomicjson.RemoveQuarantine(path); err != nil {
		slog.Error("remove settings backup", "path", path, "err", err)
	}
}

// replaceLocked atomically writes next before replacing the in-memory settings.
// Caller holds s.mu.
// 0600: the file holds the MQTT broker password in the clear.
func (s *Store) replaceLocked(next Settings) error {
	if s.closed {
		return ErrClosed
	}
	if s.readOnly != nil {
		return s.readOnly
	}
	next.SchemaVersion = SchemaVersion
	if err := atomicjson.Write(s.path, next, 0o600); err != nil {
		return err
	}
	s.s = next
	return nil
}
