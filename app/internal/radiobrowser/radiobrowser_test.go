// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package radiobrowser_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stein155/soundtap/app/internal/radiobrowser"
)

const stationUUID = "005636e9-2142-497b-8f51-606d536fb18a"

// server answers every request with status and body and counts what it was asked.
func server(t *testing.T, status int, body string, asked *atomic.Int32, seen *http.Header) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)

		if seen != nil {
			*seen = r.Header.Clone()
		}

		if r.URL.Path != "/json/stations/byuuid/"+stationUUID || r.URL.RawQuery != "" {
			http.Error(w, "unexpected "+r.URL.String(), http.StatusNotFound)

			return
		}

		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

const npo2 = `[{"stationuuid":"` + stationUUID + `","name":"NPO Radio 2",` +
	`"url":"http://example.com/npo2.m3u","url_resolved":"https://example.com/npo2.mp3",` +
	`"favicon":"https://example.com/npo2.png"}]`

// The first server that answers with a station list decides; a server that is down,
// answers with an error or with something that is not JSON is passed over.
func TestStationFallsBackToTheNextServer(t *testing.T) {
	t.Parallel()

	var broken, failing, good, after atomic.Int32

	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()

	var header http.Header

	client := radiobrowser.NewWith(http.DefaultClient, "SoundTap/9.9", []string{
		gone.URL,
		server(t, http.StatusOK, "<html>", &broken, nil),
		server(t, http.StatusServiceUnavailable, "busy", &failing, nil),
		server(t, http.StatusOK, npo2, &good, &header),
		server(t, http.StatusOK, npo2, &after, nil),
	})

	station, err := client.Station(t.Context(), stationUUID)
	if err != nil {
		t.Fatal(err)
	}

	if station.StreamURL() != "https://example.com/npo2.mp3" || station.Name != "NPO Radio 2" {
		t.Fatalf("station = %+v", station)
	}

	if broken.Load() != 1 || failing.Load() != 1 || good.Load() != 1 || after.Load() != 0 {
		t.Fatalf("asked broken %d, failing %d, good %d, after %d", broken.Load(), failing.Load(), good.Load(),
			after.Load())
	}

	if got := header.Get("User-Agent"); got != "SoundTap/9.9" {
		t.Fatalf("User-Agent = %q", got)
	}

	if header.Get("Cookie") != "" || header.Get("Authorization") != "" {
		t.Fatalf("request carries more than the uuid: %v", header)
	}
}

func TestStationErrors(t *testing.T) {
	t.Parallel()

	var asked atomic.Int32

	empty := radiobrowser.NewWith(http.DefaultClient, "SoundTap/test",
		[]string{server(t, http.StatusOK, "[]", &asked, nil), server(t, http.StatusOK, npo2, &asked, nil)})

	_, err := empty.Station(t.Context(), stationUUID)
	if !errors.Is(err, radiobrowser.ErrUnknownStation) || asked.Load() != 1 {
		t.Fatalf("empty answer: %v after %d requests; want ErrUnknownStation after 1", err, asked.Load())
	}

	other := radiobrowser.NewWith(http.DefaultClient, "SoundTap/test", []string{server(t, http.StatusOK,
		`[{"stationuuid":"11111111-2222-3333-4444-555555555555","url_resolved":"https://example.com/x"}]`,
		&asked, nil)})

	_, err = other.Station(t.Context(), stationUUID)
	if !errors.Is(err, radiobrowser.ErrUnknownStation) {
		t.Fatalf("another station's answer: %v, want ErrUnknownStation", err)
	}

	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()

	none := radiobrowser.NewWith(http.DefaultClient, "SoundTap/test",
		[]string{gone.URL, server(t, http.StatusOK, "{not json", &asked, nil)})

	_, err = none.Station(t.Context(), stationUUID)
	if !errors.Is(err, radiobrowser.ErrNoServer) {
		t.Fatalf("no server: %v, want ErrNoServer", err)
	}

	before := asked.Load()

	_, err = none.Station(t.Context(), "../../json/servers")
	if !errors.Is(err, radiobrowser.ErrInvalidUUID) || asked.Load() != before {
		t.Fatalf("a path for a uuid: %v, asked %d; want ErrInvalidUUID and nothing asked", err, asked.Load()-before)
	}
}

func TestStreamURLFallsBackToTheEnteredAddress(t *testing.T) {
	t.Parallel()

	station := radiobrowser.Station{
		UUID: stationUUID, Name: "X", URL: " http://example.com/x ", URLResolved: " ", Favicon: "",
	}
	if got := station.StreamURL(); got != "http://example.com/x" {
		t.Fatalf("StreamURL = %q", got)
	}
}

func TestValidUUID(t *testing.T) {
	t.Parallel()

	for uuid, want := range map[string]bool{
		stationUUID:                             true,
		"005636E9-2142-497B-8F51-606D536FB18A":  true,
		"005636e9-2142-497b-8f51-606d536fb18":   false,
		"005636e9-2142-497b-8f51-606d536fb18a/": false,
		"":                                      false,
		"005636e921424 97b8f51606d536fb18a":     false,
	} {
		if got := radiobrowser.ValidUUID(uuid); got != want {
			t.Errorf("ValidUUID(%q) = %v, want %v", uuid, got, want)
		}
	}
}
