// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/weather"
)

// geocode looks up a town for the standby clock by name. Only the typed name leaves
// the speaker; the answer carries the town's timezone, which the clock needs.
func (s *Server) geocode(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || utf8.RuneCountInString(q) > 80 {
		s.badRequest(w, "town name required", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), slowReadDeadline)
	defer cancel()
	places, err := weather.Geocode(ctx, q, s.settings.Get().Language)
	if err != nil {
		s.fail(w, "lookup failed", err)
		return
	}
	writeJSON(w, 200, places)
}

// getSettings returns the speaker name + bass (with range) + UI language and its
// current network connection.
func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out := map[string]any{
		"language": s.settings.Get().Language,
	}
	info, infoErr := s.backend.Info(ctx)
	if infoErr == nil {
		out["name"] = info.Name
	}
	if infoErr == nil {
		out["model"] = info.Type // device model, e.g. "SoundTouch 10"
	}
	if b, err := s.backend.Bass(ctx); err == nil {
		out["bass"] = b
	}
	out["autoOff"] = s.settings.Get().Audio.AutoOff
	out["auxName"] = s.settings.Get().AuxName
	if net, err := s.backend.NetworkInfo(ctx); err == nil {
		out["network"] = net
	}
	if h := s.mdnsHost(); h != "" {
		out["host"] = h // own .local host, e.g. "soundtap-serial0000000020a.local" (ADR 0043)
	}
	out["mqtt"] = s.mqttSettingsView()
	// The standby screen only exists on a model with an OLED, so the UI is told
	// whether to offer the page at all rather than guessing from the model name.
	out["hasDisplay"] = s.displayAvailable != nil && s.displayAvailable()
	if s.soundbar != nil {
		out["soundbar"] = s.soundbar() // null until the processor has answered
	}
	out["display"] = s.settings.Get().Display
	out["alarm"] = s.settings.Get().AlarmConfig()
	out["multiroom"] = multiroomPrefsView(s.settings.Get().Multiroom)
	in := s.settings.Get().Integrations
	out["integrations"] = map[string]bool{"spotify": in.Spotify(), "airplay": in.AirPlay()}
	out["crashReports"] = s.settings.Get().CrashReports.Enabled // ADR 0126; off by default
	writeJSON(w, 200, out)
}

// mqttSettingsView is the MQTT config as returned to the UI: the password is never
// sent back (only whether one is set), and the live connection status is folded in.
func (s *Server) mqttSettingsView() map[string]any {
	m := s.settings.Get().MQTT
	view := map[string]any{
		"enabled":         m.Enabled,
		"host":            m.Host,
		"port":            m.Port,
		"username":        m.Username,
		"hasPassword":     m.Password != "",
		"baseTopic":       m.BaseTopic,
		"discoveryPrefix": m.DiscoveryPrefix,
		"tls":             m.TLS,
	}
	if s.mqtt != nil {
		connected, lastErr := s.mqtt.Status()
		view["connected"] = connected
		view["lastError"] = lastErr
	}
	return view
}

// mqttStatus reports the live broker connection state so the settings UI can show
// whether the MQTT link is up.
func (s *Server) mqttStatus(w http.ResponseWriter, r *http.Request) {
	if s.mqtt == nil {
		writeJSON(w, 200, map[string]any{"connected": false, "lastError": ""})
		return
	}
	connected, lastErr := s.mqtt.Status()
	writeJSON(w, 200, map[string]any{"connected": connected, "lastError": lastErr})
}

// settingsMaxBody bounds a PUT /api/settings body. The largest legitimate one is the
// whole settings page at once, well under 1 KiB; the margin is for long MQTT values.
const settingsMaxBody = 16 << 10

// settingsPatch is the body of PUT /api/settings. Every field is optional: what is
// absent keeps its stored value.
type settingsPatch struct {
	Name     *string           `json:"name"`
	AuxName  *string           `json:"auxName"` // empty puts back "AUX"
	Bass     *int              `json:"bass"`
	Language *string           `json:"language"`
	AutoOff  *int              `json:"autoOff"` // idle minutes; 0 = default, negative = off
	MQTT     *mqttPatch        `json:"mqtt"`
	Display  *settings.Display `json:"display"`
	// Alarm and Multiroom are patches: each field that is present overrides the
	// stored one, so the UI can flip one switch without resending the rest.
	Alarm        *alarmPatch        `json:"alarm"`
	Multiroom    *multiroomPatch    `json:"multiroom"`
	Integrations *integrationsPatch `json:"integrations"`
	// CrashReports switches the crash report offer on or off (ADR 0126).
	CrashReports *bool `json:"crashReports"`
}

