// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/radiobrowser"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/tunein"
)

// Station uuids of the tests: made up, one per case.
const (
	uuidNPO2    = "005636e9-2142-497b-8f51-606d536fb18a"
	uuidHeld    = "11111111-1111-4111-8111-111111111111"
	uuidUnknown = "22222222-2222-4222-8222-222222222222"
	uuidLong    = "33333333-3333-4333-8333-333333333333"
	uuidFTP     = "44444444-4444-4444-8444-444444444444"
)

// testNow is a sane clock for the tests.
//
//nolint:gochecknoglobals // a constant time.Time
var testNow = time.Date(2026, time.November, 27, 9, 3, 44, 0, time.UTC)

func pending(slot int, uuid, name string, firstSeen time.Time) pendingStockPreset {
	return pendingStockPreset{Slot: slot, UUID: uuid, Name: name, Logo: "", FirstSeen: firstSeen}
}

func writePending(t *testing.T, path string, presets ...pendingStockPreset) {
	t.Helper()
	recordPendingStockPresets(path, presets, testNow, slog.New(slog.DiscardHandler))

	got, err := readPendingStockPresets(path)
	if err != nil || len(got.Presets) != len(presets) {
		t.Fatalf("written %+v, %v", got, err)
	}
}

// A takeover writes stock's Radio Browser presets down with the time it saw them; before
// the clock is set that time is left out, and a takeover that runs again keeps it.
func TestRecordPendingStockPresets(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), stockPendingName)
	log := slog.New(slog.DiscardHandler)

	recordPendingStockPresets(path, nil, testNow, log)

	_, err := os.Stat(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no pending preset wrote a file: %v", err)
	}

	recordPendingStockPresets(path, []pendingStockPreset{pending(2, uuidNPO2, "NPO Radio 2", time.Time{})},
		time.Unix(0, 0), log)

	got, err := readPendingStockPresets(path)
	if err != nil || got.Version != stockPendingVersion || len(got.Presets) != 1 || !got.Presets[0].FirstSeen.IsZero() {
		t.Fatalf("before the clock is set: %+v, %v", got, err)
	}

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "firstSeen") {
		t.Fatalf("an unknown time is written: %s", data)
	}

	recordPendingStockPresets(path, []pendingStockPreset{pending(2, uuidNPO2, "NPO Radio 2", time.Time{})},
		testNow, log)
	recordPendingStockPresets(path, []pendingStockPreset{
		pending(2, uuidNPO2, "NPO Radio 2", time.Time{}), pending(3, uuidHeld, "Other", time.Time{}),
	}, testNow.Add(time.Hour), log)

	got, err = readPendingStockPresets(path)

	want := []pendingStockPreset{
		pending(2, uuidNPO2, "NPO Radio 2", testNow), pending(3, uuidHeld, "Other", testNow.Add(time.Hour)),
	}
	if err != nil || !reflect.DeepEqual(got.Presets, want) {
		t.Fatalf("again: %+v, %v; want %+v", got.Presets, err, want)
	}
}

// The first start after an install from stock writes the Radio Browser presets down beside
// the presets file; a speaker that has presets of its own takes nothing from stock.
//
//nolint:paralleltest // swaps package variables other tests read: stockPresetsPath
func TestLoadPresetsWritesRadioBrowserPresetsDown(t *testing.T) {
	old := stockPresetsPath

	t.Cleanup(func() { stockPresetsPath = old })

	dir := t.TempDir()
	stockPresetsPath = filepath.Join(dir, "Presets.xml")
	must(t, os.WriteFile(stockPresetsPath, []byte(`<presets>`+
		`<preset id="1"><ContentItem source="TUNEIN" location="/v1/playback/station/s17523">`+
		`<itemName>NPO Radio 1</itemName></ContentItem></preset>`+
		`<preset id="2"><ContentItem source="RADIO_BROWSER" type="stationurl" location="/stations/byuuid/`+uuidNPO2+
		`" sourceAccount="" isPresetable="true"><itemName>NPO Radio 2</itemName>`+
		`<containerArt>http://example.com/npo2.png</containerArt></ContentItem></preset>`+
		`</presets>`), 0o600))

	log := slog.New(slog.DiscardHandler)

	for _, test := range []struct {
		name    string
		own     bool // the speaker has a presets file of its own
		taken   int
		pending int
	}{
		{name: "first start", own: false, taken: 1, pending: 1},
		{name: "own presets", own: true, taken: 0, pending: 0},
	} {
		home := t.TempDir()
		path := filepath.Join(home, "presets.json")

		if test.own {
			must(t, os.WriteFile(path, []byte("[]"), 0o600))
		}

		leftovers := stockTakeover(home, log)
		stored := loadPresets(context.Background(), speakerstate.New(), path, leftovers.presets, log)

		file, err := readPendingStockPresets(filepath.Join(home, stockPendingName))
		if len(stored.All()) != test.taken || len(file.Presets) != test.pending || err != nil {
			t.Errorf("%s: %d taken, %+v pending (%v); want %d and %d", test.name, len(stored.All()), file.Presets, err,
				test.taken, test.pending)
		}

		if test.pending == 1 && (file.Presets[0].Slot != 2 || file.Presets[0].UUID != uuidNPO2 ||
			file.Presets[0].Logo != "http://example.com/npo2.png") {
			t.Errorf("%s: pending %+v", test.name, file.Presets[0])
		}

		_ = stored.Close()
	}
}

