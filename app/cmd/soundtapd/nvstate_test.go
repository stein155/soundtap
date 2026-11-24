// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/bco"
	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/hw"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/lpm"
	"github.com/stein155/soundtap/app/internal/multiroom"
	"github.com/stein155/soundtap/app/internal/player"
	"github.com/stein155/soundtap/app/internal/recent"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/store"
)

// app/testdata/nv-<release> holds what that release leaves on /mnt/nv, laid out as on the
// speaker (ADR 0120, docs/contributors/nv-state.md). Every build must load every set without losing a
// value. The newest set is also what the current writers produce, so a format change
// fails here: before 0.1.0 rewrite the set with -update-nv; from 0.1.0 on add a migration
// and a new set, and leave the old ones as they are.
const (
	nvFixtures = "../../testdata"
	nvCurrent  = "nv-0.1.0"
	nvDoc      = "../../../docs/contributors/nv-state.md"
)

var updateNV = flag.Bool("update-nv", false, "rewrite app/testdata/"+nvCurrent+" from the current writers")

// nvFiles is every name docs/contributors/nv-state.md lists, relative to /mnt/nv. A name with an empty
// reason has a file in the newest fixture set; a reason says why it has none. User state
// always has one (TestNVEveryListedFileHasAFixture). A new file on /mnt/nv adds a row to
// the doc, a line here and, unless it is a log or another program's, a fixture and a check
// in loadNV.
var nvFiles = map[string]string{
	"soundtap/presets.json.settings": "",
	"soundtap/presets.json":          "",
	"soundtap/favorites.json":        "",
	"soundtap/recent.json":           "",
	"soundtap/tunein-streams.json":   "",
	"soundtap/multiroom-server":      "", // loaded in app/internal/web/nvstate_test.go
	"soundtap/bar-source":            "",
	"soundtap/bar-audio-mode":        "",
	"soundtap/bar-cec-mode":          "",
	"soundtap/bar-hdmi-input":        "",
	"soundtap/firmware-pending.json": "",
	"soundtap/.stock-imported":       "",
	"soundtap/multiroom-enabled":     "",
	"soundtap/hostname":              "",
	"soundtap/bco-image":             "",
	"wifi.conf":                      "", // also joined by the Wi-Fi manager in app/internal/wifi
	"wifi.conf.good":                 "", // read by the Wi-Fi manager only: app/internal/wifi
	"wifi.conf.known":                "",
	"ir.map":                         "",
	"buttons.map":                    "",
	"ntp-servers":                    "", // read by clock.sh only: os/tests/boot/test_clock_keep.py

	"soundtap/st20-eth0":                "machine state written by hand from stock's address; read by the init script and app/internal/bco",
	"soundtap/codec/":                   "the speaker's own codec script: stock bytes, never committed (ADR 0012, ADR 0049)",
	"soundtap/soundtapd.override":       "an uploaded executable; its fallbacks are tested in app/internal/web/firmware",
	"soundtap/soundtapd.override.gz":    "an uploaded executable; its fallbacks are tested in app/internal/web/firmware",
	"soundtap/soundtapd.override.base":  "an uploaded executable; its fallbacks are tested in app/internal/web/firmware",
	"soundtap/soundtapd.override.trial": "an uploaded executable; its fallbacks are tested in app/internal/web/firmware",
	"soundtap/allow-unsigned":           "alpha-only presence marker; tested in app/internal/web/firmware",
	"soundtap-clock":                    "machine state the init script reads; lost, the clock waits for NTP",
	"model":                             "machine state nothing of ours writes; os/tests/boot/test_detect_model.py reads one",
	"charger_active_level":              "stock's file (Portable); read by runBattery, never written",
	"soundtap/snapserver/":              "snapserver's own format (ADR 0120)",
	"bluetooth/":                        "BlueZ's own format (ADR 0120)",
	"bluealsa/":                         "bluealsa's own format (ADR 0120)",
	"spotify/":                          "librespot's own format (ADR 0120)",
	"dropbear/":                         "Dropbear's own format (ADR 0120)",
	"BoseApp-Persistence/1/":            "Bose's files, read once after an install from stock; never committed (ADR 0049)",
	"soundtap/current-boot.log":         "log",
	"soundtap/previous-boot.log":        "log",
	"soundtap/previous-boot.1.log":      "log",
	"soundtap/previous-boot.2.log":      "log",
	"soundtap/panic.log":                "log",
	"soundtap/last-boot/":               "log",
	"soundtap-boot.log":                 "log",
	"soundtap-netfail.log":              "log",
}

