// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stein155/soundtap/app/internal/radiobrowser"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/tunein"
)

// uuidNPO2New is the uuid NPO Radio 2 was added again under in Radio Browser.
const uuidNPO2New = "961e3b9a-0601-11e8-ae97-52543be04c81"

// fakeDirectory is a TuneIn and a Radio Browser stand-in on local HTTP servers: Radio
// Browser knows the uuids in byUUID and the names in byName, TuneIn answers a search with
// tuneIn; down makes a server answer 503. It records what each was asked.
type fakeDirectory struct {
	mu sync.Mutex

	byUUID     map[string]string // uuid → stream
	byName     map[string]string // name → JSON station list
	tuneIn     string            // the body of every TuneIn search answer
	uuidDown   bool
	nameDown   bool
	tuneInDown bool

	tuneInAsked []url.Values
	rbAsked     []string
}

// directory is a fake directory that knows nothing and answers.
func directory() *fakeDirectory {
	return &fakeDirectory{
		mu: sync.Mutex{}, byUUID: map[string]string{}, byName: map[string]string{}, tuneIn: "",
		uuidDown: false, nameDown: false, tuneInDown: false, tuneInAsked: nil, rbAsked: nil,
	}
}

// asked is what TuneIn and Radio Browser were asked so far.
func (f *fakeDirectory) asked() ([]url.Values, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]url.Values(nil), f.tuneInAsked...), append([]string(nil), f.rbAsked...)
}

func (f *fakeDirectory) radioBrowser(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.rbAsked = append(f.rbAsked, r.URL.Path)

	if uuid, ok := strings.CutPrefix(r.URL.Path, "/json/stations/byuuid/"); ok {
		if f.uuidDown {
			http.Error(w, "busy", http.StatusServiceUnavailable)

			return
		}

		stream, known := f.byUUID[uuid]
		if !known {
			_, _ = w.Write([]byte("[]"))

			return
		}

		_, _ = fmt.Fprintf(w, `[{"stationuuid":%q,"name":"x","url_resolved":%q}]`, uuid, stream)

		return
	}

	name, _ := strings.CutPrefix(r.URL.Path, "/json/stations/bynameexact/")

	if f.nameDown {
		http.Error(w, "busy", http.StatusServiceUnavailable)

		return
	}

	list, known := f.byName[name]
	if !known {
		list = "[]"
	}

	_, _ = w.Write([]byte(list))
}

func (f *fakeDirectory) tuneInSearch(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.tuneInAsked = append(f.tuneInAsked, r.URL.Query())

	if f.tuneInDown || r.URL.Path != "/Search.ashx" {
		http.Error(w, "busy", http.StatusServiceUnavailable)

		return
	}

	_, _ = w.Write([]byte(`{"head":{"status":"200"},"body":[` + f.tuneIn + `]}`))
}

// toServer sends every request to one local server, as TuneIn's client has no other way
// to point it elsewhere from this package.
type toServer struct{ target *url.URL }

func (s toServer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = s.target.Scheme, s.target.Host

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("to the test server: %w", err)
	}

	return resp, nil
}

// lookups is the lookups of the pending file at path against the fake directory, in country.
func (f *fakeDirectory) lookups(t *testing.T, path string, state *speakerstate.Speaker, country string,
	log *slog.Logger,
) *stockLookups {
	t.Helper()

	rbServer := httptest.NewServer(http.HandlerFunc(f.radioBrowser))
	t.Cleanup(rbServer.Close)

	tuneInServer := httptest.NewServer(http.HandlerFunc(f.tuneInSearch))
	t.Cleanup(tuneInServer.Close)

	target, err := url.Parse(tuneInServer.URL)
	if err != nil {
		t.Fatal(err)
	}

	client := radiobrowser.NewWith(rbServer.Client(), "SoundTap/test", []string{rbServer.URL})
	now := testNow
	lookups := lookupsFor(path, state, client.Station, &now, log)
	lookups.searchTuneIn = tunein.New().WithTransport(toServer{target: target}).Search
	lookups.searchByName = client.ByName
	lookups.country = func() string { return country }

	return lookups
}

// tuneInStation is one station of a TuneIn search answer.
func tuneInStation(id, name string) string {
	return fmt.Sprintf(`{"element":"outline","type":"audio","item":"station","text":%q,"guide_id":%q,`+
		`"image":"http://cdn-radiotime-logos.tunein.com/%sq.png"}`, name, id, id)
}

