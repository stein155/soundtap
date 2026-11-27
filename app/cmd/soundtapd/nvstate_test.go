// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
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

//nolint:gochecknoglobals // a test flag: go test parses the flags registered at package level
var updateNV = flag.Bool("update-nv", false, "rewrite app/testdata/"+nvCurrent+" from the current writers")

// nvFileReasons is every name docs/contributors/nv-state.md lists, relative to /mnt/nv. A name with an empty
// reason has a file in the newest fixture set; a reason says why it has none. User state
// always has one (TestNVEveryListedFileHasAFixture). A new file on /mnt/nv adds a row to
// the doc, a line here and, unless it is a log or another program's, a fixture and a check
// in loadNV.
func nvFileReasons() map[string]string {
	return map[string]string{
		"soundtap/presets.json.settings":      "",
		"soundtap/presets.json":               "",
		"soundtap/favorites.json":             "",
		"soundtap/recent.json":                "",
		"soundtap/tunein-streams.json":        "",
		"soundtap/multiroom-server":           "", // loaded in app/internal/web/nvstate_test.go
		"soundtap/bar-source":                 "",
		"soundtap/bar-audio-mode":             "",
		"soundtap/bar-cec-mode":               "",
		"soundtap/bar-hdmi-input":             "",
		"soundtap/firmware-pending.json":      "",
		"soundtap/.stock-imported":            "",
		"soundtap/stock-presets-pending.json": "",
		"soundtap/multiroom-enabled":          "",
		"soundtap/hostname":                   "",
		"soundtap/bco-image":                  "",
		"wifi.conf":                           "", // also joined by the Wi-Fi manager in app/internal/wifi
		"wifi.conf.good":                      "", // read by the Wi-Fi manager only: app/internal/wifi
		"wifi.conf.known":                     "",
		"ir.map":                              "",
		"buttons.map":                         "",
		"ntp-servers":                         "", // read by clock.sh only: os/tests/boot/test_clock_keep.py

		"soundtap/st20-eth0": "machine state written by hand from stock's address; " +
			"read by the init script and app/internal/bco",
		"soundtap/codec/": "the speaker's own codec script: " +
			"stock bytes, never committed (ADR 0012, ADR 0049)",
		"soundtap/soundtapd.override": "an uploaded executable; " +
			"its fallbacks are tested in app/internal/web/firmware",
		"soundtap/soundtapd.override.gz": "an uploaded executable; " +
			"its fallbacks are tested in app/internal/web/firmware",
		"soundtap/soundtapd.override.base": "an uploaded executable; " +
			"its fallbacks are tested in app/internal/web/firmware",
		"soundtap/soundtapd.override.trial": "an uploaded executable; " +
			"its fallbacks are tested in app/internal/web/firmware",
		"soundtap-clock": "machine state the init script reads; lost, the clock waits for NTP",
		"model": "machine state nothing of ours writes; " +
			"os/tests/boot/test_detect_model.py reads one",
		"charger_active_level": "stock's file (Portable); read by runBattery, never written",
		"soundtap/snapserver/": "snapserver's own format (ADR 0120)",
		"bluetooth/":           "BlueZ's own format (ADR 0120)",
		"bluealsa/":            "bluealsa's own format (ADR 0120)",
		"spotify/":             "librespot's own format (ADR 0120)",
		"dropbear/":            "Dropbear's own format (ADR 0120)",
		"BoseApp-Persistence/1/": "Bose's files, read once after an install from stock; " +
			"never committed (ADR 0049)",
		"soundtap/current-boot.log":    "log",
		"soundtap/previous-boot.log":   "log",
		"soundtap/previous-boot.1.log": "log",
		"soundtap/previous-boot.2.log": "log",
		"soundtap/panic.log":           "log",
		"soundtap/last-boot/":          "log",
		"soundtap-boot.log":            "log",
		"soundtap-netfail.log":         "log",
	}
}

// Every field set and away from its zero value, so a field the loader drops shows.