// Every field set and away from its zero value, so a field the loader drops shows.
var (
	nvSettings = settings.Settings{
		SchemaVersion: settings.SchemaVersion,
		Name:          "Kitchen",
		SourceNames:   []settings.SourceName{{Source: "SPOTIFY", Account: "anna", Name: "Anna's Spotify"}},
		Language:      "nl",
		MQTT: settings.MQTT{Enabled: true, Host: "broker.lan", Port: 8883, Username: "speaker",
			Password: "secret", BaseTopic: "soundtap/kitchen", DiscoveryPrefix: "ha", TLS: true},
		Audio:        settings.Audio{Volume: 23, Bass: -3, HasVolume: true, AutoOff: -1},
		Display:      settings.Display{ClockOff: true, Hour12: true, Timezone: "Europe/Amsterdam", Brightness: 50},
		Alarm:        settings.Alarm{Enabled: true, Time: "06:45", Slot: 3, Timezone: "Europe/Amsterdam"},
		Multiroom:    settings.Multiroom{DissolveGroups: true, SyncVolume: true},
		Integrations: settings.Integrations{SpotifyOff: true, AirPlayOff: true},
		Channel:      "left",
		AuxName:      "Turntable",
		CrashReports: settings.CrashReports{Enabled: true, Handled: []string{"panic-0123456789ab"}},
	}
	nvPresets = []store.Preset{
		{Slot: 1, StationID: "s24896", Name: "Radio 1", Logo: "https://example.com/radio1.png"},
		{Slot: 6, Source: "SPOTIFY", Type: "tracklisturl", Location: "spotify:playlist:0000000000000000000000",
			Name: "Sunday", Logo: "https://example.com/sunday.jpg"},
	}
	nvFavorites = []favorites.Entry{
		{Source: "TUNEIN", Name: "Jazz", Location: "s12345", Art: "https://example.com/jazz.png"},
		{Source: "LOCAL_INTERNET_RADIO", Name: "My stream", Location: "https://example.com/stream.mp3"},
	}
	nvRecent = []recent.Entry{ // newest first, as the store keeps it
		{Source: "SPOTIFY", Name: "Sunday", Location: "spotify:playlist:0000000000000000000000",
			Art: "https://example.com/sunday.jpg", StoppedAt: time.Date(2026, 11, 12, 8, 30, 0, 0, time.UTC)},
		{Source: "TUNEIN", Name: "Jazz", Location: "s12345", StoppedAt: time.Date(2026, 11, 12, 7, 0, 0, 0, time.UTC)},
	}
	nvPending = func() install.Pending {
		p := install.Pending{TargetSlot: "PONG"}
		p.Package.Size, p.Package.Kernel, p.Package.Models = 41943040, "0123456789abcdef", []string{"st10", "st20", "wla"}
		return p
	}()
	nvStreams = map[string]player.CachedStreams{
		"s24896": {URLs: []string{"https://example.com/radio1.mp3", "https://example.com/radio1.aac"},
			At: time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)},
	}
	nvBarSource            uint32 = lpm.SourceTV
	nvAudio, nvCEC, nvHDMI        = "dialog", "alternate", "2"
	// What the bco-image marker in the fixtures names: a stand-in, not a BCO image.
	nvBCOImage = []byte("soundtap nv fixture: not a BCO image\n")
)