// rbStation is one NPO Radio 2 of a Radio Browser list.
func rbStation(uuid, stream, country string, checkOK int) string {
	return fmt.Sprintf(`{"stationuuid":%q,"name":"NPO Radio 2","url_resolved":%q,"countrycode":%q,`+
		`"lastcheckok":%d}`, uuid, stream, country, checkOK)
}

// takeOver runs one round over one pending NPO Radio 2 preset in slot 2 and returns the
// speaker's presets, whether it still waits, and the log.
func takeOver(t *testing.T, dir *fakeDirectory, country string) ([]speaker.Preset, bool, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), stockPendingName)
	writePending(t, path, pending(2, uuidUnknown, "NPO Radio 2", testNow))

	var buf bytes.Buffer

	state := speakerstate.New()
	waits := dir.lookups(t, path, state, country, slog.New(slog.NewTextHandler(&buf, nil))).round(t.Context())

	_, err := os.Stat(path)
	if waits != (err == nil) {
		t.Fatalf("waits %v, but the pending file: %v", waits, err)
	}

	presets, err := state.Presets(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	return presets, waits, buf.String()
}

// A uuid Radio Browser still knows is taken over as before: nothing is searched by name.
func TestStockLookupsKnownUUIDSearchesNoName(t *testing.T) {
	t.Parallel()

	dir := directory()
	dir.byUUID[uuidUnknown] = "https://example.com/npo2.mp3"
	presets, waits, out := takeOver(t, dir, "")

	if waits || len(presets) != 1 || presets[0].StationID != "https://example.com/npo2.mp3" {
		t.Fatalf("waits %v, presets %+v", waits, presets)
	}

	tuneIn, rb := dir.asked()
	if len(tuneIn) != 0 || len(rb) != 1 {
		t.Fatalf("asked TuneIn %v and Radio Browser %v; want the uuid alone", tuneIn, rb)
	}

	if !strings.Contains(out, `msg="stock preset taken over from Radio Browser" slot=2 name="NPO Radio 2" `+
		`route=radio-browser-uuid uuid=`+uuidUnknown) {
		t.Errorf("the route is not logged:\n%s", out)
	}
}

// A uuid Radio Browser no longer knows is taken over from TuneIn's one station of the
// preset's name, case and spacing aside, stored as a TuneIn preset is; TuneIn is sent the
// name and nothing else, and Radio Browser is not searched.
func TestStockLookupsUnknownUUIDFromTuneIn(t *testing.T) {
	t.Parallel()

	dir := directory()
	dir.tuneIn = strings.Join([]string{
		tuneInStation("s6707", "NPO 3FM"),
		tuneInStation("s9483", " npo  radio 2 "),
		tuneInStation("s9483", "NPO Radio 2"),
		tuneInStation("s1234", "NPO Radio 2 Soul & Jazz"),
	}, ",")
	presets, waits, out := takeOver(t, dir, "NL")

	if waits || len(presets) != 1 {
		t.Fatalf("waits %v, presets %+v", waits, presets)
	}

	got := presets[0]
	if got.Slot != 2 || got.Source != sourceTuneIn || got.Type != "stationurl" ||
		got.Location != "/v1/playback/station/s9483" || got.StationID != "s9483" || got.Name != "NPO Radio 2" ||
		got.Logo != "http://cdn-radiotime-logos.tunein.com/s9483q.png" {
		t.Fatalf("preset %+v", got)
	}

	tuneIn, rb := dir.asked()
	if len(tuneIn) != 1 || tuneIn[0].Get("query") != "NPO Radio 2" || len(rb) != 1 {
		t.Fatalf("asked TuneIn %v and Radio Browser %v", tuneIn, rb)
	}

	if !strings.Contains(out, `msg="stock preset taken over by its name" slot=2 name="NPO Radio 2" `+
		`route=tunein-name station=s9483 uuid=`+uuidUnknown) {
		t.Errorf("the route is not logged:\n%s", out)
	}
}

