// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/upnp"
	"github.com/stein155/soundtap/app/internal/web"
)

// fakeRegistry implements web.UPnPRegistry for tests without network I/O.
type fakeRegistry struct {
	servers   []upnp.MediaServer
	found     []upnp.MediaServer // what the next Search turns up
	items     []upnp.Item
	browseErr error
}

// errEmptyUSN is the fake registry's answer to a browse that names no server.
var errEmptyUSN = errors.New("empty usn")

// newFakeRegistry is a registry that knows the given servers and nothing else.
func newFakeRegistry(servers ...upnp.MediaServer) *fakeRegistry {
	return &fakeRegistry{servers: servers, found: nil, items: nil, browseErr: nil}
}

// mediaItem is a browse result with no artist, album or art.
func mediaItem(id, title, class, url string) upnp.Item {
	return upnp.Item{ID: id, Title: title, Class: class, Artist: "", Album: "", Art: "", URL: url}
}

func (f *fakeRegistry) Servers() []upnp.MediaServer { return f.servers }
func (f *fakeRegistry) Search(context.Context, func(string, ...any)) {
	f.servers = append(f.servers, f.found...)
}

func (f *fakeRegistry) Browse(_ context.Context, usn, _ string) ([]upnp.Item, error) {
	if f.browseErr != nil {
		return nil, f.browseErr
	}

	if usn == "" {
		return nil, errEmptyUSN
	}

	return f.items, nil
}

func TestUPnPServersNilRegistry(t *testing.T) {
	t.Parallel()

	handler, _ := newServer(t)

	rec := do(t, handler, "GET", "/api/upnp/servers", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404 when registry nil, got %d", rec.Code)
	}
}

// "Search again" must search, not reread the list the 60 s ticker left behind.
func TestUPnPSearchRunsARound(t *testing.T) {
	t.Parallel()

	srv, _, _ := newServerSrv(t)
	reg := newFakeRegistry()
	reg.found = []upnp.MediaServer{{USN: "uuid:nas", FriendlyName: "", Location: ""}}

	srv.AttachForTest(func(h *web.Attached) { h.UPnP.Registry = reg })

	rec := do(t, srv.Handler(), "POST", "/api/upnp/search", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "uuid:nas") {
		t.Fatalf("search: %d %s", rec.Code, rec.Body)
	}
}

func TestUPnPBrowseNilRegistry(t *testing.T) {
	t.Parallel()

	handler, _ := newServer(t)

	rec := do(t, handler, "GET", "/api/upnp/browse?server=x", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404 when registry nil, got %d", rec.Code)
	}
}

func TestUPnPServersWithRegistry(t *testing.T) {
	t.Parallel()

	srv, spk, _ := newServerSrv(t)
	reg := newFakeRegistry(
		upnp.MediaServer{USN: "uuid:test-1", FriendlyName: "TestNAS", Location: "http://192.168.1.50:8200/desc.xml"},
	)

	srv.AttachForTest(func(h *web.Attached) { h.UPnP.Registry = reg })
	h := srv.Handler()
	_ = spk

	rec := do(t, h, "GET", "/api/upnp/servers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got []struct {
		USN          string `json:"usn"`
		FriendlyName string `json:"friendlyName"`
	}
	decodeBody(t, rec, &got)

	if len(got) != 1 {
		t.Fatalf("want 1 server, got %d", len(got))
	}

	if got[0].USN != "uuid:test-1" || got[0].FriendlyName != "TestNAS" {
		t.Errorf("server = %+v", got[0])
	}
}

func TestUPnPBrowseWithRegistry(t *testing.T) {
	t.Parallel()

	srv, spk, _ := newServerSrv(t)
	reg := newFakeRegistry(upnp.MediaServer{USN: "uuid:test-1", FriendlyName: "TestNAS", Location: ""})
	reg.items = []upnp.Item{
		mediaItem("A:MUSIC", "Music", "object.container", ""),
		mediaItem("I:1", "Track 1", "object.item.audioItem", "http://192.168.1.50/1.mp3"),
	}

	srv.AttachForTest(func(h *web.Attached) { h.UPnP.Registry = reg })
	h := srv.Handler()
	_ = spk

	rec := do(t, h, "GET", "/api/upnp/browse?server=uuid%3Atest-1&id=0", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var got []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	decodeBody(t, rec, &got)

	if len(got) != 2 {
		t.Fatalf("want 2 items, got %d: %v", len(got), got)
	}

	if got[0].ID != "A:MUSIC" || got[1].Title != "Track 1" {
		t.Errorf("items = %v", got)
	}
}

func TestUPnPBrowseMissingServer(t *testing.T) {
	t.Parallel()

	srv, spk, _ := newServerSrv(t)
	srv.AttachForTest(func(h *web.Attached) { h.UPnP.Registry = newFakeRegistry() })
	h := srv.Handler()
	_ = spk

	rec := do(t, h, "GET", "/api/upnp/browse", "") // missing ?server=
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 without server param, got %d", rec.Code)
	}
}

func TestAmpFaultHealthWarning(t *testing.T) {
	t.Parallel()

	srv, spk, _ := newServerSrv(t)
	fault := false

	srv.AttachForTest(func(h *web.Attached) { h.Audio.AmpFault = func() bool { return fault } })

	_ = spk

	handler := srv.Handler()

	rec := do(t, handler, "GET", "/api/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	var got struct {
		AmpFault bool     `json:"ampFault"`
		Warnings []string `json:"warnings"`
	}
	decodeBody(t, rec, &got)

	if got.AmpFault {
		t.Error("want no amp fault initially")
	}

	// Now assert fault.
	fault = true
	rec = do(t, handler, "GET", "/api/health", "")
	decodeBody(t, rec, &got)

	if !got.AmpFault {
		t.Error("want ampFault=true after fault asserted")
	}

	found := false

	for _, w := range got.Warnings {
		if w != "" && (w[0] == 'a' || w[0] == 'A') {
			found = true
		}
	}

	if !found {
		t.Errorf("want amp fault warning in warnings, got %v", got.Warnings)
	}
}