type mqttPatch struct {
	Enabled         *bool   `json:"enabled"`
	Host            *string `json:"host"`
	Port            *int    `json:"port"`
	Username        *string `json:"username"`
	Password        *string `json:"password"`
	BaseTopic       *string `json:"baseTopic"`
	DiscoveryPrefix *string `json:"discoveryPrefix"`
	TLS             *bool   `json:"tls"`
}

type alarmPatch struct {
	Enabled  *bool   `json:"enabled"`
	Time     *string `json:"time"`
	Slot     *int    `json:"slot"`
	Timezone *string `json:"timezone"`
}

type multiroomPatch struct {
	KeepGroups *bool `json:"keepGroups"`
	SyncVolume *bool `json:"syncVolume"`
}

type integrationsPatch struct {
	Spotify *bool `json:"spotify"`
	AirPlay *bool `json:"airplay"`
}

// putSettings applies any provided fields: name + bass on the speaker, the rest in the
// local store. Fields are optional so the UI can live-apply one at a time.
//
// Every stored field in one request is merged and validated first, then written as one
// atomic settings.Store.Update: a request either lands whole or not at all, and two
// browsers saving different fields at once cannot overwrite each other's change with
// the copy they read before it.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var patch settingsPatch
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, settingsMaxBody))
	dec.DisallowUnknownFields() // a typo is an error, not a silent no-op
	if err := dec.Decode(&patch); err != nil {
		s.badRequest(w, "bad body", err)
		return
	}
	// Validation that needs nothing stored happens before the store is locked, so a
	// bad request answers 400 with nothing written.
	if msg, err := patch.normalize(); err != nil {
		s.badRequest(w, msg, err)
		return
	}
	if patch.stored() {
		// An alarm or MQTT patch is merged with the stored one, so it can only be
		// validated inside the update; its refusal is the client's fault, a failed save
		// is not.
		var (
			refusal string
			invalid error
		)
		err := s.settings.Update(func(next *settings.Settings) error {
			refusal, invalid = patch.apply(next)
			return invalid
		})
		switch {
		case invalid != nil:
			s.badRequest(w, refusal, invalid)
			return
		case err != nil:
			s.fail(w, "save settings failed", err)
			return
		}
		if patch.Alarm != nil && !s.settings.Get().AlarmConfig().Enabled {
			s.dismissAlarm()
		}
	}
	if s.applyLiveSettings(w, r, patch) {
		writeOK(w)
	}
}

// normalize checks the fields that need nothing stored and puts the name and display
// in their stored form. On a refusal it returns the message to answer with.
func (p *settingsPatch) normalize() (msg string, err error) {
	if p.Name != nil {
		n, err := settings.NormalizeName(*p.Name)
		if err != nil {
			return "invalid name", err
		}
		p.Name = &n
	}
	if p.AuxName != nil && strings.TrimSpace(*p.AuxName) != "" {
		n, err := settings.NormalizeName(*p.AuxName)
		if err != nil {
			return "invalid auxName", err
		}
		p.AuxName = &n
	}
	if p.Display != nil {
		d, err := settings.NormalizeDisplay(*p.Display)
		if err != nil {
			return "invalid display settings", err
		}
		p.Display = &d
	}
	if p.AutoOff != nil && *p.AutoOff > settings.MaxAutoOff {
		return "invalid autoOff", fmt.Errorf("%d minutes is longer than %d", *p.AutoOff, settings.MaxAutoOff)
	}
	if p.Language != nil && *p.Language != "" && !settings.ValidLanguage(*p.Language) {
		return "invalid language", fmt.Errorf("%q is not a language code", *p.Language)
	}
	return "", nil
}

// stored reports whether the patch changes anything in the settings file. Bass alone
// lives in the audio prefs.
func (p *settingsPatch) stored() bool {
	return p.Name != nil || p.AuxName != nil || p.Language != nil || p.AutoOff != nil || p.MQTT != nil ||
		p.Display != nil || p.Alarm != nil || p.Multiroom != nil || p.Integrations != nil || p.CrashReports != nil
}