// writeNV writes the sample state into nv with the current writers.
func writeNV(t *testing.T, nv string) {
	t.Helper()
	home := filepath.Join(nv, "soundtap")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	set := settings.Open(filepath.Join(home, "presets.json.settings"))
	must(t, set.Update(func(s *settings.Settings) error { *s = nvSettings; return nil }))
	presets, err := store.Open(filepath.Join(home, "presets.json"))
	must(t, err)
	for _, p := range nvPresets {
		must(t, presets.Set(p))
	}
	must(t, favorites.Open(filepath.Join(home, "favorites.json")).Replace(nvFavorites))
	rec := recent.Open(filepath.Join(home, "recent.json"))
	for i := len(nvRecent) - 1; i >= 0; i-- {
		must(t, rec.Record(nvRecent[i]))
	}
	// The same calls the firmware handler makes before it reboots into the new slot and
	// streamCache.put makes (put stamps the time now).
	must(t, atomicjson.Write(filepath.Join(home, install.PendingPath), nvPending, 0o600))
	must(t, atomicjson.Write(filepath.Join(home, "tunein-streams.json"), nvStreams, 0o600))
	must(t, markStockTaken(home))
	must(t, multiroom.PersistActive(filepath.Join(home, "multiroom-enabled"), true))
	pointHostnameAt(t, home)
	publishHostname(nvSettings.Name)
	flashed, err := bco.AutoInstall(context.Background(), nvBCOImageFile(t), filepath.Join(home, "bco-image"),
		func() bool { return true }, func(context.Context, []byte) (string, error) { return "fixture", nil }, io.Discard)
	if err != nil || !flashed {
		t.Fatalf("bco.AutoInstall = %v, %v", flashed, err)
	}
	must(t, persistWifiProfile(filepath.Join(nv, "wifi.conf"), "home", "homepass1", quietLog()))
	pointLPMAt(t, home)
	must(t, soundbar.SaveSource(nvBarSource))
	for c, v := range map[*lpm.Choice]string{soundbar.AudioModes: nvAudio, soundbar.CECModes: nvCEC, soundbar.HDMIInputs: nvHDMI} {
		c.Keep(v)
	}
}

// TestNVFormatsAreTheWritersOwn keeps the newest fixture set equal to what this build writes.
func TestNVFormatsAreTheWritersOwn(t *testing.T) {
	got := t.TempDir()
	writeNV(t, got)
	want := filepath.Join(nvFixtures, nvCurrent)
	must(t, filepath.WalkDir(got, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(got, path)
		b, _ := os.ReadFile(path)
		if *updateNV {
			must(t, os.MkdirAll(filepath.Dir(filepath.Join(want, rel)), 0o755))
			return os.WriteFile(filepath.Join(want, rel), b, 0o644)
		}
		if old, err := os.ReadFile(filepath.Join(want, rel)); err != nil || !bytes.Equal(old, b) {
			t.Errorf("%s: this build writes another format than %s holds:\n%s\nBefore 0.1.0 run "+
				"go test -run TestNVFormats -update-nv; from 0.1.0 on add a migration and a new set (ADR 0120)",
				rel, nvCurrent, b)
		}
		return nil
	}))
}

// TestNVFixturesLoadWithoutLoss is the update matrix: every release's files (N-1), copied
// to a fresh /mnt/nv, load in this build (N) through the loaders soundtapd uses, nothing is
// moved aside, and what this build then saves loads again.
func TestNVFixturesLoadWithoutLoss(t *testing.T) {
	sets, _ := filepath.Glob(filepath.Join(nvFixtures, "nv-*"))
	if len(sets) == 0 {
		t.Fatal("no fixture sets in", nvFixtures)
	}
	for _, set := range sets {
		t.Run(filepath.Base(set), func(t *testing.T) {
			nv := t.TempDir() // a copy: loaders may rewrite what they read
			must(t, os.CopyFS(nv, os.DirFS(set)))
			loadNV(t, nv, nvSettings)
			noneMovedAside(t, nv)
			saveNV(t, nv)
			loadNV(t, nv, nvSettings)
			noneMovedAside(t, nv)
		})
	}
}