// Verify SetAmpFaultStatus is callable on the returned *web.Server.
var _ = (*web.Server)(nil)

// TestMediaURLRejectsLocalTargets guards the one rule every media entry point shares: the
// URL reaches ffmpeg, which opens file:, concat: and pipe: as readily as http.
func TestMediaURLRejectsLocalTargets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, raw := range []string{
		"http://192.168.1.50:8200/MediaItems/1.mp3",
		"https://93.184.215.14/song.flac",
	} {
		if !upnp.MediaURL(ctx, raw) {
			t.Errorf("MediaURL(%q) = false, want true", raw)
		}
	}

	for _, raw := range []string{
		"file:///etc/shadow",
		"file:///mnt/nv/wifi.conf",
		"concat:/etc/passwd|/etc/shadow",
		"pipe:0",
		"/v1/playback/station/s1234", // a TuneIn location is not a media URL
		"http://",                    // no host
		"ftp://host/song.mp3",
		"",
		"data:audio/mp3;base64,AAAA",
		"http://127.0.0.1/api/factory-reset", // the speaker's own API
		"http://localhost:8090/now_playing",
		"http://[fe80::1]:8200/1.mp3",
		"http://169.254.169.254/latest/meta-data",
	} {
		if upnp.MediaURL(ctx, raw) {
			t.Errorf("MediaURL(%q) = true, want false", raw)
		}
	}
}

// TestUPnPPlayRejectsNonMediaURL proves the endpoint refuses before touching the
// speaker, so a crafted request cannot make the player open a local path.
func TestUPnPPlayRejectsNonMediaURL(t *testing.T) {
	t.Parallel()

	srv, spk, _ := newServerSrv(t)
	srv.AttachForTest(func(h *web.Attached) { h.UPnP.Registry = newFakeRegistry() })
	handler := srv.Handler()

	for _, raw := range []string{"file:///etc/shadow", "concat:/etc/passwd", "", "ftp://h/x"} {
		body, err := json.Marshal(map[string]string{"url": raw, "name": "x"})
		if err != nil {
			t.Fatal(err)
		}

		rec := do(t, handler, "POST", "/api/upnp/play", string(body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("POST /api/upnp/play url=%q = %d, want 400", raw, rec.Code)
		}

		if got := spk.StatusSnapshot().Source; got != "STANDBY" {
			t.Fatalf("refused URL %q still changed the source to %q", raw, got)
		}
	}
}

// TestUPnPPlayStartsPlayback is the end-to-end path a browse result now takes: the
// item's media URL becomes the UPNP source, which is what the daemon's player streams.
// Without this endpoint the browse list was a dead end — it could list a NAS but never
// play from it.
// The page shows next and previous for a folder, and only while the folder's track plays.
func TestNowSaysAFolderCanSkip(t *testing.T) {
	t.Parallel()

	srv, _, _ := newServerSrv(t)
	queue := &upnp.Queue{}

	srv.AttachForTest(func(h *web.Attached) { h.UPnP.Queue = queue })
	handler := srv.Handler()

	const media = "http://192.168.1.50:8200/MediaItems/42.mp3"

	canSkip := func() bool {
		t.Helper()
		rec := do(t, handler, "GET", "/api/now", "")

		var now struct {
			CanSkip bool `json:"canSkip"`
		}

		err := json.Unmarshal(rec.Body.Bytes(), &now)
		if err != nil {
			t.Fatal(err)
		}

		return now.CanSkip
	}

	body, err := json.Marshal(map[string]string{"url": media, "name": "Nightcall"})
	if err != nil {
		t.Fatal(err)
	}

	if rec := do(t, handler, "POST", "/api/upnp/play", string(body)); rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body)
	}

	if canSkip() {
		t.Error("a single track says it can skip")
	}

	const audio = "object.item.audioItem.musicTrack"
	queue.Set([]upnp.Item{mediaItem("", "", audio, media), mediaItem("", "", audio, media+"2")}, media)

	if !canSkip() {
		t.Error("a folder's track says it cannot skip")
	}

	if !queue.Holds(media) {
		t.Error("asking ended the queue")
	}

	queue.Clear()

	if canSkip() {
		t.Error("an ended queue says it can skip")
	}
}

func TestUPnPPlayStartsPlayback(t *testing.T) {
	t.Parallel()

	srv, spk, _ := newServerSrv(t)
	srv.AttachForTest(func(h *web.Attached) { h.UPnP.Registry = newFakeRegistry() })
	handler := srv.Handler()

	const media = "http://192.168.1.50:8200/MediaItems/42.mp3"

	body, err := json.Marshal(map[string]string{"url": media, "name": "Nightcall"})
	if err != nil {
		t.Fatal(err)
	}

	rec := do(t, handler, "POST", "/api/upnp/play", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
	}

	snap := spk.StatusSnapshot()
	if snap.Source != "UPNP" {
		t.Errorf("source = %q, want UPNP", snap.Source)
	}

	src, loc, _, _ := spk.PlaybackState()
	if src != "UPNP" || loc != media {
		t.Errorf("playback state = (%q, %q), want (UPNP, %q)", src, loc, media)
	}
}
