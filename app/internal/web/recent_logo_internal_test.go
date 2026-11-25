// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/recent"
	"github.com/stein155/soundtap/app/internal/tunein"
)

type describeStub struct{ calls atomic.Int32 }

func (d *describeStub) RoundTrip(r *http.Request) (*http.Response, error) {
	d.calls.Add(1)

	body := `{"body":[{"name":"Powerhitz","logo":"https://cdn-profiles.tunein.com/s322692/images/logoq.png"}]}`

	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

// A station started from the UI is stored without a logo, and TuneIn's old logo CDN, the
// UI's fallback, has none for newer stations: reading the list looks the logo up once.
func TestRecentStationWithoutArtGetsItsLogo(t *testing.T) {
	t.Parallel()

	stub := new(describeStub)
	store := recent.Open(filepath.Join(t.TempDir(), "recent.json"))

	loc := "/v1/playback/station/s322692"

	err := store.Record(recent.Entry{
		Source: sourceTuneIn, Name: "Powerhitz", Location: loc, Art: "", StoppedAt: time.Time{},
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := new(Server)
	srv.tunein = tunein.New().WithTransport(stub)
	srv.recentStore = store
	srv.log = slog.New(slog.DiscardHandler)

	srv.fillRecentArt(t.Context(), store.All())
	srv.fillRecentArt(t.Context(), store.All()) // a second read while the first lookup runs asks nothing new

	deadline := time.Now().Add(5 * time.Second)
	for store.All()[0].Art == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := store.All()[0].Art; !strings.Contains(got, "s322692") {
		t.Fatalf("art = %q, want the station's logo", got)
	}

	if n := stub.calls.Load(); n != 1 {
		t.Errorf("Describe asked %d times, want 1", n)
	}
}