// Several TuneIn stations of that name, or none, leave it to Radio Browser by the exact
// name, which prefers the speaker's country, then a station it last found playing.
func TestStockLookupsUnknownUUIDFromRadioBrowserByName(t *testing.T) {
	t.Parallel()

	list := "[" + strings.Join([]string{
		rbStation("55555555-5555-4555-8555-555555555555", "http://example.com/de", "DE", 1),
		rbStation(uuidNPO2New, "http://icecast.omroep.nl/radio2-sb-aac", "NL", 1),
		rbStation("66666666-6666-4666-8666-666666666666", "http://example.com/old", "NL", 0),
	}, ",") + "]"

	for name, tuneIn := range map[string]string{
		"TuneIn has two":  tuneInStation("s9483", "NPO Radio 2") + "," + tuneInStation("s99", "NPO Radio 2"),
		"TuneIn has none": tuneInStation("s1234", "NPO Radio 2 Soul & Jazz"),
	} {
		dir := directory()
		dir.tuneIn, dir.byName["NPO Radio 2"] = tuneIn, list
		presets, waits, out := takeOver(t, dir, "NL")

		if waits || len(presets) != 1 || presets[0].Location != "http://icecast.omroep.nl/radio2-sb-aac" ||
			presets[0].Source != sourceTuneIn || presets[0].Name != "NPO Radio 2" {
			t.Errorf("%s: waits %v, presets %+v", name, waits, presets)
		}

		if !strings.Contains(out, `route=radio-browser-name station=`+uuidNPO2New) {
			t.Errorf("%s: the route is not logged:\n%s", name, out)
		}

		_, rb := dir.asked()
		if len(rb) != 2 || rb[1] != "/json/stations/bynameexact/NPO Radio 2" {
			t.Errorf("%s: asked Radio Browser %v", name, rb)
		}
	}
}

// Nothing by that name anywhere: given up as before, with a line that says what was tried.
func TestStockLookupsUnknownUUIDNothingByName(t *testing.T) {
	t.Parallel()

	dir := directory()
	dir.tuneIn = tuneInStation("s1234", "NPO Radio 2 Soul & Jazz")
	dir.byName["NPO Radio 2"] = "[" + rbStation(uuidNPO2New, "http://example.com/a", "NL", 1) + "," +
		rbStation("55555555-5555-4555-8555-555555555555", "http://example.com/b", "NL", 1) + "]"
	presets, waits, out := takeOver(t, dir, "NL")

	if waits || len(presets) != 0 {
		t.Fatalf("waits %v, presets %+v", waits, presets)
	}

	if !strings.Contains(out, `msg="stock preset not taken over: Radio Browser knows no such station, `+
		`and neither TuneIn nor Radio Browser has one station by its name" slot=2 name="NPO Radio 2" uuid=`+
		uuidUnknown+` tried=radio-browser-uuid,tunein-name,radio-browser-name`) {
		t.Errorf("giving up is not logged with what was tried:\n%s", out)
	}
}

// A uuid lookup no server answers is tried again later, without searching by name; so is a
// name search that did not answer and found nothing.
func TestStockLookupsNetworkErrorsRetry(t *testing.T) {
	t.Parallel()

	down := directory()
	down.uuidDown, down.tuneIn = true, tuneInStation("s9483", "NPO Radio 2")
	presets, waits, _ := takeOver(t, down, "")

	tuneIn, _ := down.asked()
	if !waits || len(presets) != 0 || len(tuneIn) != 0 {
		t.Fatalf("Radio Browser down: waits %v, presets %+v, TuneIn asked %v", waits, presets, tuneIn)
	}

	tuneInDown, namesDown := directory(), directory()
	tuneInDown.tuneInDown = true
	namesDown.nameDown, namesDown.tuneIn = true, tuneInStation("s1", "Other")

	for name, dir := range map[string]*fakeDirectory{"TuneIn down": tuneInDown, "names down": namesDown} {
		kept, waiting, out := takeOver(t, dir, "")
		if !waiting || len(kept) != 0 || !strings.Contains(out, `msg="stock preset lookup failed; tried again later"`) {
			t.Errorf("%s: waits %v, presets %+v:\n%s", name, waiting, kept, out)
		}
	}

	// TuneIn down, but Radio Browser has the one station of that name: taken over.
	dir := directory()
	dir.tuneInDown = true
	dir.byName["NPO Radio 2"] = "[" + rbStation(uuidNPO2New, "http://example.com/npo2", "NL", 1) + "]"

	presets, waits, _ = takeOver(t, dir, "")
	if waits || len(presets) != 1 || presets[0].Location != "http://example.com/npo2" {
		t.Fatalf("TuneIn down, Radio Browser has it: waits %v, presets %+v", waits, presets)
	}
}