// nvSettingsFixture is the settings file of the fixtures.
func nvSettingsFixture() settings.Settings {
	return settings.Settings{
		SchemaVersion: settings.SchemaVersion,
		Name:          "Kitchen",
		SourceNames:   []settings.SourceName{{Source: "SPOTIFY", Account: "anna", Name: "Anna's Spotify"}},
		Language:      "nl",
		MQTT: settings.MQTT{
			Enabled: true, Host: "broker.lan", Port: 8883, Username: "speaker",
			Password: "secret", BaseTopic: "soundtap/kitchen", DiscoveryPrefix: "ha", TLS: true,
		},
		Audio:        settings.Audio{Volume: 23, Bass: -3, HasVolume: true, AutoOff: -1},
		Display:      settings.Display{ClockOff: true, Hour12: true, Timezone: "Europe/Amsterdam"},
		Alarm:        settings.Alarm{Enabled: true, Time: "06:45", Slot: 3, Timezone: "Europe/Amsterdam"},
		Multiroom:    settings.Multiroom{DissolveGroups: true, SyncVolume: true},
		Integrations: settings.Integrations{SpotifyOff: true, AirPlayOff: true},
		Channel:      "left",
		AuxName:      "Turntable",
		CrashReports: settings.CrashReports{Enabled: true, Handled: []string{"panic-0123456789ab"}},
	}
}

// nvPresetsFixture is the presets file of the fixtures.
func nvPresetsFixture() []store.Preset {
	return []store.Preset{
		{
			Slot: 1, StationID: "s24896", Source: "", Type: "", Location: "",
			Name: "Radio 1", Logo: "https://example.com/radio1.png",
		},
		{
			Slot: 6, StationID: "", Source: "SPOTIFY", Type: "tracklisturl",
			Location: "spotify:playlist:0000000000000000000000",
			Name:     "Sunday", Logo: "https://example.com/sunday.jpg",
		},
	}
}

// nvFavoritesFixture is the favorites file of the fixtures.
func nvFavoritesFixture() []favorites.Entry {
	return []favorites.Entry{
		{Source: "TUNEIN", Name: "Jazz", Location: "s12345", Art: "https://example.com/jazz.png"},
		{Source: "LOCAL_INTERNET_RADIO", Name: "My stream", Location: "https://example.com/stream.mp3", Art: ""},
	}
}

// nvRecentFixture is the recent file of the fixtures, newest first, as the store keeps it.
func nvRecentFixture() []recent.Entry {
	return []recent.Entry{
		{
			Source: "SPOTIFY", Name: "Sunday", Location: "spotify:playlist:0000000000000000000000",
			Art: "https://example.com/sunday.jpg", StoppedAt: time.Date(2026, 11, 12, 8, 30, 0, 0, time.UTC),
		},
		{
			Source: "TUNEIN", Name: "Jazz", Location: "s12345", Art: "",
			StoppedAt: time.Date(2026, 11, 12, 7, 0, 0, 0, time.UTC),
		},
	}
}

// nvPendingFixture is the firmware-pending file of the fixtures.
func nvPendingFixture() install.Pending {
	var pending install.Pending

	pending.TargetSlot = "PONG"
	pending.Package.Size, pending.Package.Kernel = 41943040, "0123456789abcdef"
	pending.Package.Models = []string{"st10", "st20", "wla"}

	return pending
}

// nvStockPendingFixture is the stock Radio Browser presets waiting in the fixtures.
func nvStockPendingFixture() []pendingStockPreset {
	return []pendingStockPreset{{
		Slot: 2, UUID: "00000000-0000-4000-8000-000000000001", Name: "Radio Example",
		Logo: "https://example.com/radio.png", FirstSeen: time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC),
	}}
}

// nvStreamsFixture is the TuneIn stream cache of the fixtures.
func nvStreamsFixture() map[string]player.CachedStreams {
	return map[string]player.CachedStreams{
		"s24896": {
			URLs: []string{"https://example.com/radio1.mp3", "https://example.com/radio1.aac"},
			At:   time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC),
		},
	}
}

