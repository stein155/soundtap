// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/web"
)

func TestSoundbarTone(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	h := srv.Handler()
	if rec := do(t, h, "GET", "/api/soundbar/tone", ""); rec.Code != 404 {
		t.Fatalf("no soundbar: %d", rec.Code)
	}
	var now *speaker.Tone
	var set [4]int
	srv.AttachForTest(func(h *web.Attached) {
		h.Soundbar.Tone, h.Soundbar.SetTone = func() *speaker.Tone { return now }, func(b, tr, c, r int) error {
			if b > 10 {
				return errors.New("out of range")
			}
			set = [4]int{b, tr, c, r}
			return nil
		}
	})
	if rec := do(t, h, "GET", "/api/soundbar/tone", ""); rec.Code != 503 {
		t.Fatalf("before an answer: %d", rec.Code)
	}
	now = &speaker.Tone{Bass: speaker.ToneControl{Value: 2, Min: -10, Max: 10, Step: 1}}
	if rec := do(t, h, "GET", "/api/soundbar/tone", ""); rec.Code != 200 {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "PUT", "/api/soundbar/tone", `{"bass":3,"treble":-1,"centre":0,"rear":4}`); rec.Code != 200 || set != [4]int{3, -1, 0, 4} {
		t.Fatalf("PUT: %d %s %v", rec.Code, rec.Body.String(), set)
	}
	if rec := do(t, h, "PUT", "/api/soundbar/tone", `{"bass":11,"treble":0,"centre":0,"rear":0}`); rec.Code != 400 {
		t.Fatalf("out of range: %d", rec.Code)
	}
	if rec := do(t, h, "PUT", "/api/soundbar/tone", `{"bass":1}`); rec.Code != 400 {
		t.Fatalf("partial: %d", rec.Code)
	}
}

func TestSoundbarADAPTiQ(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	h := srv.Handler()
	if rec := do(t, h, "POST", "/api/soundbar/adaptiq", `{"confirm":true}`); rec.Code != 404 {
		t.Fatalf("no soundbar: %d", rec.Code)
	}
	started := 0
	var fail error
	srv.AttachForTest(func(h *web.Attached) { h.Soundbar.StartADAPTiQ = func() error { started++; return fail } })
	if rec := do(t, h, "POST", "/api/soundbar/adaptiq", `{}`); rec.Code != 400 || started != 0 {
		t.Fatalf("no confirm: %d, started %d", rec.Code, started)
	}
	if rec := do(t, h, "POST", "/api/soundbar/adaptiq", `{"confirm":true}`); rec.Code != 204 || started != 1 {
		t.Fatalf("confirmed: %d, started %d", rec.Code, started)
	}
	fail = errors.New("not answered")
	if rec := do(t, h, "POST", "/api/soundbar/adaptiq", `{"confirm":true}`); rec.Code != 503 {
		t.Fatalf("link down: %d", rec.Code)
	}
}

func TestSoundbarAudioMode(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	h := srv.Handler()
	if rec := do(t, h, "GET", "/api/soundbar/audio-mode", ""); rec.Code != 404 {
		t.Fatalf("no soundbar: %d", rec.Code)
	}
	mode := ""
	srv.AttachForTest(func(h *web.Attached) {
		h.Soundbar.Choices = map[string]web.SoundbarChoice{"audio-mode": {Get: func() string { return mode }, Set: func(m string) error {
			if m != "dialog" && m != "normal" {
				return errors.New("unknown audio mode")
			}
			mode = m
			return nil
		}}}
	})
	// Unknown is offered with nothing selected: the bar reports CEC mode and HDMI input
	// only once one was set.
	if rec := do(t, h, "GET", "/api/soundbar/audio-mode", ""); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `"mode":""`) {
		t.Fatalf("before an answer: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "PUT", "/api/soundbar/audio-mode", `{"mode":"dialog"}`); rec.Code != 200 || mode != "dialog" ||
		!strings.Contains(rec.Body.String(), `"mode":"dialog"`) {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "PUT", "/api/soundbar/audio-mode", `{"mode":"loud"}`); rec.Code != 400 {
		t.Fatalf("unknown mode: %d", rec.Code)
	}
	if rec := do(t, h, "PUT", "/api/soundbar/audio-mode", `{}`); rec.Code != 400 {
		t.Fatalf("no mode: %d", rec.Code)
	}
	// Each choice is its own: the CEC mode is not wired here.
	if rec := do(t, h, "GET", "/api/soundbar/cec-mode", ""); rec.Code != 404 {
		t.Fatalf("unwired choice: %d", rec.Code)
	}
}

func TestSoundbarLipSync(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	h := srv.Handler()
	if rec := do(t, h, "GET", "/api/soundbar/lip-sync", ""); rec.Code != 404 {
		t.Fatalf("no soundbar: %d", rec.Code)
	}
	ms, known := 0, false
	srv.AttachForTest(func(h *web.Attached) {
		h.Soundbar.LipSync, h.Soundbar.SetLipSync, h.Soundbar.LipSyncMax = func() (int, bool) { return ms, known }, func(v int) error {
			if v > 300 {
				return errors.New("too long")
			}
			ms, known = v, true
			return nil
		}, 300
	})
	if rec := do(t, h, "GET", "/api/soundbar/lip-sync", ""); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `"ms":null`) || !strings.Contains(rec.Body.String(), `"max":300`) {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "PUT", "/api/soundbar/lip-sync", `{"ms":20}`); rec.Code != 200 || ms != 20 ||
		!strings.Contains(rec.Body.String(), `"ms":20`) {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "PUT", "/api/soundbar/lip-sync", `{"ms":301}`); rec.Code != 400 {
		t.Fatalf("too long: %d", rec.Code)
	}
	if rec := do(t, h, "PUT", "/api/soundbar/lip-sync", `{}`); rec.Code != 400 {
		t.Fatalf("no ms: %d", rec.Code)
	}
}
