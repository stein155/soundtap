// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/store"
	"github.com/stein155/soundtap/app/internal/web/apierror"
)

// backupKind/backupVersion tag the export so a restore can refuse a file that is not
// one of ours. Files from older builds are upgraded by backupMigrations, one version at
// a time, so a format change does not strand every backup taken before it.
const (
	backupKind    = "soundtap-backup"
	backupVersion = 1
	restorePath   = "/api/restore"
	// backupMaxBytes bounds an upload, so a restore cannot be used to exhaust RAM on a
	// 128 MiB speaker. It is what the largest valid export can be: a full list of
	// favorites, each as large as addFavorite takes and doubled for the JSON escaping of
	// quotes and backslashes, plus an ordinary request's worth for presets and settings.
	// The guard uses the same number (bodyLimit), so the two cannot disagree.
	backupMaxBytes = maxRequestBody + 2*favorites.MaxStored*favoriteMaxBytes
)

// backup is the export format. Credentials are deliberately absent: the Wi-Fi PSK
// (which lives in wpa_supplicant, not here) and the MQTT broker password never leave
// the speaker in a file that lands in a Downloads folder. A restore therefore leaves
// an existing MQTT password alone rather than clearing it.
type backup struct {
	Kind       string            `json:"kind"`
	Version    int               `json:"version"`
	ExportedAt string            `json:"exportedAt"`
	Speaker    backupSpeaker     `json:"speaker"`
	Settings   backupSettings    `json:"settings"`
	Presets    []store.Preset    `json:"presets"`
	Favorites  []favorites.Entry `json:"favorites"`
	// Absent on models without a codec script and in files from before it.
	CodecScript *backupCodec `json:"codecScript,omitempty"`
	Note        string       `json:"note"`
}

type backupSpeaker struct {
	Name     string `json:"name"`
	Model    string `json:"model,omitempty"`
	DeviceID string `json:"deviceId,omitempty"`
	Version  string `json:"version"`
}

type backupSettings struct {
	Name     string         `json:"name"`
	Language string         `json:"language"`
	Audio    settings.Audio `json:"audio"`
	MQTT     backupMQTT     `json:"mqtt"`
	// Absent from files exported before these were added; a restore of such a file
	// leaves the speaker's own values alone.
	Display   *settings.Display   `json:"display,omitempty"`
	Alarm     *settings.Alarm     `json:"alarm,omitempty"`
	Multiroom *settings.Multiroom `json:"multiroom,omitempty"`
	// Integrations are the Spotify Connect and AirPlay switches.
	Integrations *settings.Integrations `json:"integrations,omitempty"`
	// Always exported, as [] when there are none: absent (an older file) keeps the
	// speaker's names, an empty list clears them.
	SourceNames []settings.SourceName `json:"sourceNames"`
	// Always exported: absent (an older file) keeps the speaker's own, an empty
	// value puts the default back: stereo, and "AUX".
	Channel *string `json:"channel,omitempty"`
	AuxName *string `json:"auxName,omitempty"`
	// CrashReports is the owner's opt-in (ADR 0126); absent keeps the speaker's own. The
	// crashes already reported or dismissed stay with the speaker they happened on.
	CrashReports *bool `json:"crashReports,omitempty"`
}

// backupMQTT mirrors settings.MQTT minus Password.
type backupMQTT struct {
	Enabled         bool   `json:"enabled"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username"`
	BaseTopic       string `json:"baseTopic"`
	DiscoveryPrefix string `json:"discoveryPrefix"`
	TLS             bool   `json:"tls"`
}

// stored is the stored form of the backup's MQTT settings, without a password.
func (m *backupMQTT) stored() settings.MQTT {
	return settings.MQTT{
		Enabled: m.Enabled, Host: m.Host, Port: m.Port,
		Username:  m.Username,
		BaseTopic: m.BaseTopic, DiscoveryPrefix: m.DiscoveryPrefix,
		TLS:      m.TLS,
		Password: "",
	}
}