// The soundbar's saved choices in the fixtures.
const (
	nvBarSource uint32 = lpm.SourceTV
	nvAudio            = "dialog"
	nvCEC              = "alternate"
	nvHDMI             = "2"
)

// nvBCOImage is what the bco-image marker in the fixtures names: a stand-in, not a BCO image.
const nvBCOImage = "soundtap nv fixture: not a BCO image\n"

// errNVNoFlash is what a test's BCO flasher answers: nothing is flashed in a test.
var errNVNoFlash = errors.New("no flash in a test")

// writeNV writes the sample state into nv with the current writers.
func writeNV(t *testing.T, nvDir string) {
	t.Helper()

	home := filepath.Join(nvDir, "soundtap")

	err := os.MkdirAll(home, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	set := settings.Open(filepath.Join(home, "presets.json.settings"))
	must(t, set.Update(func(s *settings.Settings) error {
		*s = nvSettingsFixture()

		return nil
	}))

	presets, err := store.Open(filepath.Join(home, "presets.json"))
	must(t, err)

	for _, p := range nvPresetsFixture() {
		must(t, presets.Set(p))
	}

	must(t, favorites.Open(filepath.Join(home, "favorites.json")).Replace(nvFavoritesFixture()))

	rec := recent.Open(filepath.Join(home, "recent.json"))
	for _, n := range slices.Backward(nvRecentFixture()) {
		must(t, rec.Record(n))
	}
	// The same calls the firmware handler makes before it reboots into the new slot and
	// streamCache.put makes (put stamps the time now).
	must(t, atomicjson.Write(filepath.Join(home, install.PendingPath), nvPendingFixture(), 0o600))
	must(t, atomicjson.Write(filepath.Join(home, "tunein-streams.json"), nvStreamsFixture(), 0o600))
	must(t, markStockTaken(home))
	recordPendingStockPresets(filepath.Join(home, stockPendingName), nvStockPendingFixture(),
		nvStockPendingFixture()[0].FirstSeen, quietLog())
	must(t, multiroom.PersistActive(filepath.Join(home, "multiroom-enabled"), true))
	pointHostnameAt(t, home)
	must(t, publishHostname(nvSettingsFixture().Name))

	flashed, err := bco.AutoInstall(context.Background(), nvBCOImageFile(t), filepath.Join(home, "bco-image"),
		func() bool { return true },
		func(context.Context, []byte) (string, error) { return "fixture", nil },
		io.Discard)
	if err != nil || !flashed {
		t.Fatalf("bco.AutoInstall = %v, %v", flashed, err)
	}

	must(t, persistWifiProfile(filepath.Join(nvDir, "wifi.conf"), "home", "", "homepass1", quietLog()))
	pointLPMAt(t, home)
	must(t, soundbar.SaveSource(nvBarSource))

	choices := map[*lpm.Choice]string{
		soundbar.AudioModes: nvAudio, soundbar.CECModes: nvCEC, soundbar.HDMIInputs: nvHDMI,
	}
	for c, v := range choices {
		c.Keep(v)
	}
}

// TestNVFormatsAreTheWritersOwn keeps the newest fixture set equal to what this build writes.
//
//nolint:paralleltest // swaps package variables other tests read: hostnameFile, setHostname, soundbar
func TestNVFormatsAreTheWritersOwn(t *testing.T) {
	got := t.TempDir()
	writeNV(t, got)

	want := filepath.Join(nvFixtures, nvCurrent)

	must(t, filepath.WalkDir(got, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, _ := filepath.Rel(got, path)
		data, _ := os.ReadFile(path)

		if *updateNV {
			must(t, os.MkdirAll(filepath.Dir(filepath.Join(want, rel)), 0o755))

			return os.WriteFile(filepath.Join(want, rel), data, 0o644)
		}

		old, readErr := os.ReadFile(filepath.Join(want, rel))
		if readErr != nil || !bytes.Equal(old, data) {
			t.Errorf("%s: this build writes another format than %s holds:\n%s\nBefore 0.1.0 run "+
				"go test -run TestNVFormats -update-nv; from 0.1.0 on add a migration and a new set (ADR 0120)",
				rel, nvCurrent, data)
		}

		return nil
	}))
}

