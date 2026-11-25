// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/web/firmware"
)

// A Wireless Link accepts notification volumes up to 100 while every other model stops at
// 70. The mixer turns that into a gain by dividing, so the ceiling has to travel with the
// volume: dividing a 100 by a hard-coded 70 gives 1.43 and clips the chime against full
// scale, which is the opposite of what a louder setting is for.
func TestNotificationCarriesTheCeilingItWasClampedTo(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		model   string
		volume  int
		wantVol int
		wantMax int
	}{
		{"wla takes the full range", "wla", 100, 100, 100},
		{"wla mid", "wla", 85, 85, 100},
		{"other models stop at 70", "st10", 100, 70, 70},
		{"floor applies everywhere", "wla", 3, 10, 100},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			srv := new(Server)
			srv.fw = firmware.New(nil, "")
			srv.fw.SetModel(func() (string, error) { return testCase.model, nil })

			if got := srv.notificationVolume(testCase.volume); got != testCase.wantVol {
				t.Fatalf("volume = %d, want %d", got, testCase.wantVol)
			}

			if got := srv.notificationVolumeMax(); got != testCase.wantMax {
				t.Fatalf("ceiling = %d, want %d", got, testCase.wantMax)
			}
		})
	}
}

// A clip upload is read whole into memory, so a second one while the first is still
// arriving is turned away rather than read beside it.
func TestClipUploadsAreReadOneAtATime(t *testing.T) {
	t.Parallel()

	var srv Server
	srv.clipUpload.Lock()
	rec := httptest.NewRecorder()
	clip := strings.NewReader("ID3")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/speaker/notify", clip)
	req.Header.Set("Content-Type", "audio/mpeg")
	srv.speakerNotify(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("upload during an upload = %d, want 503", rec.Code)
	}

	srv.audioMu.Lock()
	defer srv.audioMu.Unlock()

	if len(srv.audioClips) != 0 {
		t.Fatalf("refused upload left %d clips", len(srv.audioClips))
	}
}
