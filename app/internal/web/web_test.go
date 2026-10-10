// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/nowplaying"
	"github.com/stein155/soundtap/app/internal/settings"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/store"
	"github.com/stein155/soundtap/app/internal/tunein"
	"github.com/stein155/soundtap/app/internal/update"
	"github.com/stein155/soundtap/app/internal/web"
)

// newServer wires a web.Server to a fresh in-memory SoundTouch simulator and
// returns the server's HTTP handler plus the simulator (for assertions on its
// state). Everything (sim REST server) is torn down via t.Cleanup.
//
// Only handlers backed by the simulator / local state are exercised here; the
// TuneIn client is constructed but never driven (no /api/search etc.), so the
// tests make no outbound network calls.
func newServer(t *testing.T) (http.Handler, *speakerstate.Speaker) {
	t.Helper()
	srv, sp, _ := newServerSrv(t)
	return srv.Handler(), sp
}

// newServerSrv is newServer but hands back the *web.Server itself (for tests
// that need to drive its background loop, Run) plus its home/state dir.
func newServerSrv(t *testing.T) (*web.Server, *speakerstate.Speaker, string) {
	t.Helper()
	return newServerAt(t, t.TempDir())
}

// newServerAt is newServerSrv over a caller-owned state dir, so a test can
// build a second server over the same dir to simulate a restart.
func newServerAt(t *testing.T, dir string) (*web.Server, *speakerstate.Speaker, string) {
	t.Helper()
	sp := speakerstate.New()
	sp.DeviceID, sp.IP = "000000000000", "192.168.1.42" // set by soundtapd before serving
	sc := sp
	st, err := store.Open(filepath.Join(dir, "presets.json"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	for _, p := range st.All() {
		source, itemType, location := "TUNEIN", "stationurl", "/v1/playback/station/"+p.StationID
		if p.Source == "UPNP" {
			source, itemType, location = p.Source, p.Type, p.Location
		}
		if err := sp.StorePreset(context.Background(), p.Slot, source, itemType, location, p.Name, p.Logo); err != nil {
			t.Fatalf("hydrate preset: %v", err)
		}
	}
	sp.OnPresetChange(func(slot int, p *speaker.Preset) error {
		if p == nil {
			return st.Remove(slot)
		}
		return st.Set(store.Preset{Slot: slot, StationID: p.StationID, Source: p.Source, Type: p.Type, Location: p.Location, Name: p.Name, Logo: p.Logo})
	})
	sp.OnPresetSwap(st.Swap)
	set := settings.Open(filepath.Join(dir, "settings.json"))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// homeDir is a temp dir with no "soundtap" binary, so updatable() is false:
	// /api/version reports updatable:false and /api/update returns 409 without
	// ever reaching GitHub.
	srv := web.New(web.Options{TuneIn: tunein.New().WithTransport(noNetwork{}), Backend: sc, Settings: set,
		Update: update.New("test", log), NowPlaying: nowplaying.New(sc, tunein.New()), Home: dir, Log: log,
		Setup: web.SetupOptions{SetupMode: func() error { return nil }}})
	// tests never run iptables.
	return srv, sp, dir
}

// do runs a request against the handler and returns the recorder.
func do(t *testing.T, h http.Handler, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, r)
	// The API guard requires a Host that names the speaker (blocks DNS rebinding);
	// httptest defaults to "example.com", so present a loopback host here.
	req.Host = "127.0.0.1:8000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decodeBody decodes the JSON response body into v.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
}

func TestCaptivePortalRedirect(t *testing.T) {
	h, _ := newServer(t)
	for _, path := range []string{"/hotspot-detect.html", "/generate_204", "/gen_204", "/connecttest.txt", "/ncsi.txt", "/canonical.html"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "connectivity-check.example"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "http://192.168.42.1/?setup=wifi" {
			t.Errorf("GET %s = %d Location %q", path, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestSetupMarkerRedirectsBareRoot(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "setup-ap")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOUNDTAP_SETUP_AP_MARKER", marker)
	h, _ := newServer(t)
	if rec := do(t, h, http.MethodGet, "/", ""); rec.Code != http.StatusFound || rec.Header().Get("Location") != "/?setup=wifi" {
		t.Fatalf("bare setup root = %d Location %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := do(t, h, http.MethodGet, "/?lang=nl", ""); rec.Code != http.StatusOK {
		t.Fatalf("root with query = %d, want 200", rec.Code)
	}
	// Reached over Ethernet or another LAN address: the normal UI, not the setup page.
	for _, local := range []string{"192.0.2.20:80", "[fd00::20]:80"} {
		addr, err := net.ResolveTCPAddr("tcp", local)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, net.Addr(addr)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusFound {
			t.Errorf("root reached on %s redirected to %q, want the UI", local, rec.Header().Get("Location"))
		}
	}
	// On the access point's own address the setup page still comes first.
	addr, _ := net.ResolveTCPAddr("tcp", speakerstate.SetupAPAddress+":80")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = speakerstate.SetupAPAddress
	req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, net.Addr(addr)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Errorf("root on the access point = %d, want the setup redirect", rec.Code)
	}
}

func TestVolume(t *testing.T) {
	h, _ := newServer(t)

	// Fresh sim seeds the spotty default volume 30.
	rec := do(t, h, "GET", "/api/volume", "")
	if rec.Code != 200 {
		t.Fatalf("GET volume: %d", rec.Code)
	}
	var got struct {
		Volume int `json:"volume"`
	}
	decodeBody(t, rec, &got)
	if got.Volume != 30 {
		t.Errorf("initial volume = %d, want 30", got.Volume)
	}

	rec = do(t, h, "POST", "/api/volume", `{"volume":42}`)
	if rec.Code != 200 {
		t.Fatalf("POST volume: %d (%s)", rec.Code, rec.Body)
	}
	decodeBody(t, rec, &got)
	if got.Volume != 42 {
		t.Errorf("POST volume echoed %d, want 42", got.Volume)
	}

	rec = do(t, h, "GET", "/api/volume", "")
	decodeBody(t, rec, &got)
	if got.Volume != 42 {
		t.Errorf("readback volume = %d, want 42", got.Volume)
	}

	// Sim clamps to 0..100.
	do(t, h, "POST", "/api/volume", `{"volume":250}`)
	rec = do(t, h, "GET", "/api/volume", "")
	decodeBody(t, rec, &got)
	if got.Volume != 100 {
		t.Errorf("clamp high volume = %d, want 100", got.Volume)
	}

	// A body without "volume" must not mute the speaker.
	if rec = do(t, h, "POST", "/api/volume", `{}`); rec.Code != 400 {
		t.Errorf("POST {} volume: %d, want 400", rec.Code)
	}
	rec = do(t, h, "GET", "/api/volume", "")
	decodeBody(t, rec, &got)
	if got.Volume != 100 {
		t.Errorf("volume after {} = %d, want 100", got.Volume)
	}
}

func TestPresetsLifecycle(t *testing.T) {
	dir := t.TempDir()
	srv, _, _ := newServerAt(t, dir)
	h := srv.Handler()

	// Fresh speaker: no presets.
	rec := do(t, h, "GET", "/api/presets", "")
	if rec.Code != 200 {
		t.Fatalf("GET presets: %d", rec.Code)
	}
	var list []store.Preset
	decodeBody(t, rec, &list)
	if len(list) != 0 {
		t.Fatalf("fresh presets = %+v, want none", list)
	}

	// Store a native preset in slot 3.
	rec = do(t, h, "PUT", "/api/presets/3", `{"stationId":"s99","name":"Radio 538","logo":"http://logo"}`)
	if rec.Code != 200 {
		t.Fatalf("PUT preset: %d (%s)", rec.Code, rec.Body)
	}

	rec = do(t, h, "GET", "/api/presets", "")
	var sps []speaker.Preset
	decodeBody(t, rec, &sps)
	if len(sps) != 1 || sps[0].Slot != 3 || sps[0].Name != "Radio 538" || sps[0].StationID != "s99" || sps[0].Logo != "http://logo" {
		t.Fatalf("stored preset = %+v", sps)
	}

	// A fresh speaker process hydrates the preset from disk.
	reopened, _, _ := newServerAt(t, dir)
	rec = do(t, reopened.Handler(), "GET", "/api/presets", "")
	decodeBody(t, rec, &sps)
	if len(sps) != 1 || sps[0].Slot != 3 || sps[0].StationID != "s99" || sps[0].Logo != "http://logo" {
		t.Fatalf("reopened preset = %+v", sps)
	}

	// Delete it.
	rec = do(t, h, "DELETE", "/api/presets/3", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE preset: %d", rec.Code)
	}
	rec = do(t, h, "GET", "/api/presets", "")
	decodeBody(t, rec, &sps)
	if len(sps) != 0 {
		t.Errorf("after delete presets = %+v, want none", sps)
	}
	afterDelete, _, _ := newServerAt(t, dir)
	rec = do(t, afterDelete.Handler(), "GET", "/api/presets", "")
	decodeBody(t, rec, &sps)
	if len(sps) != 0 {
		t.Errorf("reopened after delete = %+v, want none", sps)
	}
}

func TestPresetBadSlot(t *testing.T) {
	h, _ := newServer(t)
	for _, slot := range []string{"0", "7", "x"} {
		rec := do(t, h, "PUT", "/api/presets/"+slot, `{"stationId":"s1"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUT slot %q: code %d, want 400", slot, rec.Code)
		}
	}
}

func TestPresetMissingStationID(t *testing.T) {
	h, _ := newServer(t)
	rec := do(t, h, "PUT", "/api/presets/1", `{"name":"no id"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PUT without stationId: code %d, want 400", rec.Code)
	}
}

func TestPlayAndNow(t *testing.T) {
	h, sp := newServer(t)

	// playStation selects a native TUNEIN ContentItem on the speakerstate. Use a station
	// id that does NOT start with "s" so the now handler skips TuneIn enrichment
	// (no outbound network).
	rec := do(t, h, "POST", "/api/play", `{"stationId":"p100","name":"Local FM"}`)
	if rec.Code != 200 {
		t.Fatalf("POST play: %d (%s)", rec.Code, rec.Body)
	}
	var played struct {
		Status  string `json:"status"`
		Station string `json:"station"`
	}
	decodeBody(t, rec, &played)
	if played.Status != "playing" || played.Station != "p100" {
		t.Errorf("play response = %+v", played)
	}

	rec = do(t, h, "GET", "/api/now", "")
	if rec.Code != 200 {
		t.Fatalf("GET now: %d (%s)", rec.Code, rec.Body)
	}
	var np speaker.NowPlaying
	decodeBody(t, rec, &np)
	if np.Source != "TUNEIN" || np.Station != "Local FM" || np.PlayStatus != "BUFFERING_STATE" {
		t.Errorf("now playing = %+v", np)
	}
	if np.StationID != "p100" {
		t.Errorf("stationId = %q, want p100", np.StationID)
	}

	// Stop pauses playback on the speakerstate.
	rec = do(t, h, "POST", "/api/stop", "")
	if rec.Code != 200 {
		t.Fatalf("POST stop: %d", rec.Code)
	}
	rec = do(t, h, "GET", "/api/now", "")
	decodeBody(t, rec, &np)
	if np.Source != "STANDBY" {
		t.Errorf("after stop = source %q status %q, want standby", np.Source, np.PlayStatus)
	}
	_ = sp
}

func TestPlayPreset(t *testing.T) {
	h, _ := newServer(t)
	// playPreset presses PRESET_n; it calls Wake (CLI port, not served here) which
	// is best-effort, then Key over the REST API which the sim accepts.
	if rec := do(t, h, "PUT", "/api/presets/2", `{"stationId":"s99","name":"Radio 538"}`); rec.Code != 200 {
		t.Fatalf("seed preset 2: %d (%s)", rec.Code, rec.Body)
	}
	rec := do(t, h, "POST", "/api/play/2", "")
	if rec.Code != 200 {
		t.Fatalf("POST play/2: %d (%s)", rec.Code, rec.Body)
	}
	var got struct {
		Playing int `json:"playing"`
	}
	decodeBody(t, rec, &got)
	if got.Playing != 2 {
		t.Errorf("playing = %d, want 2", got.Playing)
	}

	do(t, h, "DELETE", "/api/presets/5", "")
	if rec = do(t, h, "POST", "/api/play/5", ""); rec.Code != 404 {
		t.Errorf("POST play/5 on an empty slot: %d (%s), want 404", rec.Code, rec.Body)
	}
}

func TestSetSource(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	var changing string
	var flushes int
	srv.AttachForTest(func(h *web.Attached) { h.Audio.SourceChanging = func(source string) { changing = source } })
	srv.AttachForTest(func(h *web.Attached) { h.Audio.Flush = func() { flushes++ } })
	h := srv.Handler()
	post := func(body string) *httptest.ResponseRecorder {
		return doLoopbackCT(t, h, "POST", "/api/source", "application/json", body)
	}
	rec := post(`{"source":"AIRPLAY"}`)
	if rec.Code != 200 {
		t.Fatalf("POST source AIRPLAY: %d (%s)", rec.Code, rec.Body)
	}
	var np speaker.NowPlaying
	decodeBody(t, do(t, h, "GET", "/api/now", ""), &np)
	if np.Source != "AIRPLAY" || np.PlayStatus != "PLAY_STATE" {
		t.Fatalf("after AIRPLAY source = %+v", np)
	}
	if changing != "AIRPLAY" {
		t.Fatalf("source-changing callback = %q", changing)
	}

	// A flush drops queued audio only. It must not run the source-change handoff,
	// which stops the daemons owning the other sources.
	rec = post(`{"source":"AIRPLAY","event":"flush"}`)
	if rec.Code != 200 || flushes != 1 {
		t.Fatalf("source flush = %d, flushes %d", rec.Code, flushes)
	}
	// A source that does not own the amp (librespot pausing in the background) must not
	// drop the owner's audio.
	rec = post(`{"source":"SPOTIFY","event":"flush"}`)
	if rec.Code != 200 || flushes != 1 {
		t.Fatalf("background flush = %d, flushes %d", rec.Code, flushes)
	}
	if changing != "AIRPLAY" {
		t.Fatalf("flush ran the source-change handoff for %q", changing)
	}
	decodeBody(t, do(t, h, "GET", "/api/now", ""), &np)
	if np.Source != "AIRPLAY" {
		t.Fatalf("flush changed source to %q", np.Source)
	}

	// An unknown source must not be stored: it would take the amp with no daemon
	// behind it and nothing to release it.
	if rec = post(`{"source":"NOT_A_SOURCE"}`); rec.Code != 400 {
		t.Fatalf("unknown source = %d, want 400", rec.Code)
	}

	// A refused claim (a Spotify preset that starts after the user left it) keeps the
	// owner and runs no handoff.
	srv.AttachForTest(func(h *web.Attached) { h.Spotify.Claim = func(source string) bool { return source != "SPOTIFY" } })
	rec = post(`{"source":"SPOTIFY"}`)
	decodeBody(t, do(t, h, "GET", "/api/now", ""), &np)
	if rec.Code != 200 || np.Source != "AIRPLAY" || changing != "AIRPLAY" {
		t.Fatalf("refused claim: code %d, source %q, handoff %q", rec.Code, np.Source, changing)
	}
	srv.AttachForTest(func(h *web.Attached) { h.Spotify.Claim = nil })

	// A stop the filter keeps (librespot stopping a preset it is asked to load again)
	// leaves the owner in place.
	srv.AttachForTest(func(h *web.Attached) { h.Spotify.Stop = func(string) bool { return false } })
	rec = post(`{"source":"AIRPLAY","event":"stop"}`)
	decodeBody(t, do(t, h, "GET", "/api/now", ""), &np)
	if rec.Code != 200 || np.Source != "AIRPLAY" {
		t.Fatalf("kept stop: code %d, source %q", rec.Code, np.Source)
	}
	srv.AttachForTest(func(h *web.Attached) { h.Spotify.Stop = nil })

	rec = post(`{"source":"STANDBY"}`)
	if rec.Code != 200 {
		t.Fatalf("POST source STANDBY: %d (%s)", rec.Code, rec.Body)
	}
	decodeBody(t, do(t, h, "GET", "/api/now", ""), &np)
	if np.Source != "STANDBY" {
		t.Fatalf("after STANDBY source = %+v", np)
	}
}

// An ST20 without a Bluetooth adapter must not offer Bluetooth, though its model lists it.
func TestUserSourcesHideMissingBluetooth(t *testing.T) {
	t.Cleanup(web.SetBluetoothPresentForTest(false))
	srv, _, _ := newServerSrv(t)
	srv.ReconfigureForTest(func(o *web.Options) { o.Speaker.UserSources = []string{"AUX", "BLUETOOTH"} })
	h := srv.Handler()

	var sources []string
	decodeBody(t, do(t, h, "GET", "/api/user-sources", ""), &sources)
	if len(sources) != 1 || sources[0] != "AUX" {
		t.Fatalf("sources = %v", sources)
	}
	if rec := do(t, h, "POST", "/api/user-source", `{"source":"BLUETOOTH"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("Bluetooth without adapter: %d", rec.Code)
	}
}

func TestUserSourcePicker(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	srv.ReconfigureForTest(func(o *web.Options) { o.Speaker.UserSources = []string{"AUX", "BLUETOOTH"} })
	h := srv.Handler()

	rec := do(t, h, "GET", "/api/user-sources", "")
	var sources []string
	decodeBody(t, rec, &sources)
	if len(sources) != 2 || sources[0] != "AUX" || sources[1] != "BLUETOOTH" {
		t.Fatalf("sources = %v", sources)
	}

	rec = do(t, h, "POST", "/api/user-source", `{"source":"BLUETOOTH"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("Bluetooth: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, h, "GET", "/api/now", "")
	var np speaker.NowPlaying
	decodeBody(t, rec, &np)
	if np.Source != "BLUETOOTH" {
		t.Fatalf("source = %q", np.Source)
	}

	for _, source := range []string{"SPOTIFY", "AIRPLAY", "NOPE"} {
		rec = do(t, h, "POST", "/api/user-source", `{"source":"`+source+`"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code %d", source, rec.Code)
		}
	}
}

// Two quick picks must not interleave their key sequences: the second waits for the
// first, so the speaker ends on the source picked last.
func TestUserSourcePicksDoNotOverlap(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	srv.ReconfigureForTest(func(o *web.Options) { o.Speaker.UserSources = []string{"AUX", "BLUETOOTH"} })
	var busy, overlapped atomic.Bool
	srv.AttachForTest(func(h *web.Attached) {
		h.Soundbar.PickInput = func(string) error {
			if busy.Swap(true) {
				overlapped.Store(true)
			}
			time.Sleep(20 * time.Millisecond)
			busy.Store(false)
			return nil
		}
	})
	h := srv.Handler()
	var wg sync.WaitGroup
	for _, source := range []string{"AUX", "BLUETOOTH", "AUX"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			do(t, h, "POST", "/api/user-source", `{"source":"`+source+`"}`)
		}()
	}
	wg.Wait()
	if overlapped.Load() {
		t.Fatal("two source picks ran at once")
	}
}

// The source hook is a local-daemon API. Off-box callers could otherwise claim the
// amp, force standby by spoofing a stop, or kill the receivers through flush.
func TestSetSourceRejectsRemoteCallers(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	h := srv.Handler()
	if rec := do(t, h, "POST", "/api/source", `{"source":"AIRPLAY"}`); rec.Code != 403 {
		t.Fatalf("remote POST /api/source = %d, want 403", rec.Code)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	srv, sp, dir := newServerSrv(t)
	h := srv.Handler()

	rec := do(t, h, "GET", "/api/settings", "")
	if rec.Code != 200 {
		t.Fatalf("GET settings: %d", rec.Code)
	}
	var got map[string]any
	decodeBody(t, rec, &got)
	if got["name"] != "SoundTouch" {
		t.Errorf("name = %v, want SoundTouch", got["name"])
	}
	if got["model"] != "SoundTouch 10" {
		t.Errorf("model = %v, want SoundTouch 10", got["model"])
	}
	if got["language"] != "en" {
		t.Errorf("language = %v, want en", got["language"])
	}
	// bass comes back as an object (target/actual/min/max).
	if _, ok := got["bass"]; !ok {
		t.Errorf("settings missing bass: %+v", got)
	}

	// Update name, bass and language in one call.
	rec = do(t, h, "PUT", "/api/settings", `{"name":"Kantoor","bass":-4,"language":"nl"}`)
	if rec.Code != 200 {
		t.Fatalf("PUT settings: %d (%s)", rec.Code, rec.Body)
	}

	if sp.Name != "Kantoor" {
		t.Errorf("sim name = %q, want Kantoor", sp.Name)
	}
	if got := settings.Open(filepath.Join(dir, "settings.json")).Get().Name; got != "Kantoor" {
		t.Errorf("persisted name = %q, want Kantoor", got)
	}

	rec = do(t, h, "GET", "/api/settings", "")
	decodeBody(t, rec, &got)
	if got["name"] != "Kantoor" {
		t.Errorf("readback name = %v, want Kantoor", got["name"])
	}
	if got["language"] != "nl" {
		t.Errorf("readback language = %v, want nl", got["language"])
	}
	bass, ok := got["bass"].(map[string]any)
	if !ok {
		t.Fatalf("bass not an object: %v", got["bass"])
	}
	if bass["target"].(float64) != -4 || bass["actual"].(float64) != -4 {
		t.Errorf("bass after set = %+v, want target/actual -4", bass)
	}
}

func TestSettingsRejectsUnsafeName(t *testing.T) {
	h, sp := newServer(t)
	rec := do(t, h, "PUT", "/api/settings", `{"name":"bad\nname"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unsafe name: %d (%s)", rec.Code, rec.Body)
	}
	if sp.Name != "SoundTouch" {
		t.Fatalf("name mutated to %q", sp.Name)
	}
}

func TestSetupMode(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	called := false
	srv.ReconfigureForTest(func(o *web.Options) { o.Setup.SetupMode = func() error { called = true; return nil } })
	h := srv.Handler()
	rec := do(t, h, "POST", "/api/setup", `{"confirm":true}`)
	if rec.Code != 200 {
		t.Fatalf("POST setup: %d (%s)", rec.Code, rec.Body)
	}
	if !called {
		t.Fatal("setup callback not called")
	}
}

func TestSetupModeNeedsConfirmation(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	called := false
	srv.ReconfigureForTest(func(o *web.Options) { o.Setup.SetupMode = func() error { called = true; return nil } })

	for _, body := range []string{"{}", `{"confirm":false}`, "not json"} {
		if rec := do(t, srv.Handler(), "POST", "/api/setup", body); rec.Code != http.StatusBadRequest {
			t.Errorf("POST setup body %q = %d, want 400", body, rec.Code)
		}
	}
	if called {
		t.Fatal("unconfirmed setup request called hook")
	}
}

func TestDeviceSettingsRoundTrip(t *testing.T) {
	h, _ := newServer(t)

	rec := do(t, h, "GET", "/api/settings", "")
	if rec.Code != 200 {
		t.Fatalf("GET settings: %d", rec.Code)
	}
	var got map[string]any
	decodeBody(t, rec, &got)

	if _, ok := got["treble"]; ok {
		t.Fatalf("settings exposes unsupported treble control: %+v", got)
	}
	net, ok := got["network"].(map[string]any)
	if !ok {
		t.Fatalf("settings missing network: %+v", got)
	}
	if net["ssid"] != "HomeWiFi" || net["type"] != "wifi" {
		t.Errorf("network = %+v", net)
	}
}

func TestSettingsBadBody(t *testing.T) {
	h, _ := newServer(t)
	rec := do(t, h, "PUT", "/api/settings", `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT bad body: %d, want 400", rec.Code)
	}
	var got struct {
		Error string `json:"error"`
	}
	decodeBody(t, rec, &got)
	if got.Error == "" {
		t.Errorf("expected error message, got %q", rec.Body.String())
	}
}

func TestVersion(t *testing.T) {
	h, _ := newServer(t)
	osFile := filepath.Join(t.TempDir(), "os-version")
	if err := os.WriteFile(osFile, []byte("0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(osFile), "os-commit"), []byte("8f980c2b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer web.SetOSVersionFileForTest(osFile)()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://192.168.1.42/api/version", nil)
	r.Header.Set("Origin", "https://installer.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("installer cannot read the version response")
	}
	if rec.Code != 200 {
		t.Fatalf("GET version: %d", rec.Code)
	}
	var got struct {
		Version   string `json:"version"`
		OS        string `json:"os"`
		App       string `json:"app"`
		Commit    string `json:"commit"`
		Updatable bool   `json:"updatable"`
	}
	decodeBody(t, rec, &got)
	if got.Version != "test" || got.App != "test" || got.OS != "0.1.0" || got.Commit != "8f980c2b" {
		t.Errorf("version = %q app %q os %q commit %q, want test, test, 0.1.0, 8f980c2b",
			got.Version, got.App, got.OS, got.Commit)
	}
	// homeDir (a t.TempDir) has no "soundtap" binary, so updates are unavailable.
	if got.Updatable {
		t.Errorf("updatable = true, want false (no installed binary)")
	}
}

// The bench scripts read the boot from /api/health on a production image, which has no
// shell (tools/hil/, ADR 0119): which boot, which slot, confirmed or not, how the last ended.
func TestHealthReportsTheBoot(t *testing.T) {
	root := t.TempDir()
	for path, body := range map[string]string{
		"proc/sys/kernel/random/boot_id": "6f1c2a4e-1b7d-4a55-9a0e-3c2d1e0f9b8a\n",
		"proc/cmdline":                   "console=ttyS3,115200 root=ubi0:rootfs ubi.mtd=9,2048 ubi.mtd=NAND.Persistent-Storage,2048\n",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	defer web.SetSystemRootForTest(root)()
	srv, _, home := newServerSrv(t)
	if err := os.MkdirAll(filepath.Join(home, web.LastBootDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, web.LastBootDir, "how"), []byte("power loss\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	type boot struct {
		ID        string `json:"id"`
		Slot      string `json:"slot"`
		Confirmed bool   `json:"confirmed"`
		Previous  string `json:"previous"`
	}
	var got struct {
		Boot boot `json:"boot"`
	}
	rec := do(t, srv.Handler(), "GET", "/api/health", "")
	if rec.Code != 200 {
		t.Fatalf("GET health: %d", rec.Code)
	}
	decodeBody(t, rec, &got)
	want := boot{ID: "6f1c2a4e-1b7d-4a55-9a0e-3c2d1e0f9b8a", Slot: "PONG", Previous: "power loss"}
	if got.Boot != want {
		t.Errorf("boot = %+v, want %+v", got.Boot, want)
	}
	// A confirm that fails (no NAND here) leaves the boot unconfirmed.
	if err := srv.ConfirmBoot(); err == nil {
		t.Fatal("ConfirmBoot succeeded without NAND")
	}
	decodeBody(t, do(t, srv.Handler(), "GET", "/api/health", ""), &got)
	if got.Boot.Confirmed {
		t.Error("a failed boot confirm reports the boot confirmed")
	}

	// Off NAND (a dev box, a RAM recovery boot) there is no slot to name.
	if err := os.WriteFile(filepath.Join(root, "proc/cmdline"), []byte("root=/dev/ram0 soundtap.ram=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	decodeBody(t, do(t, srv.Handler(), "GET", "/api/health", ""), &got)
	if got.Boot.Slot != "" {
		t.Errorf("slot = %q off NAND, want empty", got.Boot.Slot)
	}
}

func TestInfo(t *testing.T) {
	h, _ := newServer(t)
	rec := do(t, h, "GET", "/api/info", "")
	if rec.Code != 200 {
		t.Fatalf("GET info: %d (%s)", rec.Code, rec.Body)
	}
	var got struct {
		DeviceID string `json:"deviceId"`
		Name     string `json:"name"`
		Model    string `json:"model"`
		IP       string `json:"ip"`
	}
	decodeBody(t, rec, &got)
	if got.DeviceID != "000000000000" || got.Name != "SoundTouch" || got.Model != "SoundTouch 10" || got.IP != "192.168.1.42" {
		t.Errorf("info = %+v", got)
	}
}

func TestUpdateUnavailable(t *testing.T) {
	h, _ := newServer(t)
	// Not an installed speaker -> 409 Conflict, without ever contacting GitHub.
	rec := do(t, h, "POST", "/api/update", `{"confirm":true}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST update: %d, want 409", rec.Code)
	}
	var got struct {
		Error string `json:"error"`
	}
	decodeBody(t, rec, &got)
	if got.Error == "" {
		t.Errorf("expected error message, got %q", rec.Body.String())
	}
}

func TestUpdateNeedsConfirmation(t *testing.T) {
	h, _ := newServer(t)
	for _, body := range []string{"{}", `{"confirm":false}`, "not json"} {
		if rec := do(t, h, "POST", "/api/update", body); rec.Code != http.StatusBadRequest {
			t.Errorf("POST update body %q = %d, want 400", body, rec.Code)
		}
	}
}

// TestEventsStream drives the SSE endpoint end to end: it runs the server's
// background hub, puts the sim into a known playing state, connects to
// /api/events and verifies a live "state" push carrying the now-playing +
// volume the browser would otherwise have to poll for.
func TestEventsStream(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	h := srv.Handler()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Run(ctx) // the hub's poll loop; without it nothing is pushed

	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	// A known playing state so the push has content to assert on.
	if rec := do(t, h, "POST", "/api/play", `{"stationId":"p100","name":"Local FM"}`); rec.Code != 200 {
		t.Fatalf("play: %d (%s)", rec.Code, rec.Body)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("events status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}

	var snap struct {
		Now *speaker.NowPlaying `json:"now"`
		Vol *int                `json:"volume"`
	}
	if err := readStateEvent(cancel, resp.Body, 5*time.Second, &snap); err != nil {
		t.Fatalf("read state event: %v", err)
	}
	if snap.Now == nil {
		t.Fatal("state push carried no now-playing")
	}
	if snap.Now.Source != "TUNEIN" || snap.Now.Station != "Local FM" {
		t.Errorf("pushed now-playing = %+v", snap.Now)
	}
	if snap.Vol == nil || *snap.Vol != 30 {
		t.Errorf("pushed volume = %v, want 30", snap.Vol)
	}
}

// readStateEvent reads the SSE stream until the first "state" event and decodes
// its data into out. It cancels the request and fails if none arrives in time,
// so a broken stream can't hang the test.
func readStateEvent(cancel context.CancelFunc, body io.Reader, timeout time.Duration, out any) error {
	type result struct {
		data string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		sc := bufio.NewScanner(body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if event == "state" {
					ch <- result{data: strings.TrimPrefix(line, "data: ")}
					return
				}
			case line == "":
				event = ""
			}
		}
		ch <- result{err: sc.Err()}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if r.data == "" {
			return fmt.Errorf("stream ended before a state event")
		}
		return json.Unmarshal([]byte(r.data), out)
	case <-time.After(timeout):
		cancel()
		return fmt.Errorf("timed out waiting for state event")
	}
}

// The name set in SoundTap settings is the name AirPlay and Spotify Connect advertise.
// The rename has to reach the receiver supervisor, which restarts shairport-sync and
// librespot with the new -a / --name argument; without this hook a rename only shows
// up after a reboot.
func TestRenameNotifiesReceivers(t *testing.T) {
	srv, sp, _ := newServerSrv(t)
	var renamed []string
	srv.ReconfigureForTest(func(o *web.Options) { o.Changed.Name = func(name string) { renamed = append(renamed, name) } })
	h := srv.Handler()

	if rec := do(t, h, "PUT", "/api/settings", `{"name":"Keuken"}`); rec.Code != 200 {
		t.Fatalf("rename: %d (%s)", rec.Code, rec.Body)
	}
	if len(renamed) != 1 || renamed[0] != "Keuken" {
		t.Fatalf("receiver rename hook got %v, want [Keuken]", renamed)
	}
	// And the speaker's own identity follows, so /api/info and mDNS agree with it.
	if sp.Name != "Keuken" {
		t.Fatalf("speaker name = %q, want Keuken", sp.Name)
	}
	// A rejected name must not reach the receivers at all.
	if rec := do(t, h, "PUT", "/api/settings", `{"name":"bad\nname"}`); rec.Code != 400 {
		t.Fatalf("invalid rename = %d, want 400", rec.Code)
	}
	if len(renamed) != 1 {
		t.Fatalf("invalid name reached the receivers: %v", renamed)
	}
}

// An unreachable release list is an expected state, not a 500.
func TestReleasesUnreachableIsNotAnError(t *testing.T) {
	h, _ := newServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the lookup fails at once, without touching the network
	req := httptest.NewRequest("GET", "/api/releases", nil).WithContext(ctx)
	req.Host = "127.0.0.1:8000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET releases: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Stable      any    `json:"stable"`
		Unavailable string `json:"unavailable"`
		Core        *struct {
			Latest any `json:"latest"`
		} `json:"core"`
	}
	decodeBody(t, rec, &got)
	if got.Stable != nil || got.Unavailable == "" {
		t.Errorf("releases = %+v, want stable null and a reason", got)
	}
	if got.Core == nil || got.Core.Latest != nil {
		t.Errorf("core = %+v, want present with no latest", got.Core)
	}
}

// Off a speaker there is no Core to replace: the route says so before any download.
func TestCoreUpdateOffASpeaker(t *testing.T) {
	h, _ := newServer(t)
	rec := do(t, h, "POST", "/api/core-update", `{"confirm":true}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("core update off a speaker = %d %s, want 409", rec.Code, rec.Body)
	}
	if rec := do(t, h, "POST", "/api/core-update", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("core update without confirm = %d, want 400", rec.Code)
	}
}

// noNetwork keeps TuneIn lookups in the tests from reaching the real service.
type noNetwork struct{}

func (noNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("no network in tests")
}

// Clearing the pairings drops a streaming phone; the speaker must not stay BLUETOOTH and
// playing with nothing behind it.
func TestBluetoothClearEndsStream(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	srv.ReconfigureForTest(func(o *web.Options) {
		o.Bluetooth.Pair, o.Bluetooth.Clear = nil, func(context.Context) error { return nil }
	})
	h := srv.Handler()
	doLoopbackCT(t, h, "POST", "/api/source", "application/json", `{"source":"BLUETOOTH"}`)
	doLoopbackCT(t, h, "POST", "/api/source", "application/json", `{"source":"BLUETOOTH","event":"play"}`)
	var np speaker.NowPlaying
	decodeBody(t, do(t, h, "GET", "/api/now", ""), &np)
	if np.Source != "BLUETOOTH" || np.PlayStatus != "PLAY_STATE" {
		t.Fatalf("before clear = %+v", np)
	}
	if rec := do(t, h, "POST", "/api/bluetooth/clear", ""); rec.Code != 200 {
		t.Fatalf("clear: %d (%s)", rec.Code, rec.Body)
	}
	decodeBody(t, do(t, h, "GET", "/api/now", ""), &np)
	if np.Source != "BLUETOOTH" || np.PlayStatus == "PLAY_STATE" {
		t.Fatalf("after clear = %+v, want BLUETOOTH and not playing", np)
	}
}

func TestSourceFlushWaitAcknowledgesOnlyAfterReaderAndDoesNotRepeatOnClaim(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	h := srv.Handler()
	post := func(body string) *httptest.ResponseRecorder {
		return doLoopbackCT(t, h, "POST", "/api/source", "application/json", body)
	}
	post(`{"source":"SPOTIFY"}`)
	entered, release := make(chan struct{}), make(chan struct{})
	srv.AttachForTest(func(h *web.Attached) {
		h.Audio.FlushWait = func(ctx context.Context) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- post(`{"source":"SPOTIFY","event":"flush_wait"}`) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("flush not entered")
	}
	select {
	case <-done:
		t.Fatal("acknowledged before reader")
	default:
	}
	close(release)
	if rec := <-done; rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body)
	}
	changes := 0
	srv.AttachForTest(func(h *web.Attached) { h.Audio.SourceChanging = func(string) { changes++ } })
	post(`{"source":"SPOTIFY"}`)
	if changes != 0 {
		t.Fatal("same-source claim flushed the new opening")
	}
	post(`{"source":"AIRPLAY"}`)
	if changes != 1 {
		t.Fatal("actual source change was not flushed")
	}
	// A stale Spotify hook must not touch another source's bus.
	if rec := post(`{"source":"SPOTIFY","event":"flush_wait"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
}

func TestSourceFlushWaitReportsFailure(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	h := srv.Handler()
	doLoopbackCT(t, h, "POST", "/api/source", "application/json", `{"source":"SPOTIFY"}`)
	srv.AttachForTest(func(h *web.Attached) {
		h.Audio.FlushWait = func(context.Context) error { return context.DeadlineExceeded }
	})
	rec := doLoopbackCT(t, h, "POST", "/api/source", "application/json", `{"source":"SPOTIFY","event":"flush_wait"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("flush failure acknowledged: %d", rec.Code)
	}
}
