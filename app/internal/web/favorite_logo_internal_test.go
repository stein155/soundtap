// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/tunein"
)

// A station saved from the player comes without a logo, and the UI's CDN fallback has none
// for newer stations: the speaker looks the logo up once, when it is saved.
func TestFavoriteStationWithoutArtGetsItsLogo(t *testing.T) {
	t.Parallel()

	store := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))
	srv := new(Server)
	srv.tunein = tunein.New().WithTransport(&describeStub{calls: atomic.Int32{}})
	srv.favoritesStore = store
	srv.log = slog.New(slog.DiscardHandler)
	body := `{"source":"TUNEIN","name":"Powerhitz","location":"s322692","art":""}`
	rec := httptest.NewRecorder()
	srv.addFavorite(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/favorites", strings.NewReader(body)),
	)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	if got := store.All()[0].Art; !strings.Contains(got, "s322692") {
		t.Fatalf("art = %q, want the station's logo", got)
	}
}