// radioBrowser is a Radio Browser stand-in that knows the test stations.
func radioBrowser(t *testing.T) *radiobrowser.Client {
	t.Helper()

	stations := map[string]string{
		uuidNPO2: "https://example.com/npo2.mp3",
		uuidHeld: "https://example.com/held.mp3",
		uuidLong: "https://example.com/" + strings.Repeat("a", 600),
		uuidFTP:  "ftp://example.com/x.mp3",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uuid := strings.TrimPrefix(r.URL.Path, "/json/stations/byuuid/")

		stream, ok := stations[uuid]
		if !ok {
			_, _ = w.Write([]byte("[]"))

			return
		}

		_, _ = fmt.Fprintf(w, `[{"stationuuid":%q,"name":"x","url":"","url_resolved":%q}]`, uuid, stream)
	}))
	t.Cleanup(srv.Close)

	return radiobrowser.NewWith(srv.Client(), "SoundTap/test", []string{srv.URL})
}

// lookupsFor is the lookups of the pending file at path, storing into state, with now as clock.
func lookupsFor(path string, state *speakerstate.Speaker,
	resolve func(context.Context, string) (radiobrowser.Station, error), now *time.Time, log *slog.Logger,
) *stockLookups {
	return &stockLookups{
		path:    path,
		resolve: resolve,
		// Nothing by name, unless a test sets them (stock_byname_test.go).
		searchTuneIn: func(context.Context, string) ([]tunein.Station, error) { return nil, nil },
		searchByName: func(context.Context, string) ([]radiobrowser.Station, error) { return nil, nil },
		country:      func() string { return "" },
		slotHeld: func(ctx context.Context, slot int) bool {
			presets, _ := state.Presets(ctx)
			for _, preset := range presets {
				if preset.Slot == slot {
					return true
				}
			}

			return false
		},
		store: func(ctx context.Context, slot int, location, name, logo string) (bool, error) {
			return state.StorePresetIfEmpty(ctx, slot, sourceTuneIn, "stationurl", location, name, logo)
		},
		now:        func() time.Time { return *now },
		synced:     func() bool { return true },
		buildEpoch: time.Time{},
		log:        log,
		mu:         sync.Mutex{},
		closed:     false,
		cancel:     nil,
	}
}

