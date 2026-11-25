// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/web"
)

func TestChannelIsStoredAndApplied(t *testing.T) {
	t.Parallel()

	srv, _, _ := newServerSrv(t)

	var applied string

	srv.AttachForTest(func(h *web.Attached) { h.Audio.Channel = func(c string) { applied = c } })

	handler := srv.Handler()
	if rec := do(t, handler, "GET", "/api/channel", ""); !strings.Contains(rec.Body.String(), `"stereo"`) {
		t.Fatalf("default channel: %s", rec.Body.String())
	}

	if rec := do(t, handler, "PUT", "/api/channel", `{"channel":"left"}`); rec.Code != 200 || applied != "left" {
		t.Fatalf("PUT left: %d %s, applied %q", rec.Code, rec.Body.String(), applied)
	}

	if rec := do(t, handler, "GET", "/api/channel", ""); !strings.Contains(rec.Body.String(), `"left"`) {
		t.Fatalf("stored channel: %s", rec.Body.String())
	}

	if rec := do(t, handler, "PUT", "/api/channel", `{"channel":"centre"}`); rec.Code != 400 || applied != "left" {
		t.Fatalf("PUT centre: %d, applied %q", rec.Code, applied)
	}
}