// TestNVRollbackLeavesNewerFilesInPlace is the other direction (N -> N-1): after a rollback
// to the other slot the older build reads what the newer one wrote. The older build is not
// in this tree, so this build plays it: the files the current writers produce get a field
// only a later build knows, and the settings file a schema one past SchemaVersion. ADR
// 0120 promises the older build reads every value it knows, or for the settings refuses
// writes, and that opening never moves aside, rewrites or deletes a file.
func TestNVRollbackLeavesNewerFilesInPlace(t *testing.T) {
	// The newest set is what this build writes (TestNVFormatsAreTheWritersOwn), plus the
	// files people write by hand.
	nv := t.TempDir()
	must(t, os.CopyFS(nv, os.DirFS(filepath.Join(nvFixtures, nvCurrent))))
	home := filepath.Join(nv, "soundtap")
	for name, each := range map[string]bool{
		"presets.json.settings": false, install.PendingPath: false,
		"presets.json": true, "favorites.json": true, "recent.json": true, "tunein-streams.json": true,
	} {
		addLaterField(t, filepath.Join(home, name), each)
	}
	newer := nvSettings
	newer.SchemaVersion = settings.SchemaVersion + 1
	setJSONField(t, filepath.Join(home, "presets.json.settings"), "schemaVersion", newer.SchemaVersion)

	before := snapshotNV(t, nv)
	loadNV(t, nv, newer)
	noneMovedAside(t, nv)
	if after := snapshotNV(t, nv); !reflect.DeepEqual(after, before) {
		for name, b := range before {
			if !bytes.Equal(after[name], b) {
				t.Errorf("%s: opened by the older build, it changed:\n%s", name, after[name])
			}
		}
		t.Fatalf("files after opening: %d, before: %d", len(after), len(before))
	}

	set := settings.Open(filepath.Join(home, "presets.json.settings"))
	if !errors.Is(set.LoadErr(), settings.ErrNewerSchema) {
		t.Errorf("settings LoadErr = %v, want ErrNewerSchema", set.LoadErr())
	}
	if err := set.SetName("Renamed"); err == nil {
		t.Error("the older build saved over a newer settings file")
	}
	if b, _ := os.ReadFile(filepath.Join(home, "presets.json.settings")); !bytes.Equal(b, before["soundtap/presets.json.settings"]) {
		t.Errorf("a refused save changed the newer settings file:\n%s", b)
	}
	// The files without a version take the older build's saves. A field only the newer
	// build knew may go (ADR 0120 does not promise it back), every other value stays.
	saveNV(t, nv)
	loadNV(t, nv, newer)
	noneMovedAside(t, nv)
}

// TestNVEveryListedFileHasAFixture keeps nvFiles, docs/contributors/nv-state.md and the newest fixture
// set in step: every listed name is in nvFiles and the other way round, user state has a
// fixture, every name without a reason has one, and the set holds nothing unlisted.
func TestNVEveryListedFileHasAFixture(t *testing.T) {
	doc, err := os.ReadFile(nvDoc)
	must(t, err)
	kinds := nvDocFiles(string(doc))
	if len(kinds) < 20 {
		t.Fatalf("read %d names from %s; did its tables change shape?", len(kinds), nvDoc)
	}
	for name, kind := range kinds {
		reason, ok := nvFiles[name]
		switch {
		case !ok:
			t.Errorf("%s is listed in docs/contributors/nv-state.md but not in nvFiles", name)
		case strings.HasPrefix(kind, "User") && reason != "":
			t.Errorf("%s is user state (%s): it needs a fixture in app/testdata/%s, not a reason", name, kind, nvCurrent)
		}
	}
	set := filepath.Join(nvFixtures, nvCurrent)
	for name, reason := range nvFiles {
		if _, ok := kinds[name]; !ok {
			t.Errorf("%s is in nvFiles but not in docs/contributors/nv-state.md", name)
		}
		if _, err := os.Stat(filepath.Join(set, name)); reason == "" && err != nil {
			t.Errorf("%s has no fixture in %s: add one written by the current writer (ADR 0120)", name, set)
		}
	}
	must(t, filepath.WalkDir(set, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(set, path)
		if reason, ok := nvFiles[filepath.ToSlash(rel)]; !ok || reason != "" {
			t.Errorf("%s in %s is not a listed fixture in nvFiles", rel, set)
		}
		return nil
	}))
}

