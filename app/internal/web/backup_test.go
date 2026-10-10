// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/web"
)

// exported is the subset of the backup file the tests assert on.
type exported struct {
	Kind     string `json:"kind"`
	Version  int    `json:"version"`
	Settings struct {
		Name     string `json:"name"`
		Language string `json:"language"`
		MQTT     map[string]any
	} `json:"settings"`
	Presets []struct {
		Slot      int    `json:"slot"`
		StationID string `json:"stationId"`
		Name      string `json:"name"`
	} `json:"presets"`
}

func TestBackupExportsPresetsAndSettings(t *testing.T) {
	h, _ := newServer(t)
	if rec := do(t, h, "PUT", "/api/presets/2", `{"stationId":"s12345","name":"Radio 2"}`); rec.Code != 200 {
		t.Fatalf("seed preset: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "PUT", "/api/settings", `{"name":"Keuken","language":"nl"}`); rec.Code != 200 {
		t.Fatalf("seed settings: %d %s", rec.Code, rec.Body)
	}

	rec := do(t, h, "GET", "/api/backup", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", got)
	}
	var b exported
	decodeBody(t, rec, &b)
	if b.Kind != "soundtap-backup" || b.Version != 1 {
		t.Fatalf("kind/version = %q/%d", b.Kind, b.Version)
	}
	if b.Settings.Name != "Keuken" || b.Settings.Language != "nl" {
		t.Errorf("settings = %+v", b.Settings)
	}
	if len(b.Presets) != 1 || b.Presets[0].Slot != 2 || b.Presets[0].StationID != "s12345" {
		t.Fatalf("presets = %+v", b.Presets)
	}
	// A file that lands in a Downloads folder carries no credentials.
	if strings.Contains(strings.ToLower(rec.Body.String()), "password\"") {
		t.Errorf("backup carries a password field: %s", rec.Body)
	}
}

func TestRestoreRoundTripsAndClearsSlotsTheFileOmits(t *testing.T) {
	h, _ := newServer(t)
	do(t, h, "PUT", "/api/presets/1", `{"stationId":"s111","name":"One"}`)
	do(t, h, "PUT", "/api/settings", `{"name":"Woonkamer","language":"en"}`)
	saved := do(t, h, "GET", "/api/backup", "").Body.String()

	// Drift away from the backup: different preset slot, different name.
	do(t, h, "DELETE", "/api/presets/1", "")
	do(t, h, "PUT", "/api/presets/5", `{"stationId":"s555","name":"Five"}`)
	do(t, h, "PUT", "/api/settings", `{"name":"Zolder","language":"de"}`)

	if rec := do(t, h, "POST", "/api/restore", saved); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}

	var b exported
	if err := json.Unmarshal([]byte(do(t, h, "GET", "/api/backup", "").Body.String()), &b); err != nil {
		t.Fatal(err)
	}
	if b.Settings.Name != "Woonkamer" || b.Settings.Language != "en" {
		t.Errorf("settings after restore = %+v", b.Settings)
	}
	if len(b.Presets) != 1 || b.Presets[0].Slot != 1 || b.Presets[0].StationID != "s111" {
		t.Fatalf("presets after restore = %+v, want only slot 1", b.Presets)
	}
}

