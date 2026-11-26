// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package icy_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/icy"
)

func TestSplitArtistTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, artist, title string
	}{
		{"Charles & Eddie - Would I Lie To You", "Charles & Eddie", "Would I Lie To You"},
		{"  A  -  B  ", "A", "B"},
		{"NPO Radio 2 - De Wild - PowNed", "NPO Radio 2", "De Wild - PowNed"}, // splits on first " - "
		{"Just A Title", "", "Just A Title"},
		{"My Body Isn't Ready - SOMBR", "SOMBR", "My Body Isn't Ready"}, // Qmusic: "Title - ARTIST"
		{"ABBA - Dancing Queen", "ABBA", "Dancing Queen"},
		{"ABBA - SOS", "ABBA", "SOS"},
		{"Abba - S.O.S.", "Abba", "S.O.S."}, // an initialism is no shouted artist
		{"", "", ""},
	}
	for _, tt := range tests {
		artist, title := icy.SplitArtistTitle(tt.in)
		if artist != tt.artist || title != tt.title {
			t.Errorf("SplitArtistTitle(%q) = (%q, %q), want (%q, %q)", tt.in, artist, title, tt.artist, tt.title)
		}
	}
}

// icyBody builds a valid ICY stream body: metaint audio bytes, a length byte,
// then the metadata block padded to a 16-byte multiple.
func icyBody(metaint int, meta string) []byte {
	audio := strings.Repeat("A", metaint)
	blocks := (len(meta) + 15) / 16
	padded := meta + strings.Repeat("\x00", blocks*16-len(meta))

	return append([]byte(audio), append([]byte{byte(blocks)}, []byte(padded)...)...)
}

func TestStreamTitle(t *testing.T) {
	t.Parallel()

	const metaint = 32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Icy-Metadata") != "1" {
			t.Error("missing Icy-MetaData header")
		}

		w.Header().Set("Icy-Metaint", strconv.Itoa(metaint))
		_, _ = w.Write(icyBody(metaint, "StreamTitle='A - B';"))
	}))
	defer srv.Close()

	got, err := icy.StreamTitle(t.Context(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("StreamTitle: %v", err)
	}

	if got != "A - B" {
		t.Errorf("StreamTitle = %q, want %q", got, "A - B")
	}
}

func TestStreamTitleSkipsEmptyBlock(t *testing.T) {
	t.Parallel()

	const metaint = 16

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Icy-Metaint", strconv.Itoa(metaint))
		// First block empty (length 0), second carries the title.
		body := append([]byte(strings.Repeat("A", metaint)), 0)
		body = append(body, icyBody(metaint, "StreamTitle='Song';")...)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got, err := icy.StreamTitle(t.Context(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("StreamTitle: %v", err)
	}

	if got != "Song" {
		t.Errorf("StreamTitle = %q, want %q", got, "Song")
	}
}

func TestStreamTitleNoMetaint(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("audio"))
	}))
	defer srv.Close()

	got, err := icy.StreamTitle(t.Context(), srv.Client(), srv.URL)
	if err != nil || got != "" {
		t.Errorf("got (%q, %v), want (\"\", nil)", got, err)
	}
}

func TestStreamTitleNon200(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := icy.StreamTitle(t.Context(), srv.Client(), srv.URL)
	if !errors.Is(err, icy.ErrStatus) {
		t.Errorf("err = %v, want ErrStatus on non-200", err)
	}
}