// nvDocFiles reads the names and kinds of the file tables in docs/contributors/nv-state.md, relative to
// /mnt/nv. "`a` (`.x`, `.y`)" names a, a.x and a.y.
func nvDocFiles(doc string) map[string]string {
	tick := regexp.MustCompile("`([^`]+)`")
	out := map[string]string{}
	dir := ""
	for _, line := range strings.Split(doc, "\n") {
		switch {
		case strings.HasPrefix(line, "## `/mnt/nv/soundtap`"):
			dir = "soundtap/"
		case strings.HasPrefix(line, "## `/mnt/nv`"):
			dir = ""
		case strings.HasPrefix(line, "| `"):
			cells := strings.Split(line, "|")
			if len(cells) < 3 {
				continue
			}
			names, kind := cells[1], strings.TrimSpace(cells[2])
			base, suffixes, _ := strings.Cut(names, "(")
			var first string
			for _, m := range tick.FindAllStringSubmatch(base, -1) {
				if first == "" {
					first = m[1]
				}
				out[dir+m[1]] = kind
			}
			for _, m := range tick.FindAllStringSubmatch(suffixes, -1) {
				out[dir+first+m[1]] = kind
			}
		}
	}
	return out
}

func loadNV(t *testing.T, nv string, wantSettings settings.Settings) {
	t.Helper()
	home := filepath.Join(nv, "soundtap")
	check := func(what string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", what, got, want)
		}
	}
	set := settings.Open(filepath.Join(home, "presets.json.settings"))
	check("settings", set.Get(), wantSettings)
	if wantSettings.SchemaVersion == settings.SchemaVersion {
		check("settings load error", set.LoadErr(), nil)
	}
	presets, err := store.Open(filepath.Join(home, "presets.json"))
	must(t, err)
	check("presets", presets.All(), nvPresets)
	fav := favorites.Open(filepath.Join(home, "favorites.json"))
	check("favorites", []any{fav.All(), fav.LoadErr()}, []any{nvFavorites, nil})
	rec := recent.Open(filepath.Join(home, "recent.json"))
	check("recent", []any{rec.All(), rec.LoadErr()}, []any{nvRecent, nil})
	recovery := install.LoadRecovery(home, func() (string, error) { return "ubi.mtd=9,2048", nil })
	check("firmware-pending", []any{recovery.Status, recovery.Pending}, []any{"booted", nvPending})
	check(".stock-imported keeps stock's name and presets out", takeStockOnce(home, quietLog()), false)
	streams := &player.StreamCache{}
	streams.Load(filepath.Join(home, "tunein-streams.json"))
	check("tunein-streams.json", streams.All(), nvStreams)
	_, err = os.Stat(filepath.Join(home, "multiroom-enabled")) // the init script's test is presence
	check("multiroom-enabled", err, nil)

	// The init script names the host from this file before soundtapd starts; soundtapd
	// leaves one that already holds the name alone.
	hostname := filepath.Join(home, "hostname")
	old, err := os.ReadFile(hostname)
	must(t, err)
	pointHostnameAt(t, home)
	publishHostname(wantSettings.Name)
	now, _ := os.ReadFile(hostname)
	check("hostname", []string{string(old), string(now)}, []string{"kitchen\n", "kitchen\n"})

	// The marker of an image that is installed and running: no BCO flash at boot.
	marker := filepath.Join(home, "bco-image")
	old, _ = os.ReadFile(marker)
	flashed, err := bco.AutoInstall(context.Background(), nvBCOImageFile(t), marker, func() bool { return true },
		func(context.Context, []byte) (string, error) {
			t.Error("bco-image: an installed image was flashed again")
			return "", errors.New("no flash in a test")
		}, io.Discard)
	now, _ = os.ReadFile(marker)
	check("bco-image", []any{flashed, err, string(now)}, []any{false, nil, string(old)})

	conf := filepath.Join(nv, "wifi.conf")
	b, err := os.ReadFile(conf)
	must(t, err)
	check("wifi.conf has a network", savedNetwork(b), true)
	known, err := savedWifiProfiles(conf)
	must(t, err)
	var ssids []string
	for _, k := range known {
		ssids = append(ssids, k.SSID)
	}
	check("wifi.conf.known", ssids, []string{"home", "work", "cafe"})

	pointLPMAt(t, home)
	source, ok := soundbar.SavedInput()
	check("bar-source", []any{source, ok}, []any{nvBarSource, true})
	for file, c := range map[string]struct {
		choice *lpm.Choice
		want   string
	}{"bar-audio-mode": {soundbar.AudioModes, nvAudio}, "bar-cec-mode": {soundbar.CECModes, nvCEC}, "bar-hdmi-input": {soundbar.HDMIInputs, nvHDMI}} {
		c.choice.Load()
		check(file, c.choice.Get(), c.want)
	}

	ir := hw.LoadIRKeymap(filepath.Join(nv, "ir.map"), quietLog())
	check("ir.map", ir, map[byte]string{0x10: "POWER", 0x11: "VOLUME_UP", 0x12: "PRESET_1"})
	keys := hw.LoadKeymap(filepath.Join(nv, "buttons.map"))
	check("buttons.map", []string{keys[0], keys[4]}, []string{"VOLUME_DOWN", "VOLUME_UP"})
}