// TestNVFixturesLoadWithoutLoss is the update matrix: every release's files (N-1), copied
// to a fresh /mnt/nv, load in this build (N) through the loaders soundtapd uses, nothing is
// moved aside, and what this build then saves loads again.
//
//nolint:paralleltest // swaps package variables other tests read: hostnameFile, setHostname, soundbar
func TestNVFixturesLoadWithoutLoss(t *testing.T) {
	sets, _ := filepath.Glob(filepath.Join(nvFixtures, "nv-*"))
	if len(sets) == 0 {
		t.Fatal("no fixture sets in", nvFixtures)
	}

	for _, set := range sets {
		t.Run(filepath.Base(set), func(t *testing.T) {
			nvDir := t.TempDir() // a copy: loaders may rewrite what they read
			must(t, os.CopyFS(nvDir, os.DirFS(set)))

			want := nvSettingsFixture()
			loadNV(t, nvDir, &want)
			noneMovedAside(t, nvDir)
			saveNV(t, nvDir)
			loadNV(t, nvDir, &want)
			noneMovedAside(t, nvDir)
		})
	}
}

// TestNVRollbackLeavesNewerFilesInPlace is the other direction (N -> N-1): after a rollback
// to the other slot the older build reads what the newer one wrote. The older build is not
// in this tree, so this build plays it: the files the current writers produce get a field
// only a later build knows, and the settings file a schema one past SchemaVersion. ADR
// 0120 promises the older build reads every value it knows, or for the settings refuses
// writes, and that opening never moves aside, rewrites or deletes a file.
//
//nolint:paralleltest // swaps package variables other tests read: hostnameFile, setHostname, soundbar
func TestNVRollbackLeavesNewerFilesInPlace(t *testing.T) {
	// The newest set is what this build writes (TestNVFormatsAreTheWritersOwn), plus the
	// files people write by hand.
	nvDir := t.TempDir()
	must(t, os.CopyFS(nvDir, os.DirFS(filepath.Join(nvFixtures, nvCurrent))))

	home := filepath.Join(nvDir, "soundtap")
	for _, name := range []string{"presets.json.settings", install.PendingPath, stockPendingName} {
		addLaterField(t, filepath.Join(home, name))
	}

	for _, name := range []string{"presets.json", "favorites.json", "recent.json", "tunein-streams.json"} {
		addLaterFieldToEach(t, filepath.Join(home, name))
	}

	newer := nvSettingsFixture()
	newer.SchemaVersion = settings.SchemaVersion + 1
	setJSONField(t, filepath.Join(home, "presets.json.settings"), "schemaVersion", newer.SchemaVersion)

	before := snapshotNV(t, nvDir)
	loadNV(t, nvDir, &newer)
	noneMovedAside(t, nvDir)

	if after := snapshotNV(t, nvDir); !reflect.DeepEqual(after, before) {
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

	err := set.SetName("Renamed")
	if err == nil {
		t.Error("the older build saved over a newer settings file")
	}

	b, _ := os.ReadFile(filepath.Join(home, "presets.json.settings"))
	if !bytes.Equal(b, before["soundtap/presets.json.settings"]) {
		t.Errorf("a refused save changed the newer settings file:\n%s", b)
	}
	// The files without a version take the older build's saves. A field only the newer
	// build knew may go (ADR 0120 does not promise it back), every other value stays.
	saveNV(t, nvDir)
	loadNV(t, nvDir, &newer)
	noneMovedAside(t, nvDir)
}

// TestNVEveryListedFileHasAFixture keeps nvFiles, docs/contributors/nv-state.md and the newest fixture
// set in step: every listed name is in nvFiles and the other way round, user state has a
// fixture, every name without a reason has one, and the set holds nothing unlisted.
func TestNVEveryListedFileHasAFixture(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile(nvDoc)
	must(t, err)

	kinds := nvDocFiles(string(doc))
	if len(kinds) < 20 {
		t.Fatalf("read %d names from %s; did its tables change shape?", len(kinds), nvDoc)
	}

	nvFiles := nvFileReasons()

	for name, kind := range kinds {
		reason, ok := nvFiles[name]
		switch {
		case !ok:
			t.Errorf("%s is listed in docs/contributors/nv-state.md but not in nvFiles", name)
		case strings.HasPrefix(kind, "User") && reason != "":
			t.Errorf("%s is user state (%s): it needs a fixture in app/testdata/%s, not a reason",
				name, kind, nvCurrent)
		default: // listed, and user state has its fixture
		}
	}

	set := filepath.Join(nvFixtures, nvCurrent)

	for name, reason := range nvFiles {
		if _, ok := kinds[name]; !ok {
			t.Errorf("%s is in nvFiles but not in docs/contributors/nv-state.md", name)
		}

		_, err := os.Stat(filepath.Join(set, name))
		if reason == "" && err != nil {
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

	for line := range strings.SplitSeq(doc, "\n") {
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
		default: // prose and other tables
		}
	}

	return out
}

func loadNV(t *testing.T, nvDir string, wantSettings *settings.Settings) {
	t.Helper()

	home := filepath.Join(nvDir, "soundtap")
	check := func(what string, got, want any) {
		t.Helper()

		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", what, got, want)
		}
	}
	set := settings.Open(filepath.Join(home, "presets.json.settings"))
	check("settings", set.Get(), *wantSettings)

	if wantSettings.SchemaVersion == settings.SchemaVersion {
		check("settings load error", set.LoadErr(), nil)
	}

	presets, err := store.Open(filepath.Join(home, "presets.json"))
	must(t, err)
	check("presets", presets.All(), nvPresetsFixture())

	fav := favorites.Open(filepath.Join(home, "favorites.json"))
	check("favorites", []any{fav.All(), fav.LoadErr()}, []any{nvFavoritesFixture(), nil})

	rec := recent.Open(filepath.Join(home, "recent.json"))
	check("recent", []any{rec.All(), rec.LoadErr()}, []any{nvRecentFixture(), nil})

	recovery := install.LoadRecovery(home, func() (string, error) { return "ubi.mtd=9,2048", nil })
	check("firmware-pending", []any{recovery.Status, recovery.Pending}, []any{"booted", nvPendingFixture()})
	check(".stock-imported keeps stock's name and presets out", stockTaken(home), true)

	stockPending, err := readPendingStockPresets(filepath.Join(home, stockPendingName))
	check("stock-presets-pending.json", []any{stockPending.Presets, err}, []any{nvStockPendingFixture(), nil})

	streams := &player.StreamCache{}
	streams.Load(filepath.Join(home, "tunein-streams.json"))
	check("tunein-streams.json", streams.All(), nvStreamsFixture())

	_, err = os.Stat(filepath.Join(home, "multiroom-enabled")) // the init script's test is presence
	check("multiroom-enabled", err, nil)

	// The init script names the host from this file before soundtapd starts; soundtapd
	// leaves one that already holds the name alone.
	hostname := filepath.Join(home, "hostname")
	old, err := os.ReadFile(hostname)
	must(t, err)
	pointHostnameAt(t, home)
	must(t, publishHostname(wantSettings.Name))

	now, _ := os.ReadFile(hostname)
	check("hostname", []string{string(old), string(now)}, []string{"kitchen\n", "kitchen\n"})

	// The marker of an image that is installed and running: no BCO flash at boot.
	marker := filepath.Join(home, "bco-image")
	old, _ = os.ReadFile(marker)
	flashed, err := bco.AutoInstall(context.Background(), nvBCOImageFile(t), marker, func() bool { return true },
		func(context.Context, []byte) (string, error) {
			t.Error("bco-image: an installed image was flashed again")

			return "", errNVNoFlash
		}, io.Discard)
	now, _ = os.ReadFile(marker)
	check("bco-image", []any{flashed, err, string(now)}, []any{false, nil, string(old)})

	conf := filepath.Join(nvDir, "wifi.conf")
	b, err := os.ReadFile(conf)
	must(t, err)
	check("wifi.conf has a network", savedNetwork(b), true)

	known, err := savedWifiProfiles(conf)
	must(t, err)

	ssids := make([]string, 0, len(known))
	for _, k := range known {
		ssids = append(ssids, k.SSID)
	}

	check("wifi.conf.known", ssids, []string{"home", "work", "cafe"})

	pointLPMAt(t, home)

	source, ok := soundbar.SavedInput()
	check("bar-source", []any{source, ok}, []any{nvBarSource, true})

	for file, choice := range map[string]struct {
		choice *lpm.Choice
		want   string
	}{
		"bar-audio-mode": {soundbar.AudioModes, nvAudio},
		"bar-cec-mode":   {soundbar.CECModes, nvCEC},
		"bar-hdmi-input": {soundbar.HDMIInputs, nvHDMI},
	} {
		choice.choice.Load()
		check(file, choice.choice.Get(), choice.want)
	}

	ir := hw.LoadIRKeymap(filepath.Join(nvDir, "ir.map"), quietLog())
	check("ir.map", ir, map[byte]string{0x10: "POWER", 0x11: "VOLUME_UP", 0x12: "PRESET_1"})

	keys := hw.LoadKeymap(filepath.Join(nvDir, "buttons.map"))
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

func snapshotNV(t *testing.T, nvDir string) map[string][]byte {
	t.Helper()

	out := map[string][]byte{}

	must(t, filepath.WalkDir(nvDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, _ := filepath.Rel(nvDir, path)

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("snapshot %s: %w", rel, err)
		}

		out[filepath.ToSlash(rel)] = data

		return nil
	}))

	return out
}