// apply writes the patch over next. It refuses an alarm or MQTT settings that are
// invalid once merged with the stored ones, returning the message to answer with.
func (p *settingsPatch) apply(next *settings.Settings) (msg string, err error) {
	if p.Alarm != nil {
		a, err := p.Alarm.merge(next.AlarmConfig())
		if err != nil {
			return "invalid alarm", err
		}
		next.Alarm = a
	}
	if p.MQTT != nil {
		p.MQTT.apply(&next.MQTT)
		if err := next.MQTT.Validate(); err != nil {
			return "invalid MQTT settings: " + err.Error(), err
		}
	}
	if p.Multiroom != nil {
		p.Multiroom.apply(&next.Multiroom)
	}
	if p.Integrations != nil {
		p.Integrations.apply(&next.Integrations)
	}
	if p.Display != nil {
		next.Display = *p.Display
	}
	if p.Name != nil {
		next.Name = *p.Name
	}
	if p.AuxName != nil {
		next.AuxName = strings.TrimSpace(*p.AuxName)
	}
	if p.Language != nil && *p.Language != "" {
		next.Language = *p.Language
	}
	if p.AutoOff != nil {
		next.Audio.AutoOff = settings.NormalizeAutoOff(*p.AutoOff)
	}
	if p.CrashReports != nil {
		next.CrashReports.Enabled = *p.CrashReports
	}
	return "", nil
}

func (p *alarmPatch) merge(a settings.Alarm) (settings.Alarm, error) {
	if p.Enabled != nil {
		a.Enabled = *p.Enabled
	}
	if p.Time != nil {
		a.Time = strings.TrimSpace(*p.Time)
	}
	if p.Slot != nil {
		a.Slot = *p.Slot
	}
	if p.Timezone != nil {
		a.Timezone = strings.TrimSpace(*p.Timezone)
	}
	return a, a.Validate()
}

func (p *multiroomPatch) apply(m *settings.Multiroom) {
	if p.KeepGroups != nil {
		m.DissolveGroups = !*p.KeepGroups
	}
	if p.SyncVolume != nil {
		m.SyncVolume = *p.SyncVolume
	}
}

func (p *integrationsPatch) apply(i *settings.Integrations) {
	if p.Spotify != nil {
		i.SpotifyOff = !*p.Spotify
	}
	if p.AirPlay != nil {
		i.AirPlayOff = !*p.AirPlay
	}
}

// apply overrides only the fields the UI sent, so an omitted password (never
// returned to the UI) is preserved.
func (p *mqttPatch) apply(cfg *settings.MQTT) {
	prev := *cfg
	if p.Enabled != nil {
		cfg.Enabled = *p.Enabled
	}
	if p.Host != nil {
		cfg.Host = strings.TrimSpace(*p.Host)
	}
	if p.Port != nil {
		cfg.Port = *p.Port
	}
	if p.Username != nil {
		cfg.Username = *p.Username
	}
	if p.Password != nil {
		cfg.Password = *p.Password
	}
	if p.BaseTopic != nil {
		cfg.BaseTopic = strings.TrimSpace(*p.BaseTopic)
	}
	if p.DiscoveryPrefix != nil {
		cfg.DiscoveryPrefix = strings.TrimSpace(*p.DiscoveryPrefix)
	}
	if p.TLS != nil {
		cfg.TLS = *p.TLS
	}
	// The stored password only ever goes to the broker it was entered for.
	if p.Password == nil && !sameBroker(prev, *cfg) {
		cfg.Password = ""
	}
}

// applyLiveSettings puts the saved patch into effect on the running speaker. It
// answers w and returns false when that fails.
func (s *Server) applyLiveSettings(w http.ResponseWriter, r *http.Request, p settingsPatch) bool {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	if p.Name != nil {
		// Persisted above first: a failed NAND write must not mutate live identity.
		if err := s.applyName(ctx, *p.Name); err != nil {
			s.fail(w, "set name failed", err)
			return false
		}
	}
	if p.Bass != nil {
		if err := s.backend.SetBass(ctx, *p.Bass); err != nil {
			s.fail(w, "set bass failed", err)
			return false
		}
	}
	if p.MQTT != nil && s.mqtt != nil {
		s.mqtt.Reload() // apply the new config: reconnect (or disconnect)
	}
	if p.Integrations != nil && s.receiversChanged != nil {
		s.receiversChanged() // start or stop Spotify Connect and AirPlay
	}
	return true
}

// applyName puts a stored name live: the speaker, its .local name and the receivers.
// The settings route and a restore both go through here.
func (s *Server) applyName(ctx context.Context, name string) error {
	if err := s.backend.SetName(ctx, name); err != nil {
		return err
	}
	s.mdnsSetName(name) // re-advertise <name>.local
	if s.nameChanged != nil {
		s.nameChanged(name)
	}
	return nil
}

// sameBroker reports whether two MQTT settings send credentials to the same place.
func sameBroker(a, b settings.MQTT) bool {
	return a.Host == b.Host && a.Port == b.Port && a.TLS == b.TLS
}