// One round stores what Radio Browser resolves into a slot that is still empty, and drops
// a preset whose slot the owner filled, a station Radio Browser does not know, and a stream
// address no preset may hold; nothing waits afterwards, so the file goes.
func TestStockLookupsStoreOrDropEachPreset(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), stockPendingName)
	writePending(t, path,
		pending(1, uuidNPO2, "NPO Radio 2", testNow), pending(2, uuidHeld, "Held", testNow),
		pending(3, uuidUnknown, "Gone", testNow), pending(4, uuidLong, "Long", testNow),
		pending(5, uuidFTP, "FTP", testNow), pending(9, uuidNPO2, "No slot", testNow))

	state := speakerstate.New()
	must(t, state.StorePreset(t.Context(), 2, sourceTuneIn, "stationurl", "/v1/playback/station/s1", "Owner's", ""))

	var buf bytes.Buffer

	now := testNow
	lookups := lookupsFor(path, state, radioBrowser(t).Station, &now, slog.New(slog.NewTextHandler(&buf, nil)))

	if lookups.round(t.Context()) {
		t.Fatal("a preset still waits")
	}

	_, err := os.Stat(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the pending file stays: %v", err)
	}

	presets, _ := state.Presets(t.Context())
	if len(presets) != 2 || presets[0].Slot != 1 || presets[0].StationID != "https://example.com/npo2.mp3" ||
		presets[0].Name != "NPO Radio 2" || presets[1].Name != "Owner's" {
		t.Fatalf("presets = %+v", presets)
	}

	out := buf.String()
	for _, want := range []string{
		`msg="stock preset taken over from Radio Browser" slot=1 name="NPO Radio 2"`,
		`msg="stock preset not taken over: the slot holds a preset already" slot=2`,
		`msg="stock preset not taken over: Radio Browser knows no such station, ` +
			`and neither TuneIn nor Radio Browser has one station by its name" slot=3`,
		`msg="stock preset not taken over: Radio Browser gives no usable stream address" slot=4`,
		`msg="stock preset not taken over: Radio Browser gives no usable stream address" slot=5`,
		`msg="stock preset not taken over" slot=9 name="No slot" why="no preset slot 9"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %s:\n%s", want, out)
		}
	}
}

// A lookup no server answers is tried again; the presets after it wait for the next round
// without asking, the file is not rewritten while nothing changed, and after seven days
// from the time it was first seen the preset is given up with a reason.
func TestStockLookupsRetryThenGiveUp(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), stockPendingName)
	writePending(t, path, pending(1, uuidNPO2, "NPO Radio 2", testNow), pending(2, uuidHeld, "Other", testNow))

	asked := 0
	down := func(context.Context, string) (radiobrowser.Station, error) {
		asked++

		return radiobrowser.Station{}, fmt.Errorf("%w: offline", radiobrowser.ErrNoServer)
	}

	var buf bytes.Buffer

	now := testNow.Add(time.Minute)
	lookups := lookupsFor(path, speakerstate.New(), down, &now, slog.New(slog.NewTextHandler(&buf, nil)))

	before, _ := os.Stat(path)

	if !lookups.round(t.Context()) || asked != 1 {
		t.Fatalf("offline: nothing waits, or %d lookups; want waiting after 1", asked)
	}

	after, _ := os.Stat(path)
	if !os.SameFile(before, after) {
		t.Fatal("the pending file was rewritten although nothing changed")
	}

	now = testNow.Add(stockPendingMaxAge - time.Second)

	if !lookups.round(t.Context()) {
		t.Fatal("given up before seven days")
	}

	now = testNow.Add(stockPendingMaxAge)
	asked = 0

	if !lookups.round(t.Context()) || asked != 1 {
		t.Fatalf("after seven days: the second preset was not kept for its own lookup (%d asked)", asked)
	}

	file, err := readPendingStockPresets(path)
	if err != nil || len(file.Presets) != 1 || file.Presets[0].Slot != 2 {
		t.Fatalf("after giving the first up: %+v, %v", file.Presets, err)
	}

	if lookups.round(t.Context()) {
		t.Fatal("the second preset still waits after seven days")
	}

	_, err = os.Stat(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the pending file stays: %v", err)
	}

	if !strings.Contains(buf.String(), `msg="stock preset given up: its lookup failed for 7 days" slot=1`) {
		t.Errorf("giving up is not logged with its reason:\n%s", buf.String())
	}
}

// A preset written down before the clock was set gets its time at the first round with a
// believable clock, and its week starts then.
func TestStockLookupsDateAPresetOnceTheClockIsSet(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), stockPendingName)
	recordPendingStockPresets(path, []pendingStockPreset{pending(1, uuidNPO2, "NPO Radio 2", time.Time{})},
		time.Unix(0, 0), slog.New(slog.DiscardHandler))

	down := func(context.Context, string) (radiobrowser.Station, error) {
		return radiobrowser.Station{}, radiobrowser.ErrNoServer
	}
	now := time.Unix(3600, 0)
	lookups := lookupsFor(path, speakerstate.New(), down, &now, slog.New(slog.DiscardHandler))

	if !lookups.round(t.Context()) {
		t.Fatal("given up with the clock at 1970")
	}

	file, _ := readPendingStockPresets(path)
	if !file.Presets[0].FirstSeen.IsZero() {
		t.Fatalf("dated by a 1970 clock: %v", file.Presets[0].FirstSeen)
	}

	now = testNow

	if !lookups.round(t.Context()) {
		t.Fatal("given up at the first round with a set clock")
	}

	file, _ = readPendingStockPresets(path)
	if !file.Presets[0].FirstSeen.Equal(testNow) {
		t.Fatalf("first seen %v, want %v", file.Presets[0].FirstSeen, testNow)
	}
}

// On the first boot after an install from stock init sets the clock to the build's time: a
// preset is not dated by it, nor by any clock before NTP set it, so a release older than a
// week does not give the presets up at the first failed lookup once NTP moves the clock on.
func TestStockLookupsWaitForNTPToDateAPreset(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), stockPendingName)
	buildEpoch := testNow.Add(-30 * 24 * time.Hour)
	recordPendingStockPresets(path, []pendingStockPreset{pending(1, uuidNPO2, "NPO Radio 2", time.Time{})},
		trustedNow(buildEpoch.Add(time.Minute), func() bool { return false }), slog.New(slog.DiscardHandler))

	file, _ := readPendingStockPresets(path)
	if !file.Presets[0].FirstSeen.IsZero() {
		t.Fatalf("dated by the build clock at the takeover: %v", file.Presets[0].FirstSeen)
	}

	down := func(context.Context, string) (radiobrowser.Station, error) {
		return radiobrowser.Station{}, radiobrowser.ErrNoServer
	}
	now := buildEpoch.Add(2 * time.Minute)
	synced := false
	lookups := lookupsFor(path, speakerstate.New(), down, &now, slog.New(slog.DiscardHandler))
	lookups.synced = func() bool { return synced }
	lookups.buildEpoch = buildEpoch

	if !lookups.round(t.Context()) {
		t.Fatal("given up before NTP set the clock")
	}

	file, _ = readPendingStockPresets(path)
	if !file.Presets[0].FirstSeen.IsZero() {
		t.Fatalf("dated before NTP set the clock: %v", file.Presets[0].FirstSeen)
	}

	now, synced = testNow, true

	if !lookups.round(t.Context()) {
		t.Fatal("given up at the first round after NTP set the clock")
	}

	file, _ = readPendingStockPresets(path)
	if !file.Presets[0].FirstSeen.Equal(testNow) {
		t.Fatalf("first seen %v, want %v", file.Presets[0].FirstSeen, testNow)
	}
}

// A preset an older build dated by init's build clock is dated again once NTP set the clock,
// instead of being given up at once.
func TestStockLookupsRedateAPresetDatedByTheBuildClock(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), stockPendingName)
	buildEpoch := testNow.Add(-30 * 24 * time.Hour)
	writePending(t, path, pending(1, uuidNPO2, "NPO Radio 2", buildEpoch.Add(time.Minute)))

	down := func(context.Context, string) (radiobrowser.Station, error) {
		return radiobrowser.Station{}, radiobrowser.ErrNoServer
	}
	now := testNow
	lookups := lookupsFor(path, speakerstate.New(), down, &now, slog.New(slog.DiscardHandler))
	lookups.buildEpoch = buildEpoch

	if !lookups.round(t.Context()) {
		t.Fatal("a preset dated by the build clock was given up")
	}

	file, _ := readPendingStockPresets(path)
	if !file.Presets[0].FirstSeen.Equal(testNow) {
		t.Fatalf("first seen %v, want %v", file.Presets[0].FirstSeen, testNow)
	}
}

// A factory reset closes the lookups before it removes the pending file: a round still
// waiting for Radio Browser then never writes the file back, so stock's presets do not come
// back at the next start.
func TestStockLookupsClosedByAResetWriteNothing(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), stockPendingName)
	// the first is dropped, so the round has the second to write back
	writePending(t, path,
		pending(1, "not-a-uuid", "Gone", time.Time{}), pending(2, uuidNPO2, "NPO Radio 2", time.Time{}))

	asked, answer := make(chan struct{}), make(chan struct{})
	slow := func(context.Context, string) (radiobrowser.Station, error) {
		close(asked)
		<-answer

		return radiobrowser.Station{}, radiobrowser.ErrNoServer
	}
	now := testNow
	lookups := lookupsFor(path, speakerstate.New(), slow, &now, slog.New(slog.DiscardHandler))

	done := make(chan bool)

	go func() { done <- lookups.round(context.Background()) }()

	<-asked // the round is in flight, with a shorter file to write back

	err := lookups.Close()
	if err != nil {
		t.Fatal(err)
	}

	_ = os.Remove(path) // the reset

	close(answer)
	<-done

	_, err = os.Stat(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the round wrote the pending file back after the reset: %v", err)
	}
}

// Lookups that never started, because nothing waited, close too.
func TestStockLookupsCloseWhenNothingWaits(t *testing.T) {
	t.Parallel()

	closer := startStockPresetLookups(t.Context(), t.TempDir(), speakerstate.New(), slog.New(slog.DiscardHandler))

	err := closer.Close()
	if err != nil {
		t.Fatal(err)
	}
}

// The build's time reads from its file; a missing or garbled one reads as none.
func TestReadBuildEpoch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	bad := filepath.Join(dir, "bad")
	_ = os.WriteFile(good, []byte("1764000000\n"), 0o600)
	_ = os.WriteFile(bad, []byte("soon\n"), 0o600)

	if got := readBuildEpoch(good); !got.Equal(time.Unix(1764000000, 0)) {
		t.Errorf("good = %v", got)
	}

	if got := readBuildEpoch(bad); !got.IsZero() {
		t.Errorf("bad = %v", got)
	}

	if got := readBuildEpoch(filepath.Join(dir, "none")); !got.IsZero() {
		t.Errorf("missing = %v", got)
	}
}

// A pending file that does not read is logged and removed, never a crash; one of a newer
// format stays for the build that wrote it.
func TestStockLookupsMalformedFile(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, content string
		stays         bool
	}{
		{name: "not JSON", content: "{", stays: false},
		{name: "no version", content: `{"presets":[{"slot":1}]}`, stays: false},
		{name: "a list", content: `[1,2]`, stays: false},
		{name: "empty", content: `{"version":1,"presets":[]}`, stays: false},
		{name: "newer", content: `{"version":2,"presets":[{"slot":1}]}`, stays: true},
	} {
		path := filepath.Join(t.TempDir(), stockPendingName)
		must(t, os.WriteFile(path, []byte(test.content), 0o600))

		asked := false
		resolve := func(context.Context, string) (radiobrowser.Station, error) {
			asked = true

			return radiobrowser.Station{}, radiobrowser.ErrNoServer
		}
		now := testNow
		lookups := lookupsFor(path, speakerstate.New(), resolve, &now, slog.New(slog.DiscardHandler))

		if lookups.round(t.Context()) || asked {
			t.Errorf("%s: something waits or was looked up", test.name)
		}

		_, err := os.Stat(path)
		if stays := err == nil; stays != test.stays {
			t.Errorf("%s: file stays %v, want %v", test.name, stays, test.stays)
		}
	}
}

// run tries at once, then waits 30 s, doubling up to ten minutes, and ends once nothing
// waits.
func TestStockLookupsRunPaces(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), stockPendingName)
	writePending(t, path, pending(1, uuidNPO2, "NPO Radio 2", testNow))

	const answerAt = 8 // the round that finds a server

	rounds := 0
	resolve := func(context.Context, string) (radiobrowser.Station, error) {
		rounds++
		if rounds < answerAt {
			return radiobrowser.Station{}, radiobrowser.ErrNoServer
		}

		return radiobrowser.Station{
			UUID: uuidNPO2, Name: "", URL: "", URLResolved: "https://example.com/npo2.mp3", Favicon: "",
			CountryCode: "", LastCheckOK: 0,
		}, nil
	}

	state := speakerstate.New()
	now := testNow
	lookups := lookupsFor(path, state, resolve, &now, slog.New(slog.DiscardHandler))

	var pauses []time.Duration

	lookups.run(t.Context(), func(_ context.Context, d time.Duration) bool {
		pauses = append(pauses, d)

		return true
	})

	want := []time.Duration{
		30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute,
		10 * time.Minute, 10 * time.Minute,
	}
	if !reflect.DeepEqual(pauses, want) || rounds != answerAt {
		t.Fatalf("pauses %v after %d rounds, want %v", pauses, rounds, want)
	}

	if presets, _ := state.Presets(t.Context()); len(presets) != 1 {
		t.Fatalf("presets = %+v", presets)
	}

	// Shutdown ends the waiting.
	writePending(t, path, pending(2, uuidNPO2, "NPO Radio 2", testNow))

	rounds = 0

	lookups.run(t.Context(), func(context.Context, time.Duration) bool { return false })

	if rounds != 1 {
		t.Fatalf("%d rounds after a cancelled wait, want 1", rounds)
	}
}