// saveNV saves every JSON store once with this build, as the first change after an
// update does. The settings save is skipped when the file is from a newer build: that one
// is refused, which TestNVRollbackLeavesNewerFilesInPlace checks.
func saveNV(t *testing.T, nv string) {
	t.Helper()
	home := filepath.Join(nv, "soundtap")
	set := settings.Open(filepath.Join(home, "presets.json.settings"))
	if set.LoadErr() == nil {
		must(t, set.Update(func(*settings.Settings) error { return nil }))
	}
	presets, err := store.Open(filepath.Join(home, "presets.json"))
	must(t, err)
	must(t, presets.Set(presets.All()[0]))
	fav := favorites.Open(filepath.Join(home, "favorites.json"))
	must(t, fav.Replace(fav.All()))
	rec := recent.Open(filepath.Join(home, "recent.json"))
	must(t, rec.Record(rec.All()[0]))
	streams := &player.StreamCache{}
	streams.Load(filepath.Join(home, "tunein-streams.json"))
	must(t, atomicjson.Write(filepath.Join(home, "tunein-streams.json"), streams.All(), 0o600))
}

// noneMovedAside fails when a loader quarantined a file it should have read.
func noneMovedAside(t *testing.T, nv string) {
	t.Helper()
	must(t, filepath.WalkDir(nv, func(path string, d fs.DirEntry, err error) error {
		if err == nil && strings.Contains(d.Name(), ".corrupt") {
			t.Errorf("%s: a loader moved a fixture aside", path)
		}
		return err
	}))
}

func snapshotNV(t *testing.T, nv string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	must(t, filepath.WalkDir(nv, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(nv, path)
		out[filepath.ToSlash(rel)], err = os.ReadFile(path)
		return err
	}))
	return out
}

// addLaterField adds a field this build does not know to the object in path, or with
// each to every object in its array or map, as a later build might.
func addLaterField(t *testing.T, path string, each bool) {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	var v any
	must(t, json.Unmarshal(b, &v))
	add := func(o any) {
		if m, ok := o.(map[string]any); ok {
			m["addedLater"] = map[string]any{"by": "a later build"}
		} else {
			t.Fatalf("%s: %T is not an object", path, o)
		}
	}
	switch c := v.(type) {
	case []any:
		for _, o := range c {
			add(o)
		}
	case map[string]any:
		if !each {
			add(c)
			break
		}
		for _, o := range c {
			add(o)
		}
	}
	b, err = json.MarshalIndent(v, "", "  ")
	must(t, err)
	must(t, os.WriteFile(path, b, 0o600))
}

func setJSONField(t *testing.T, path, key string, value any) {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	var m map[string]any
	must(t, json.Unmarshal(b, &m))
	m[key] = value
	b, err = json.MarshalIndent(m, "", "  ")
	must(t, err)
	must(t, os.WriteFile(path, b, 0o600))
}

// nvBCOImageFile is the image the bco-image marker in the fixtures names.
func nvBCOImageFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bco-app.bcd")
	must(t, os.WriteFile(path, nvBCOImage, 0o644))
	return path
}

// pointHostnameAt moves the host name file into home for one test.
func pointHostnameAt(t *testing.T, home string) {
	t.Helper()
	oldFile, oldSet := hostnameFile, setHostname
	hostnameFile, setHostname = filepath.Join(home, "hostname"), func([]byte) error { return nil }
	t.Cleanup(func() { hostnameFile, setHostname = oldFile, oldSet })
}

// pointLPMAt moves the soundbar's saved choices into home for one test.
func pointLPMAt(t *testing.T, home string) {
	t.Helper()
	old := soundbar
	soundbar = lpm.New(home, version)
	t.Cleanup(func() { soundbar = old })
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