// addLaterField adds a field this build does not know to the object in path, as a later
// build might.
func addLaterField(t *testing.T, path string) {
	t.Helper()

	rewriteJSON(t, path, func(decoded any) {
		addLaterFieldTo(t, path, decoded)
	})
}

// addLaterFieldToEach adds a field this build does not know to every object in the array
// or map in path, as a later build might.
func addLaterFieldToEach(t *testing.T, path string) {
	t.Helper()

	rewriteJSON(t, path, func(decoded any) {
		switch values := decoded.(type) {
		case []any:
			for _, o := range values {
				addLaterFieldTo(t, path, o)
			}
		case map[string]any:
			for _, o := range values {
				addLaterFieldTo(t, path, o)
			}
		default: // a scalar holds no objects
		}
	})
}

// addLaterFieldTo adds the later build's field to the object o read from path.
func addLaterFieldTo(t *testing.T, path string, o any) {
	t.Helper()

	m, ok := o.(map[string]any)
	if !ok {
		t.Fatalf("%s: %T is not an object", path, o)
	}

	m["addedLater"] = map[string]any{"by": "a later build"}
}

// rewriteJSON reads the JSON in path, lets change edit it and writes it back indented.
func rewriteJSON(t *testing.T, path string, change func(decoded any)) {
	t.Helper()

	data, err := os.ReadFile(path)
	must(t, err)

	var decoded any
	must(t, json.Unmarshal(data, &decoded))
	change(decoded)

	data, err = json.MarshalIndent(decoded, "", "  ")
	must(t, err)
	must(t, os.WriteFile(path, data, 0o600))
}

func setJSONField(t *testing.T, path, key string, value any) {
	t.Helper()

	data, err := os.ReadFile(path)
	must(t, err)

	var m map[string]any
	must(t, json.Unmarshal(data, &m))
	m[key] = value
	data, err = json.MarshalIndent(m, "", "  ")
	must(t, err)
	must(t, os.WriteFile(path, data, 0o600))
}

// nvBCOImageFile is the image the bco-image marker in the fixtures names.
func nvBCOImageFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bco-app.bcd")
	must(t, os.WriteFile(path, []byte(nvBCOImage), 0o644))

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
