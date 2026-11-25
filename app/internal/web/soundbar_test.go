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

// The bar's refusals the fakes below answer with.
var (
	errToneOutOfRange    = errors.New("out of range")
	errADAPTiQUnanswered = errors.New("not answered")
	errUnknownAudioMode  = errors.New("unknown audio mode")
	errLipSyncTooLong    = errors.New("too long")
)

// soundbarMax is the lip-sync delay ceiling the fake bar reports, in milliseconds.
const soundbarMax = 300

func TestSoundbarTone(t *testing.T) {
	t.Parallel()

	srv, _, _ := newServerSrv(t)

	handler := srv.Handler()
	if rec := do(t, handler, "GET", "/api/soundbar/tone", ""); rec.Code != 404 {
		t.Fatalf("no soundbar: %d", rec.Code)
	}

	var (
		now *speaker.Tone
		set [4]int
	)

	srv.AttachForTest(func(h *web.Attached) {
		h.Soundbar.Tone = func() *speaker.Tone { return now }
		h.Soundbar.SetTone = func(bass, treble, centre, rear int) error {
			if bass > 10 {
				return errToneOutOfRange
			}

			set = [4]int{bass, treble, centre, rear}

			return nil
		}
	})

	if rec := do(t, handler, "GET", "/api/soundbar/tone", ""); rec.Code != 503 {
		t.Fatalf("before an answer: %d", rec.Code)
	}

	answer := new(speaker.Tone)
	answer.Bass = speaker.ToneControl{Value: 2, Min: -10, Max: 10, Step: 1}
	now = answer

	if rec := do(t, handler, "GET", "/api/soundbar/tone", ""); rec.Code != 200 {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}

	put := do(t, handler, "PUT", "/api/soundbar/tone", `{"bass":3,"treble":-1,"centre":0,"rear":4}`)
	if put.Code != 200 || set != [4]int{3, -1, 0, 4} {
		t.Fatalf("PUT: %d %s %v", put.Code, put.Body.String(), set)
	}

	put = do(t, handler, "PUT", "/api/soundbar/tone", `{"bass":11,"treble":0,"centre":0,"rear":0}`)
	if put.Code != 400 {
		t.Fatalf("out of range: %d", put.Code)
	}

	if rec := do(t, handler, "PUT", "/api/soundbar/tone", `{"bass":1}`); rec.Code != 400 {
		t.Fatalf("partial: %d", rec.Code)
	}
}

func TestSoundbarADAPTiQ(t *testing.T) {
	t.Parallel()

	srv, _, _ := newServerSrv(t)

	handler := srv.Handler()
	if rec := do(t, handler, "POST", "/api/soundbar/adaptiq", `{"confirm":true}`); rec.Code != 404 {
		t.Fatalf("no soundbar: %d", rec.Code)
	}

	started := 0

	var fail error

	srv.AttachForTest(func(h *web.Attached) {
		h.Soundbar.StartADAPTiQ = func() error {
			started++

			return fail
		}
	})

	if rec := do(t, handler, "POST", "/api/soundbar/adaptiq", `{}`); rec.Code != 400 || started != 0 {
		t.Fatalf("no confirm: %d, started %d", rec.Code, started)
	}

	if rec := do(t, handler, "POST", "/api/soundbar/adaptiq", `{"confirm":true}`); rec.Code != 204 || started != 1 {
		t.Fatalf("confirmed: %d, started %d", rec.Code, started)
	}

	fail = errADAPTiQUnanswered

	if rec := do(t, handler, "POST", "/api/soundbar/adaptiq", `{"confirm":true}`); rec.Code != 503 {
		t.Fatalf("link down: %d", rec.Code)
	}
}

func TestSoundbarAudioMode(t *testing.T) {
	t.Parallel()

	srv, _, _ := newServerSrv(t)

	handler := srv.Handler()
	if rec := do(t, handler, "GET", "/api/soundbar/audio-mode", ""); rec.Code != 404 {
		t.Fatalf("no soundbar: %d", rec.Code)
	}

	mode := ""

	srv.AttachForTest(func(attached *web.Attached) {
		setMode := func(m string) error {
			if m != "dialog" && m != "normal" {
				return errUnknownAudioMode
			}

			mode = m

			return nil
		}
		attached.Soundbar.Choices = map[string]web.SoundbarChoice{
			"audio-mode": {Get: func() string { return mode }, Set: setMode},
		}
	})
	// Unknown is offered with nothing selected: the bar reports CEC mode and HDMI input
	// only once one was set.
	if rec := do(t, handler, "GET", "/api/soundbar/audio-mode", ""); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `"mode":""`) {
		t.Fatalf("before an answer: %d %s", rec.Code, rec.Body.String())
	}

	put := do(t, handler, "PUT", "/api/soundbar/audio-mode", `{"mode":"dialog"}`)
	if put.Code != 200 || mode != "dialog" || !strings.Contains(put.Body.String(), `"mode":"dialog"`) {
		t.Fatalf("PUT: %d %s", put.Code, put.Body.String())
	}

	if rec := do(t, handler, "PUT", "/api/soundbar/audio-mode", `{"mode":"loud"}`); rec.Code != 400 {
		t.Fatalf("unknown mode: %d", rec.Code)
	}

	if rec := do(t, handler, "PUT", "/api/soundbar/audio-mode", `{}`); rec.Code != 400 {
		t.Fatalf("no mode: %d", rec.Code)
	}
	// Each choice is its own: the CEC mode is not wired here.
	if rec := do(t, handler, "GET", "/api/soundbar/cec-mode", ""); rec.Code != 404 {
		t.Fatalf("unwired choice: %d", rec.Code)
	}
}

func TestSoundbarLipSync(t *testing.T) {
	t.Parallel()

	srv, _, _ := newServerSrv(t)

	handler := srv.Handler()
	if rec := do(t, handler, "GET", "/api/soundbar/lip-sync", ""); rec.Code != 404 {
		t.Fatalf("no soundbar: %d", rec.Code)
	}

	delayMs, known := 0, false

	srv.AttachForTest(func(attached *web.Attached) {
		attached.Soundbar.LipSync = func() (int, bool) { return delayMs, known }
		attached.Soundbar.SetLipSync = func(v int) error {
			if v > soundbarMax {
				return errLipSyncTooLong
			}

			delayMs, known = v, true

			return nil
		}
		attached.Soundbar.LipSyncMax = soundbarMax
	})

	if rec := do(t, handler, "GET", "/api/soundbar/lip-sync", ""); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `"ms":null`) || !strings.Contains(rec.Body.String(), `"max":300`) {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body.String())
	}

	if rec := do(t, handler, "PUT", "/api/soundbar/lip-sync", `{"ms":20}`); rec.Code != 200 || delayMs != 20 ||
		!strings.Contains(rec.Body.String(), `"ms":20`) {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}

	if rec := do(t, handler, "PUT", "/api/soundbar/lip-sync", `{"ms":301}`); rec.Code != 400 {
		t.Fatalf("too long: %d", rec.Code)
	}

	if rec := do(t, handler, "PUT", "/api/soundbar/lip-sync", `{}`); rec.Code != 400 {
		t.Fatalf("no ms: %d", rec.Code)
	}
}
