// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/favorites"
	"github.com/stein155/soundtap/app/internal/tunein"
)

// A station saved from the player comes without a logo, and the UI's CDN fallback has none
// for newer stations: the speaker looks the logo up once, when it is saved.
func TestFavoriteStationWithoutArtGetsItsLogo(t *testing.T) {
	st := favorites.Open(filepath.Join(t.TempDir(), "favorites.json"))
	s := &Server{tunein: tunein.New().WithTransport(&describeStub{}), favoritesStore: st, log: slog.New(slog.DiscardHandler)}
	body := `{"source":"TUNEIN","name":"Powerhitz","location":"s322692","art":""}`
	w := httptest.NewRecorder()
	s.addFavorite(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/favorites", strings.NewReader(body)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if got := st.All()[0].Art; !strings.Contains(got, "s322692") {
		t.Fatalf("art = %q, want the station's logo", got)
	}
}