// A restore writes NAND and renames the speaker, so anything malformed must be
// refused whole — never applied up to the bad field.
func TestRestoreRejectsBadFiles(t *testing.T) {
	cases := map[string]string{
		"not a backup":      `{"kind":"something-else","version":1}`,
		"future version":    `{"kind":"soundtap-backup","version":99}`,
		"slot out of range": `{"kind":"soundtap-backup","version":1,"presets":[{"slot":9,"stationId":"s1"}]}`,
		"duplicate slot":    `{"kind":"soundtap-backup","version":1,"presets":[{"slot":1,"stationId":"s1"},{"slot":1,"stationId":"s2"}]}`,
		"bad station id":    `{"kind":"soundtap-backup","version":1,"presets":[{"slot":1,"stationId":"../../etc/passwd"}]}`,
		"bad language":      `{"kind":"soundtap-backup","version":1,"settings":{"language":"klingon-ish!"}}`,
		"control in name":   "{\"kind\":\"soundtap-backup\",\"version\":1,\"settings\":{\"name\":\"bad\\u0007name\"}}",
		"not json":          `nope`,
		"autoOff too long":  `{"kind":"soundtap-backup","version":1,"settings":{"audio":{"autoOff":10081}}}`,
		"volume over 100":   `{"kind":"soundtap-backup","version":1,"settings":{"audio":{"volume":101,"hasVolume":true}}}`,
		"mqtt port":         `{"kind":"soundtap-backup","version":1,"settings":{"mqtt":{"enabled":true,"host":"broker.lan","port":70000}}}`,
		"mqtt on, no host":  `{"kind":"soundtap-backup","version":1,"settings":{"mqtt":{"enabled":true}}}`,
		"mqtt host":         `{"kind":"soundtap-backup","version":1,"settings":{"mqtt":{"host":"broker lan"}}}`,
		"mqtt base topic":   `{"kind":"soundtap-backup","version":1,"settings":{"mqtt":{"baseTopic":"soundtap/#"}}}`,
		"mqtt prefix":       `{"kind":"soundtap-backup","version":1,"settings":{"mqtt":{"discoveryPrefix":"homeassistant/"}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h, _ := newServer(t)
			do(t, h, "PUT", "/api/presets/3", `{"stationId":"s333","name":"Three"}`)

			if rec := do(t, h, "POST", "/api/restore", body); rec.Code != 400 {
				t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
			}
			var after exported
			decodeBody(t, do(t, h, "GET", "/api/backup", ""), &after)
			if len(after.Presets) != 1 || after.Presets[0].Slot != 3 {
				t.Fatalf("a refused restore changed the presets: %+v", after.Presets)
			}
		})
	}
}

func TestMovePresetSwapsSlots(t *testing.T) {
	h, _ := newServer(t)
	do(t, h, "PUT", "/api/presets/1", `{"stationId":"s111","name":"One"}`)
	do(t, h, "PUT", "/api/presets/4", `{"stationId":"s444","name":"Four"}`)

	if rec := do(t, h, "POST", "/api/presets/move", `{"from":1,"to":4}`); rec.Code != 200 {
		t.Fatalf("move: %d %s", rec.Code, rec.Body)
	}
	var b exported
	decodeBody(t, do(t, h, "GET", "/api/backup", ""), &b)
	got := map[int]string{}
	for _, p := range b.Presets {
		got[p.Slot] = p.StationID
	}
	if got[1] != "s444" || got[4] != "s111" {
		t.Fatalf("after swap = %+v", got)
	}
}

// Moving onto an empty slot moves rather than duplicates: the source must end empty.
func TestMovePresetToAnEmptySlot(t *testing.T) {
	h, _ := newServer(t)
	do(t, h, "PUT", "/api/presets/2", `{"stationId":"s222","name":"Two"}`)

	if rec := do(t, h, "POST", "/api/presets/move", `{"from":2,"to":6}`); rec.Code != 200 {
		t.Fatalf("move: %d %s", rec.Code, rec.Body)
	}
	var b exported
	decodeBody(t, do(t, h, "GET", "/api/backup", ""), &b)
	if len(b.Presets) != 1 || b.Presets[0].Slot != 6 || b.Presets[0].StationID != "s222" {
		t.Fatalf("presets = %+v, want only slot 6", b.Presets)
	}
}

func TestMovePresetRejectsBadSlots(t *testing.T) {
	h, _ := newServer(t)
	do(t, h, "PUT", "/api/presets/1", `{"stationId":"s111","name":"One"}`)
	for _, body := range []string{`{"from":0,"to":3}`, `{"from":1,"to":7}`, `{"from":2,"to":3}`} {
		if rec := do(t, h, "POST", "/api/presets/move", body); rec.Code != 400 {
			t.Errorf("%s gave %d, want 400", body, rec.Code)
		}
	}
}

// A Spotify preset is a URI and nothing else, and survives a backup round trip.
func TestSpotifyPresetStoresAndRoundTrips(t *testing.T) {
	h, _ := newServer(t)
	const uri = "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M"
	for _, bad := range []string{`{"source":"SPOTIFY","location":"spotify:show:37i9dQZF1DXcBWIGoYBM5M"}`, `{"source":"SPOTIFY","location":"https://x"}`} {
		if rec := do(t, h, "PUT", "/api/presets/4", bad); rec.Code != 400 {
			t.Errorf("PUT %s = %d, want 400", bad, rec.Code)
		}
	}
	if rec := do(t, h, "PUT", "/api/presets/4", `{"source":"SPOTIFY","location":"`+uri+`","name":"Top Hits"}`); rec.Code != 200 {
		t.Fatalf("store: %d %s", rec.Code, rec.Body)
	}
	saved := do(t, h, "GET", "/api/backup", "").Body.String()
	do(t, h, "DELETE", "/api/presets/4", "")
	if rec := do(t, h, "POST", "/api/restore", saved); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	list := do(t, h, "GET", "/api/presets", "").Body.String()
	if !strings.Contains(list, `"source":"SPOTIFY"`) || !strings.Contains(list, uri) {
		t.Fatalf("presets after restore = %s", list)
	}
}

// The alarm, the standby screen and the multiroom choices are part of a backup too, and a
// restored volume reaches the speaker itself, not just the settings file.
func TestRestoreBringsBackAlarmDisplayMultiroomAndVolume(t *testing.T) {
	h, _ := newServer(t)
	do(t, h, "PUT", "/api/settings", `{"alarm":{"enabled":true,"time":"06:45","slot":3},"display":{"clockOff":true,"hour12":true},"multiroom":{"keepGroups":false,"syncVolume":true}}`)
	saved := strings.Replace(do(t, h, "GET", "/api/backup", "").Body.String(),
		`"audio":{"volume":0,"bass":0,"hasVolume":false`, `"audio":{"volume":17,"bass":-3,"hasVolume":true`, 1)
	if !strings.Contains(saved, `"volume":17`) {
		t.Fatalf("backup has an unexpected audio block: %s", saved)
	}

	do(t, h, "PUT", "/api/settings", `{"alarm":{"enabled":false,"time":"09:00","slot":1},"display":{"clockOff":true},"multiroom":{"keepGroups":true,"syncVolume":false}}`)
	do(t, h, "POST", "/api/volume", `{"volume":60}`)
	if rec := do(t, h, "POST", "/api/restore", saved); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}

	var b struct {
		Settings struct {
			Alarm     map[string]any `json:"alarm"`
			Display   map[string]any `json:"display"`
			Multiroom map[string]any `json:"multiroom"`
		} `json:"settings"`
	}
	decodeBody(t, do(t, h, "GET", "/api/backup", ""), &b)
	if b.Settings.Alarm["time"] != "06:45" || b.Settings.Alarm["enabled"] != true || b.Settings.Alarm["slot"] != 3.0 {
		t.Errorf("alarm after restore = %v", b.Settings.Alarm)
	}
	if b.Settings.Display["hour12"] != true {
		t.Errorf("display after restore = %v", b.Settings.Display)
	}
	if b.Settings.Multiroom["dissolveGroups"] != true || b.Settings.Multiroom["syncVolume"] != true {
		t.Errorf("multiroom after restore = %v", b.Settings.Multiroom)
	}
	var v struct {
		Volume int `json:"volume"`
	}
	decodeBody(t, do(t, h, "GET", "/api/volume", ""), &v)
	if v.Volume != 17 {
		t.Errorf("live volume after restore = %d, want 17", v.Volume)
	}
}

// The channel a speaker plays and the name of its AUX input are settings too.
func TestRestoreBringsBackChannelAndAuxName(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	h := srv.Handler()
	live := ""
	srv.AttachForTest(func(h *web.Attached) { h.Audio.Channel = func(c string) { live = c } })
	state := func() string {
		return do(t, h, "GET", "/api/channel", "").Body.String() + do(t, h, "GET", "/api/settings", "").Body.String()
	}
	do(t, h, "PUT", "/api/channel", `{"channel":"mono"}`)
	do(t, h, "PUT", "/api/settings", `{"auxName":"Platenspeler"}`)
	saved := do(t, h, "GET", "/api/backup", "").Body.String()
	older := strings.Replace(strings.Replace(saved, `"channel":"mono",`, "", 1), `"auxName":"Platenspeler"`, `"auxName":null`, 1)
	cleared := strings.Replace(strings.Replace(saved, `"channel":"mono"`, `"channel":""`, 1), `"auxName":"Platenspeler"`, `"auxName":""`, 1)

	do(t, h, "PUT", "/api/channel", `{"channel":"left"}`)
	do(t, h, "PUT", "/api/settings", `{"auxName":"TV"}`)
	// A file from before these were exported leaves both alone.
	if rec := do(t, h, "POST", "/api/restore", older); rec.Code != 200 {
		t.Fatalf("restore older: %d %s", rec.Code, rec.Body)
	}
	if got := state(); !strings.Contains(got, `"channel":"left"`) || !strings.Contains(got, `"auxName":"TV"`) || live != "left" {
		t.Fatalf("after an older backup: live %q, %s", live, got)
	}
	if rec := do(t, h, "POST", "/api/restore", saved); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	if got := state(); !strings.Contains(got, `"channel":"mono"`) || !strings.Contains(got, `"auxName":"Platenspeler"`) || live != "mono" {
		t.Fatalf("after restore: live %q, %s", live, got)
	}
	// Present and empty puts the defaults back.
	if rec := do(t, h, "POST", "/api/restore", cleared); rec.Code != 200 {
		t.Fatalf("restore cleared: %d %s", rec.Code, rec.Body)
	}
	if got := state(); !strings.Contains(got, `"channel":"stereo"`) || !strings.Contains(got, `"auxName":""`) || live != "" {
		t.Fatalf("after a cleared backup: live %q, %s", live, got)
	}
	bad := strings.Replace(saved, `"channel":"mono"`, `"channel":"centre"`, 1)
	if rec := do(t, h, "POST", "/api/restore", bad); rec.Code != 400 {
		t.Fatalf("restore of an unknown channel: %d, want 400", rec.Code)
	}
}

// A muted speaker reports the level it comes back to, and says it is muted.
func TestVolumeReportsMuteBesideTheLevel(t *testing.T) {
	h, sp := newServer(t)
	do(t, h, "POST", "/api/volume", `{"volume":33}`)
	sp.ApplyKey("MUTE")
	var v struct {
		Volume int  `json:"volume"`
		Muted  bool `json:"muted"`
	}
	decodeBody(t, do(t, h, "GET", "/api/volume", ""), &v)
	if v.Volume != 33 || !v.Muted {
		t.Fatalf("muted /api/volume = %+v, want level 33 muted", v)
	}
	do(t, h, "POST", "/api/volume", `{"volume":34}`)
	decodeBody(t, do(t, h, "GET", "/api/volume", ""), &v)
	if v.Volume != 34 || v.Muted {
		t.Fatalf("after a set /api/volume = %+v, want 34 unmuted", v)
	}
}

// A typo in a settings write is refused instead of silently ignored.
func TestPutSettingsRejectsUnknownFields(t *testing.T) {
	h, _ := newServer(t)
	if rec := do(t, h, "PUT", "/api/settings", `{"nmae":"Keuken"}`); rec.Code != 400 {
		t.Fatalf("unknown field: %d %s", rec.Code, rec.Body)
	}
}

// A backup taken with no source names restores to none: a name added later must not
// survive the restore.
func TestRestoreClearsSourceNamesAddedAfterTheBackup(t *testing.T) {
	dir := t.TempDir()
	srv, _, _ := newServerAt(t, dir)
	srv.ReconfigureForTest(func(o *web.Options) { o.Speaker.UserSources = []string{"AUX"} })
	h := srv.Handler()
	saved := do(t, h, "GET", "/api/backup", "").Body.String()
	if !strings.Contains(saved, `"sourceNames":[]`) {
		t.Fatalf("backup does not carry an empty source-name list: %s", saved)
	}
	named := strings.Replace(saved, `"sourceNames":[]`, `"sourceNames":[{"source":"AUX","account":"AUX","name":"Turntable"}]`, 1)
	if rec := do(t, h, "POST", "/api/restore", named); rec.Code != 200 {
		t.Fatalf("restore with a source name: %d %s", rec.Code, rec.Body)
	}
	if got := settings.Open(filepath.Join(dir, "settings.json")).Get().SourceNames; len(got) != 1 {
		t.Fatalf("source names after restoring one = %+v, want one", got)
	}
	if rec := do(t, h, "POST", "/api/restore", saved); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	if got := settings.Open(filepath.Join(dir, "settings.json")).Get().SourceNames; len(got) != 0 {
		t.Fatalf("source names after restore = %+v, want none", got)
	}
}

// The settings API refuses a language its own backup restore would refuse, so every
// exported backup stays restorable.
func TestSettingsRefuseALanguageABackupCannotRestore(t *testing.T) {
	h, _ := newServer(t)
	if rec := do(t, h, "PUT", "/api/settings", `{"language":"not-a-language"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT bad language = %d, want 400", rec.Code)
	}
	if rec := do(t, h, "PUT", "/api/settings", `{"language":"pt-BR"}`); rec.Code != http.StatusOK {
		t.Fatalf("PUT pt-BR = %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "POST", "/api/restore", do(t, h, "GET", "/api/backup", "").Body.String()); rec.Code != http.StatusOK {
		t.Fatalf("own backup refused: %d %s", rec.Code, rec.Body)
	}
}

// The Spotify and AirPlay switches survive a backup and restore.
func TestRestoreBringsBackIntegrations(t *testing.T) {
	h, _ := newServer(t)
	do(t, h, "PUT", "/api/settings", `{"integrations":{"spotify":false,"airplay":false}}`)
	saved := do(t, h, "GET", "/api/backup", "").Body.String()
	do(t, h, "PUT", "/api/settings", `{"integrations":{"spotify":true,"airplay":true}}`)
	if rec := do(t, h, "POST", "/api/restore", saved); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Integrations map[string]bool `json:"integrations"`
	}
	decodeBody(t, do(t, h, "GET", "/api/settings", ""), &got)
	if got.Integrations["spotify"] || got.Integrations["airplay"] {
		t.Errorf("integrations after restore = %v, want both off", got.Integrations)
	}
}

// An address the speaker will not store says so, so the page can name the address as the
// problem instead of a generic refusal.
func TestPresetUnusableStreamAddressSaysSo(t *testing.T) {
	h, _ := newServer(t)
	rec := do(t, h, "PUT", "/api/presets/2", `{"stationId":"http://127.0.0.1/stream","name":"Mine"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "stream address is not usable") {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
}

func TestBackupCarriesFavorites(t *testing.T) {
	h, _ := newServer(t)
	body := `{"source":"TUNEIN","name":"Radio 2","location":"s12345","art":""}`
	if rec := do(t, h, "POST", "/api/favorites", body); rec.Code != 204 {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	file := do(t, h, "GET", "/api/backup", "").Body.String()
	if rec := do(t, h, "DELETE", "/api/favorites", body); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "POST", "/api/restore", file); rec.Code >= 300 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	if got := do(t, h, "GET", "/api/favorites", "").Body.String(); !strings.Contains(got, "s12345") {
		t.Fatalf("favorites after restore: %s", got)
	}
}

// A full list of favorites with long logo addresses is a valid export, so it must restore.
func TestRestoreTakesTheLargestValidBackup(t *testing.T) {
	h, _ := newServer(t)
	art := "https://example.org/?" + strings.Repeat("a&\\\"", 675) // a quote per unit: the export escapes it
	for i := range favorites.Limit {
		body := fmt.Sprintf(`{"source":"TUNEIN","name":"Radio %d","location":"s%d","art":"%s"}`, i, i+1, art)
		if rec := do(t, h, "POST", "/api/favorites", body); rec.Code != 204 {
			t.Fatalf("add %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	file := do(t, h, "GET", "/api/backup", "").Body.String()
	if len(file) <= web.MaxRequestBody || len(file) > web.BackupMaxBytes {
		t.Fatalf("export is %d bytes; want above %d and at most %d", len(file), web.MaxRequestBody, web.BackupMaxBytes)
	}
	if rec := do(t, h, "DELETE", "/api/favorites", `{"source":"TUNEIN","location":"s1"}`); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "POST", web.RestorePath, file); rec.Code >= 300 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	var got []favorites.Entry
	if err := json.Unmarshal(do(t, h, "GET", "/api/favorites", "").Body.Bytes(), &got); err != nil || len(got) != favorites.Limit {
		t.Fatalf("favorites after restore: %d entries, err %v", len(got), err)
	}
	// Larger than any valid export stays refused, and changes nothing.
	huge := strings.Replace(file, `"note":"`, `"note":"`+strings.Repeat("x", web.BackupMaxBytes), 1)
	if rec := do(t, h, "POST", web.RestorePath, huge); rec.Code < 400 {
		t.Fatalf("oversized restore: %d, want a refusal", rec.Code)
	}
}

func TestFavoriteMustBePlayable(t *testing.T) {
	h, _ := newServer(t)
	for _, body := range []string{
		`{"source":"AIRPLAY","name":"x","location":"s1"}`,
		`{"source":"TUNEIN","name":"x","location":"not an id"}`,
		`{"source":"SPOTIFY","name":"x","location":"s1"}`,
		`{"source":"TUNEIN","name":" ","location":"s1"}`,
		`{"source":"TUNEIN","name":"x","location":"http://"}`,
		`{"source":"TUNEIN","name":"x","location":"https:///x"}`,
	} {
		if rec := do(t, h, "POST", "/api/favorites", body); rec.Code != 400 {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
}

// noZeroField fails for every field of v, at any depth, that still has its zero value.
func noZeroField(t *testing.T, path string, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			noZeroField(t, path+"."+v.Type().Field(i).Name, v.Field(i))
		}
	case reflect.Slice:
		if v.Len() == 0 {
			t.Errorf("%s is empty: give it a value so the round trip proves it", path)
		}
		for i := range v.Len() {
			noZeroField(t, path, v.Index(i))
		}
	default:
		if v.IsZero() {
			t.Errorf("%s is zero: give it a value so the round trip proves it", path)
		}
	}
}

// Every setting the speaker stores comes back from a backup on a fresh speaker, apart
// from the three the backup leaves out on purpose: the MQTT password, the schema version
// and the crashes already reported or dismissed. The fixture must set every
// field, so a setting added later is part of this test from the day it exists.
func TestRestoreRoundTripsEverySetting(t *testing.T) {
	full := settings.Settings{
		SchemaVersion: 1,
		Name:          "Keuken",
		SourceNames:   []settings.SourceName{{Source: "AUX", Account: "AUX", Name: "Platenspeler"}},
		Language:      "nl",
		MQTT: settings.MQTT{
			Enabled: true, Host: "broker.example", Port: 8883, Username: "speaker", Password: "secret",
			BaseTopic: "soundtap/keuken", DiscoveryPrefix: "ha", TLS: true,
		},
		Audio:        settings.Audio{Volume: 23, Bass: -3, HasVolume: true, AutoOff: 45},
		Display:      settings.Display{ClockOff: true, Hour12: true, City: "Amsterdam", Lat: 52.37, Lon: 4.89, Timezone: "Europe/Amsterdam", WeatherOn: true, Brightness: 50},
		Alarm:        settings.Alarm{Enabled: true, Time: "06:45", Slot: 3, Timezone: "Europe/Amsterdam"},
		Multiroom:    settings.Multiroom{DissolveGroups: true, SyncVolume: true},
		Integrations: settings.Integrations{SpotifyOff: true, AirPlayOff: true},
		Channel:      "left",
		AuxName:      "Tape",
		CrashReports: settings.CrashReports{Enabled: true, Handled: []string{"panic-0123456789ab"}},
	}
	noZeroField(t, "Settings", reflect.ValueOf(full))

	stored := func(dir string) settings.Settings {
		t.Helper()
		var got settings.Settings
		raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	from := t.TempDir()
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	old, _, _ := newServerAt(t, from)
	file := do(t, old.Handler(), "GET", "/api/backup", "").Body.String()
	if strings.Contains(file, full.MQTT.Password) {
		t.Fatal("the backup carries the MQTT password")
	}

	fresh, _, to := newServerSrv(t)
	if rec := do(t, fresh.Handler(), "POST", "/api/restore", file); rec.Code != 200 {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	got, want := stored(to), full
	want.MQTT.Password = ""
	want.CrashReports.Handled = nil // crashes belong to the speaker they happened on
	got.SchemaVersion = want.SchemaVersion
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after restore:\n got %+v\nwant %+v", got, want)
	}
}