// exportBackup writes the presets and settings as one downloadable JSON file. Read
// only — the point is that a user can take it before an update or a factory reset.
func (s *Server) exportBackup(w http.ResponseWriter, r *http.Request) {
	set := s.settings.Get()
	alarm := set.AlarmConfig()

	doc := backup{
		Kind:       backupKind,
		Version:    backupVersion,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Speaker:    backupSpeaker{Name: set.Name, Version: s.version, Model: "", DeviceID: ""},
		Settings: backupSettings{
			Name:     set.Name,
			Language: set.Language,
			Audio:    set.Audio,
			MQTT: backupMQTT{
				Enabled: set.MQTT.Enabled, Host: set.MQTT.Host, Port: set.MQTT.Port,
				Username: set.MQTT.Username, BaseTopic: set.MQTT.BaseTopic,
				DiscoveryPrefix: set.MQTT.DiscoveryPrefix, TLS: set.MQTT.TLS,
			},
			Display:      &set.Display,
			Alarm:        &alarm,
			Multiroom:    &set.Multiroom,
			Integrations: &set.Integrations,
			SourceNames:  append([]settings.SourceName{}, set.SourceNames...),
			Channel:      &set.Channel,
			AuxName:      &set.AuxName,
			CrashReports: &set.CrashReports.Enabled,
		},
		Note:        "No passwords are included. Restoring keeps the MQTT password already on the speaker.",
		Presets:     nil,
		Favorites:   nil,
		CodecScript: nil,
	}

	info, err := s.backend.Info(r.Context())
	if err == nil && info != nil {
		doc.Speaker.Model, doc.Speaker.DeviceID = info.Type, info.DeviceID
	}

	presets, err := s.presetList(r.Context())
	if err != nil {
		s.fail(w, "read presets", err)

		return
	}

	doc.Presets = presets
	doc.Favorites = s.favoritesStore.All()
	doc.CodecScript = exportCodec(s.codecDir())

	w.Header().Set("Content-Disposition", `attachment; filename="soundtap-backup.json"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	// A file, not a page: without HTML escaping an "&" in a logo address stays one byte,
	// which keeps the export inside backupMaxBytes.
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(doc) //nolint:errchkjson // the client may be gone; nothing left to tell it
}

// importBackup validates a whole file, then applies it. Presets are written for every
// slot the file names and cleared for every slot it does not, so a restore reproduces
// the speaker rather than merging into whatever is there now.
func (s *Server) importBackup(w http.ResponseWriter, r *http.Request) {
	// A restore or reset rewrites what an install or update is reading (ADR 0052).
	if !s.fw.TryLock() {
		apiErrorCode(w, http.StatusConflict, apierror.Busy, "another speaker operation is in progress")

		return
	}
	defer s.fw.Unlock()

	var parsed backup

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, backupMaxBytes))

	err := dec.Decode(&parsed)
	if err != nil {
		s.badRequest(w, "not readable as a backup file", err)

		return
	}

	err = parsed.validate()
	if err != nil {
		s.badRequest(w, err.Error(), err)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), restoreDeadline)
	defer cancel()

	// Validation above is the only place a restore can be refused whole. Past this
	// point every step touches something different (settings file, NAND, mDNS) with
	// no way to roll the earlier ones back, so a failure halfway used to return 500
	// and leave the speaker in a state nobody could name. Apply everything instead
	// and report exactly which steps failed: half-restored is unavoidable, but
	// half-restored-and-unreported is not.
	var failed []string

	try := func(step string, err error) {
		if err != nil {
			failed = append(failed, step)
			s.log.Error("restore step failed", "step", step, "err", err)
		}
	}

	// Every stored setting lands in one atomic write: a power cut mid-restore leaves the
	// old settings or the new ones, never a mix. Live side effects follow it.
	name, _ := settings.NormalizeName(parsed.Settings.Name)
	stored := s.settings.Update(func(next *settings.Settings) error {
		parsed.Settings.applyTo(next, name)

		return nil
	})
	try("settings", stored)

	if stored == nil {
		s.applyRestoredLive(ctx, &parsed.Settings, name, try)
	}

	// Absent from a file exported before favorites: the speaker's own list stays.
	if parsed.Favorites != nil {
		try("favorites", s.favoritesStore.Replace(parsed.Favorites))
	}

	restored := s.restorePresets(ctx, parsed.Presets, try)
	codec, err := restoreCodec(s.codecDir(), parsed.CodecScript, wantedCodec)
	try("codec script", err)

	if codec {
		s.log.Info("restored the codec script; sound returns at the next restart", "name", parsed.CodecScript.Name)
	}

	if len(failed) > 0 {
		s.log.Warn("restored backup with failures", "presets", restored, "failed", failed)
		s.hub.nudge()
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"status": "partly restored", "presets": restored, "failed": failed,
			"error": "some settings could not be restored: " + strings.Join(failed, ", "),
			"code":  apierror.Internal,
		})

		return
	}

	s.log.Info("restored backup", "presets", restored, "exportedAt", parsed.ExportedAt)
	s.hub.nudge()
	writeJSON(w, http.StatusOK, map[string]any{"status": "restored", "presets": restored, "codecScript": codec})
}

// applyTo copies a validated backup's settings into next. name is the normalized
// speaker name, empty to keep the current one.
func (bs *backupSettings) applyTo(next *settings.Settings, name string) {
	if name != "" {
		next.Name = name
	}

	if bs.Language != "" {
		next.Language = bs.Language
	}

	next.Audio = bs.Audio

	next.Audio.AutoOff = settings.NormalizeAutoOff(bs.Audio.AutoOff)
	if d := bs.Display; d != nil {
		next.Display = *d
		if *d != (settings.Display{ClockOff: false, Hour12: false, Timezone: ""}) {
			next.Display, _ = settings.NormalizeDisplay(*d) // validated above
		}
	}

	if bs.Alarm != nil {
		next.Alarm = *bs.Alarm
	}

	if bs.Multiroom != nil {
		next.Multiroom = *bs.Multiroom
	}

	if bs.Integrations != nil {
		next.Integrations = *bs.Integrations
	}

	if bs.SourceNames != nil {
		next.SourceNames = bs.SourceNames
	}

	if bs.Channel != nil {
		next.Channel = *bs.Channel
	}

	if bs.CrashReports != nil {
		next.CrashReports.Enabled = *bs.CrashReports
	}

	if bs.AuxName != nil {
		next.AuxName = strings.TrimSpace(*bs.AuxName)
		if next.AuxName != "" {
			next.AuxName, _ = settings.NormalizeName(next.AuxName) // validated above
		}
	}
	// Keep the configured password when the backup names the same broker: the backup
	// never carries one, and blanking it would silently disconnect a working bridge. A
	// backup naming another broker gets no password, or a crafted one could collect it.
	mq := bs.MQTT.stored()
	if sameBroker(&next.MQTT, &mq) {
		mq.Password = next.MQTT.Password
	}

	next.MQTT = mq
}

// applyRestoredLive applies what a stored setting alone does not change on a running
// speaker. try records a failed step.
func (s *Server) applyRestoredLive(
	ctx context.Context, restored *backupSettings, name string, try func(string, error),
) {
	if name != "" {
		try("name", s.applyName(ctx, name))
	}

	if a := restored.Audio; a.HasVolume {
		// Live as well as stored: the prefs saver persists whatever the speaker plays at,
		// so a value only in the settings file would be overwritten ten seconds later.
		try("volume", s.backend.SetVolume(ctx, a.Volume))
		try("bass", s.backend.SetBass(ctx, a.Bass))
	}
	// As PUT /api/settings does: an alarm restored as off must not still ring.
	if restored.Alarm != nil && !s.settings.Get().AlarmConfig().Enabled {
		s.dismissAlarm(ctx)
	}

	if restored.Channel != nil && s.channel != nil {
		s.channel(s.settings.Get().Channel) // the running mixer, as PUT /api/channel does
	}

	if s.mqtt != nil {
		s.mqtt.Reload()
	}

	if restored.Integrations != nil && s.receiversChanged != nil {
		s.receiversChanged() // start or stop Spotify Connect and AirPlay
	}
}

// restorePresets writes every slot the backup names and clears every slot it does not.
// It returns how many presets it wrote; try records a failed step.
func (s *Server) restorePresets(ctx context.Context, presets []store.Preset, try func(string, error)) int {
	want := map[int]store.Preset{}
	for _, p := range presets {
		want[p.Slot] = p
	}

	restored := 0

	for slot := 1; slot <= speaker.PresetSlots; slot++ {
		preset, ok := want[slot]
		if !ok {
			try(fmt.Sprintf("clear preset %d", slot), s.backend.RemovePreset(ctx, slot))

			continue
		}

		source, itemType, loc := presetContent(&preset)

		err := s.backend.StorePreset(ctx, slot, source, itemType, loc, preset.Name, preset.Logo)
		if err != nil {
			try(fmt.Sprintf("preset %d", slot), err)

			continue
		}

		restored++
	}

	return restored
}

// factoryReset returns the speaker to a fresh state: presets, preferences and the
// speaker's name go here, and on hardware the callback clears accounts, Bluetooth
// pairings and Wi-Fi as well and restarts into the setup AP. Nothing formats
// persistent storage — the contract in CONTRIBUTING.md forbids it.
//
// Without the callback — the simulator, and the tests — this clears everything that
// lives in the store, which is what "reset" can mean off a speaker.
//
// The body must carry {"confirm":true}. The UI asks first; the flag makes a stray
// POST — a bookmarked URL, a curl with the wrong path — inert.
func (s *Server) factoryReset(w http.ResponseWriter, r *http.Request) {
	// A restore or reset rewrites what an install or update is reading (ADR 0052).
	if !s.fw.TryLock() {
		apiErrorCode(w, http.StatusConflict, apierror.Busy, "another speaker operation is in progress")

		return
	}
	defer s.fw.Unlock()

	var body struct {
		Confirm bool `json:"confirm"`
	}

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, smallBody)).Decode(&body)
	if err != nil {
		s.badRequest(w, "bad body", err)

		return
	}

	if !body.Confirm {
		s.badRequest(w, "confirm must be true", nil)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), restoreDeadline)
	defer cancel()

	for slot := 1; slot <= speaker.PresetSlots; slot++ {
		removeErr := s.backend.RemovePreset(ctx, slot)
		if removeErr != nil {
			s.fail(w, "clear preset failed", removeErr)

			return
		}
	}

	err = s.settings.Reset()
	if err != nil {
		s.fail(w, "clear settings failed", err)

		return
	}

	if s.mqtt != nil {
		s.mqtt.Reload() // the bridge was just switched off with everything else
	}

	if s.hardwareReset != nil {
		err := s.hardwareReset()
		if err != nil {
			s.fail(w, "factory reset failed", err)

			return
		}
	}

	s.log.Warn("factory reset")
	s.hub.nudge()
	writeJSON(w, http.StatusOK, map[string]any{"status": "reset"})
}

// movePreset swaps two preset slots, which is all "reorder" needs on a six-slot
// grid: the front panel's buttons are physical and fixed, so ordering is only ever
// "put this one where that one is". The swap goes through the backend, as a normal
// store does, so the front panel, the API and the persisted file cannot disagree about
// what is on a button.
func (s *Server) movePreset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From int `json:"from"`
		To   int `json:"to"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		s.badRequest(w, "bad body", err)

		return
	}

	if body.From < 1 || body.From > speaker.PresetSlots || body.To < 1 || body.To > speaker.PresetSlots {
		s.badRequest(w, fmt.Sprintf("slots must be 1..%d", speaker.PresetSlots), nil)

		return
	}

	if body.From == body.To {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})

		return
	}

	presets, err := s.presetList(r.Context())
	if err != nil {
		s.fail(w, "read presets", err)

		return
	}

	occupied := func(p store.Preset) bool { return p.Slot == body.From || p.Slot == body.To }
	if !slices.ContainsFunc(presets, occupied) {
		s.badRequest(w, "both slots are empty", nil)

		return
	}
	// One backend call: both slots change under one lock and in one write.
	err = s.backend.SwapPresets(r.Context(), body.From, body.To)
	if err != nil {
		s.fail(w, "move failed", err)

		return
	}

	s.hub.nudge()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// It returns the source, the item type and the location, in that order.
func presetContent(preset *store.Preset) (string, string, string) {
	switch preset.Source {
	case sourceUPnP:
		return preset.Source, preset.Type, preset.Location
	case sourceSpotify:
		return preset.Source, "uri", preset.Location
	}

	return sourceTuneIn, "stationurl", speaker.StationLocation(preset.StationID)
}
