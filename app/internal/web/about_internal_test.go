// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/clocksync"
	"github.com/stein155/soundtap/app/internal/update"
	"github.com/stein155/soundtap/app/internal/web/apierror"
)

type stagedApp struct{ dir string }

func (a stagedApp) StagingPath() string   { return filepath.Join(a.dir, "soundtapd.new") }
func (stagedApp) InstallApp(string) error { return nil }

// Before NTP has set the clock the speaker does not ask GitHub, and says why: the UI
// shows "clock not set" rather than a TLS failure or a generic "unreachable".
func TestUpdateCheckSaysTheClockIsNotSet(t *testing.T) {
	t.Setenv("SOUNDTAP_FIRMWARE", "1")

	synced := clocksync.Path

	clocksync.Path = filepath.Join(t.TempDir(), "missing")
	defer func() { clocksync.Path = synced }()

	log := slog.New(slog.DiscardHandler)
	m := update.New("app-v0.1.0", log)
	m.SetAppInstaller(stagedApp{t.TempDir()})

	srv := new(Server)
	srv.update = m
	srv.log = log
	srv.version = "app-v0.1.0"
	// A request that has ended: the check gives up at once instead of waiting its bound.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := httptest.NewRecorder()
	srv.releases(rec, httptest.NewRequestWithContext(ctx, "GET", "/api/releases", nil))

	var rel struct {
		Stable          any    `json:"stable"`
		Unavailable     string `json:"unavailable"`
		UnavailableCode string `json:"unavailableCode"`
	}

	err := json.Unmarshal(rec.Body.Bytes(), &rel)
	if err != nil {
		t.Fatal(err)
	}

	if rec.Code != 200 ||
		rel.Stable != nil ||
		rel.Unavailable == "" ||
		rel.UnavailableCode != string(apierror.ClockNotSet) {
		t.Fatalf("GET /api/releases: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	srv.updateApp(
		rec,
		httptest.NewRequestWithContext(ctx, "POST", "/api/update", strings.NewReader(`{"confirm":true}`)),
	)

	var body apierror.Body

	err = json.Unmarshal(rec.Body.Bytes(), &body)
	if err != nil {
		t.Fatal(err)
	}

	if rec.Code != http.StatusServiceUnavailable || body.Code != apierror.ClockNotSet {
		t.Errorf("POST /api/update: %d %s, want 503 with code clock_not_set", rec.Code, rec.Body)
	}
}
