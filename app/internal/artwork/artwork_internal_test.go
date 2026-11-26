// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package artwork

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withServer starts a local search API and returns its client and root.
func withServer(t *testing.T, handler http.HandlerFunc) (*http.Client, string) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv.Client(), srv.URL
}

func TestSearch(t *testing.T) {
	t.Parallel()

	var gotQuery string

	client, root := withServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"results":[{"artworkUrl100":"https://cdn/x/100x100bb.jpg"}]}`))
	})

	got, err := searchAt(t.Context(), client, root, "Charles & Eddie Would I Lie To You")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if got != "https://cdn/x/600x600bb.jpg" {
		t.Errorf("Search = %q, want upgraded to 600x600", got)
	}

	if gotQuery == "" || !strings.Contains(gotQuery, "term=") {
		t.Errorf("query = %q, want a term parameter", gotQuery)
	}
}

func TestSearchNoResults(t *testing.T) {
	t.Parallel()

	client, root := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	})

	got, err := searchAt(t.Context(), client, root, "nothing matches")
	if err != nil || got != "" {
		t.Errorf("got (%q, %v), want (\"\", nil)", got, err)
	}
}

func TestSearchEmptyTerm(t *testing.T) {
	t.Parallel()

	// No term -> no request, empty result, no error.
	got, err := Search(t.Context(), http.DefaultClient, "   ")
	if err != nil || got != "" {
		t.Errorf("got (%q, %v), want (\"\", nil)", got, err)
	}
}

func TestSearchNon200(t *testing.T) {
	t.Parallel()

	client, root := withServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := searchAt(t.Context(), client, root, "x")
	if !errors.Is(err, ErrStatus) {
		t.Errorf("err = %v, want ErrStatus on non-200", err)
	}
}
