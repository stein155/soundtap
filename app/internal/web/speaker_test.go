// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/web"
)

// doLoopbackCT is like do but presents a loopback RemoteAddr (so it clears the
// loopbackOnly guard) and a caller-chosen Content-Type.
func doLoopbackCT(t *testing.T, h http.Handler, method, target, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "127.0.0.1:8000"
	req.RemoteAddr = "127.0.0.1:54321"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSpeakerNotifyURL(t *testing.T) {
	h, sp := newServer(t)

	rec := doLoopbackCT(t, h, "POST", "/api/speaker/notify", "application/json",
		`{"url":"http://translate.google.com/translate_tts?q=hoi","volume":40,"artist":"Afvalwijzer","track":"Morgen: GFT"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body %q; want 200", rec.Code, rec.Body.String())
	}
	n := sp.LastNotification()
	if n == nil {
		t.Fatal("sim recorded no notification")
	}
	// A URL is passed straight through to the firmware (TTS / remote clips).
	if n.URL != "http://translate.google.com/translate_tts?q=hoi" || n.Volume != 40 {
		t.Errorf("url/volume wrong: %+v", n)
	}
	if n.Artist != "Afvalwijzer" || n.Track != "Morgen: GFT" {
		t.Errorf("metadata wrong: %+v", n)
	}
}

// Clients written for Bose's SoundTouch API send an app key. Nothing uses it: the request
// is accepted and the key goes nowhere.
func TestSpeakerNotifyIgnoresAppKey(t *testing.T) {
	want := speakerstate.Notification{URL: "http://192.0.2.10/chime.mp3", Volume: 25, Artist: "Bel"}
	for _, body := range []string{
		`{"url":"http://192.0.2.10/chime.mp3","volume":25,"artist":"Bel","appKey":"any-key"}`,
		`{"url":"http://192.0.2.10/chime.mp3","volume":25,"artist":"Bel","app_key":"any-key"}`,
	} {
		h, sp := newServer(t)
		rec := doLoopbackCT(t, h, "POST", "/api/speaker/notify", "application/json", body)
		if rec.Code != 200 {
			t.Fatalf("%s: status = %d, body %q; want 200", body, rec.Code, rec.Body.String())
		}
		if n := sp.LastNotification(); n == nil || *n != want {
			t.Errorf("%s: notification = %+v, want %+v", body, n, want)
		}
	}

	srv, sp, _ := newServerSrv(t)
	srv.ReconfigureForTest(func(o *web.Options) { o.Speaker.NotifyBaseURL = "http://127.0.0.1:8000" })
	h := srv.Handler()
	rec := doLoopbackCT(t, h, "POST", "/api/speaker/notify?volume=25&appKey=any-key&app_key=any-key",
		"audio/mpeg", "ID3fake-mp3-bytes")
	if rec.Code != 200 {
		t.Fatalf("upload with appKey: status = %d, body %q; want 200", rec.Code, rec.Body.String())
	}
	if n := sp.LastNotification(); n == nil || n.Volume != 25 {
		t.Errorf("upload with appKey: notification = %+v", n)
	}
}

func TestSpeakerNotifyUploadRoundTrip(t *testing.T) {
	srv, sp, _ := newServerSrv(t)
	srv.ReconfigureForTest(func(o *web.Options) { o.Speaker.NotifyBaseURL = "http://127.0.0.1:8000" }) // as soundtapd sets it
	h := srv.Handler()

	const clip = "ID3fake-mp3-bytes"
	rec := doLoopbackCT(t, h, "POST", "/api/speaker/notify?volume=30&artist=Ring&track=Voordeur",
		"audio/mpeg", clip)
	if rec.Code != 200 {
		t.Fatalf("upload status = %d, body %q; want 200", rec.Code, rec.Body.String())
	}
	n := sp.LastNotification()
	if n == nil {
		t.Fatal("sim recorded no notification")
	}
	// The player is handed a SoundTap-hosted loopback URL, not the raw bytes.
	if !strings.HasPrefix(n.URL, "http://127.0.0.1:8000/api/speaker/audio/") || !strings.HasSuffix(n.URL, ".mp3") {
		t.Errorf("notify URL not a hosted clip: %q", n.URL)
	}
	if n.Volume != 30 || n.Artist != "Ring" || n.Track != "Voordeur" {
		t.Errorf("metadata wrong: %+v", n)
	}

	// The hosted clip must be fetchable (this is what the firmware does).
	path := n.URL[strings.Index(n.URL, "/api/speaker/audio/"):]
	got := doLoopbackCT(t, h, "GET", path, "", "")
	if got.Code != 200 || got.Body.String() != clip {
		t.Fatalf("audio fetch: status %d body %q; want 200 %q", got.Code, got.Body.String(), clip)
	}
	if ct := got.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("audio content-type = %q, want audio/mpeg", ct)
	}
}

// Without the base URL soundtapd gives it there is no address the player could fetch
// an uploaded clip from, so the upload is refused instead of handing over a dead URL.
func TestSpeakerNotifyUploadWithoutBaseURLIsRefused(t *testing.T) {
	h, sp := newServer(t)
	rec := doLoopbackCT(t, h, "POST", "/api/speaker/notify", "audio/mpeg", "ID3fake-mp3-bytes")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("upload without a base URL = %d %q, want 503", rec.Code, rec.Body.String())
	}
	if n := sp.LastNotification(); n != nil {
		t.Fatalf("refused upload still played %+v", n)
	}
}

func TestSpeakerNotifyUploadEmpty(t *testing.T) {
	h, _ := newServer(t)
	rec := doLoopbackCT(t, h, "POST", "/api/speaker/notify", "audio/mpeg", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSpeakerNotifyRejectsEmptyURL(t *testing.T) {
	h, _ := newServer(t)
	rec := doLoopbackCT(t, h, "POST", "/api/speaker/notify", "application/json", `{"volume":40}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSpeakerNotifyAcceptsLAN(t *testing.T) {
	h, _ := newServer(t)
	req := httptest.NewRequest("POST", "/api/speaker/notify", strings.NewReader(`{"url":"http://192.168.1.5/a.mp3"}`))
	req.Host = "127.0.0.1:8000"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestSpeakerAudioMissing(t *testing.T) {
	h, _ := newServer(t)
	rec := doLoopbackCT(t, h, "GET", "/api/speaker/audio/deadbeef.mp3", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// The JSON notify path must enforce a tight body limit independently of the guard's
// 4 MiB cap (which exists for the audio-upload path). A 4 MiB JSON body on a
// 128 MiB speaker allocates the entire URL string in memory.
func TestSpeakerNotifyJSONBodyLimited(t *testing.T) {
	h, _ := newServer(t)
	// Build a body just over notifyJSONMax (4 KiB) with a very long URL field.
	big := `{"url":"http://example.com/` + strings.Repeat("a", 5000) + `"}`
	rec := doLoopbackCT(t, h, "POST", "/api/speaker/notify", "application/json", big)
	// The decoder hits the limit mid-read and returns a bad-body 400.
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized JSON body = %d, want 400", rec.Code)
	}
}

// ffmpeg opens file:, concat: and pipe: as readily as http:, so notify takes only
// http(s) URLs.
func TestSpeakerNotifyRejectsNonHTTPURL(t *testing.T) {
	h, _ := newServer(t)
	for _, u := range []string{"file:/mnt/nv/soundtap/settings.json", "concat:/etc/passwd|/etc/shadow", "pipe:0"} {
		if rec := doLoopbackCT(t, h, "POST", "/api/speaker/notify", "application/json", `{"url":"`+u+`"}`); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", u, rec.Code)
		}
	}
}

// The stored MQTT password only goes to the broker it was entered for: a PUT that
// points the bridge elsewhere without a new password must not carry it along.
func TestMQTTPasswordStaysWithItsBroker(t *testing.T) {
	h, _ := newServer(t)
	hasPassword := func() bool {
		var got struct {
			MQTT struct {
				HasPassword bool `json:"hasPassword"`
			} `json:"mqtt"`
		}
		decodeBody(t, do(t, h, "GET", "/api/settings", ""), &got)
		return got.MQTT.HasPassword
	}
	do(t, h, "PUT", "/api/settings", `{"mqtt":{"host":"broker.lan","port":1883,"username":"u","password":"secret"}}`)
	do(t, h, "PUT", "/api/settings", `{"mqtt":{"baseTopic":"soundtap2"}}`)
	if !hasPassword() {
		t.Fatal("password dropped by an edit that kept the broker")
	}
	do(t, h, "PUT", "/api/settings", `{"mqtt":{"host":"evil.lan"}}`)
	if hasPassword() {
		t.Fatal("password kept for a different broker")
	}
}

// PUT /api/settings refuses MQTT settings the bridge cannot use, checked once merged
// with the stored ones, and keeps what was stored.
func TestPutSettingsRefusesInvalidMQTT(t *testing.T) {
	h, _ := newServer(t)
	if rec := do(t, h, "PUT", "/api/settings", `{"mqtt":{"enabled":true,"host":"broker.lan","baseTopic":"home/kitchen"}}`); rec.Code != http.StatusOK {
		t.Fatalf("valid MQTT settings: %d %s", rec.Code, rec.Body)
	}
	cases := map[string]struct{ body, says string }{
		"port too high":     {`{"mqtt":{"port":65536}}`, "port"},
		"negative port":     {`{"mqtt":{"port":-1}}`, "port"},
		"on without host":   {`{"mqtt":{"host":""}}`, "host"},
		"space in host":     {`{"mqtt":{"host":"broker lan.example"}}`, "host"},
		"control in host":   {`{"mqtt":{"host":"broker\u0007.lan"}}`, "host"},
		"wildcard in base":  {`{"mqtt":{"baseTopic":"home/#"}}`, "base topic"},
		"trailing slash":    {`{"mqtt":{"baseTopic":"home/kitchen/"}}`, "base topic"},
		"plus in prefix":    {`{"mqtt":{"discoveryPrefix":"home+assistant"}}`, "discovery prefix"},
		"leading slash":     {`{"mqtt":{"discoveryPrefix":"/homeassistant"}}`, "discovery prefix"},
		"control in prefix": {`{"mqtt":{"discoveryPrefix":"home\u0000assistant"}}`, "discovery prefix"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(t, h, "PUT", "/api/settings", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body)
			}
			var e struct {
				Error string `json:"error"`
			}
			decodeBody(t, rec, &e)
			if !strings.Contains(e.Error, "invalid MQTT settings") || !strings.Contains(e.Error, c.says) {
				t.Errorf("error = %q, want it to name the MQTT %s", e.Error, c.says)
			}
			var got struct {
				MQTT struct {
					Enabled   bool   `json:"enabled"`
					Host      string `json:"host"`
					Port      int    `json:"port"`
					BaseTopic string `json:"baseTopic"`
				} `json:"mqtt"`
			}
			decodeBody(t, do(t, h, "GET", "/api/settings", ""), &got)
			if m := got.MQTT; !m.Enabled || m.Host != "broker.lan" || m.Port != 0 || m.BaseTopic != "home/kitchen" {
				t.Fatalf("a refused PUT changed the stored MQTT settings: %+v", m)
			}
		})
	}
	// Switching the bridge off needs no host.
	if rec := do(t, h, "PUT", "/api/settings", `{"mqtt":{"enabled":false,"host":""}}`); rec.Code != http.StatusOK {
		t.Fatalf("MQTT off without a host: %d %s", rec.Code, rec.Body)
	}
}