// TuneIn's match is its one station of the name; its results name no country, so two of the
// same name are none.
func TestTuneInMatch(t *testing.T) {
	t.Parallel()

	station := func(id, name string) tunein.Station {
		return tunein.Station{ID: id, Name: name, Logo: "", Bitrate: "", NowPlaying: ""}
	}

	for name, test := range map[string]struct {
		stations []tunein.Station
		want     string
	}{
		"exact":           {[]tunein.Station{station("s1", "Radio 1"), station("s2", "Radio 10")}, "s2"},
		"case and spaces": {[]tunein.Station{station("s2", "  RADIO\t10 ")}, "s2"},
		"same twice":      {[]tunein.Station{station("s2", "Radio 10"), station("s2", "radio 10")}, "s2"},
		"two":             {[]tunein.Station{station("s2", "Radio 10"), station("s3", "Radio 10")}, ""},
		"longer only":     {[]tunein.Station{station("s4", "Radio 10 Gold")}, ""},
		"odd id":          {[]tunein.Station{station("s2/../x", "Radio 10")}, ""},
		"none":            {nil, ""},
	} {
		got, ok := tuneInMatch(test.stations, "Radio 10")
		if ok != (test.want != "") || got.ID != test.want {
			t.Errorf("%s: %+v, %v; want %q", name, got, ok, test.want)
		}
	}
}

// Radio Browser's match: of several, the speaker's country, then one last found playing;
// the same stream entered twice is one station, two streams are none.
func TestRadioBrowserMatch(t *testing.T) {
	t.Parallel()

	station := func(uuid, name, stream, country string, ok int) radiobrowser.Station {
		return radiobrowser.Station{
			UUID: uuid, Name: name, URL: "", URLResolved: stream, Favicon: "", CountryCode: country, LastCheckOK: ok,
		}
	}

	de1, nl0 := station("a", "Radio 10", "http://x/a", "DE", 1), station("b", "radio 10", "http://x/b", "NL", 0)
	nlA, nlB := station("a", "Radio 10", "http://x/a", "NL", 1), station("b", "Radio 10", "http://x/b", "NL", 1)
	twin := station("b", "Radio 10", "http://x/a", "NL", 1)

	for name, test := range map[string]struct {
		stations []radiobrowser.Station
		country  string
		want     string
	}{
		"one":              {[]radiobrowser.Station{nl0}, "", "b"},
		"country":          {[]radiobrowser.Station{de1, nl0}, "nl", "b"},
		"no country match": {[]radiobrowser.Station{de1, nl0}, "BE", "a"},
		"checked":          {[]radiobrowser.Station{nl0, nlA}, "NL", "a"},
		"same stream":      {[]radiobrowser.Station{nlA, twin}, "NL", "a"},
		"two streams":      {[]radiobrowser.Station{nlA, nlB}, "NL", ""},
		"unknown country":  {[]radiobrowser.Station{de1, nlB}, "", ""},
		"other name":       {[]radiobrowser.Station{station("a", "Radio 10 Gold", "http://x/a", "NL", 1)}, "NL", ""},
		"bad stream":       {[]radiobrowser.Station{station("a", "Radio 10", "ftp://x/a", "NL", 1)}, "NL", ""},
	} {
		got, ok := radioBrowserMatch(test.stations, "Radio 10", test.country)
		if ok != (test.want != "") || got.UUID != test.want {
			t.Errorf("%s: %+v, %v; want %q", name, got, ok, test.want)
		}
	}
}

// The errors a name search ends in are kept apart from "nothing found".
func TestFindByNameReportsAFailedSearch(t *testing.T) {
	t.Parallel()

	dir := directory()
	dir.tuneInDown, dir.nameDown = true, true
	path := filepath.Join(t.TempDir(), stockPendingName)
	lookups := dir.lookups(t, path, speakerstate.New(), "", slog.New(slog.DiscardHandler))
	preset := pending(2, uuidUnknown, "NPO Radio 2", testNow)

	_, found, err := lookups.findByName(t.Context(), lookups.log, &preset)
	if found || err == nil || !errors.Is(err, radiobrowser.ErrNoServer) {
		t.Fatalf("found %v, err %v", found, err)
	}
}
