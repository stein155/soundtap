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
	stub := &describeStub{}
	st := recent.Open(filepath.Join(t.TempDir(), "recent.json"))
	loc := "/v1/playback/station/s322692"
	if err := st.Record(recent.Entry{Source: sourceTuneIn, Name: "Powerhitz", Location: loc}); err != nil {
		t.Fatal(err)
	}
	s := &Server{tunein: tunein.New().WithTransport(stub), recentStore: st, log: slog.New(slog.DiscardHandler)}

	s.fillRecentArt(st.All())
	s.fillRecentArt(st.All()) // a second read while the first lookup runs asks nothing new
	deadline := time.Now().Add(5 * time.Second)
	for st.All()[0].Art == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := st.All()[0].Art; !strings.Contains(got, "s322692") {
		t.Fatalf("art = %q, want the station's logo", got)
	}
	if n := stub.calls.Load(); n != 1 {
		t.Errorf("Describe asked %d times, want 1", n)
	}
}
