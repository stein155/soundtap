// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoundInt(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want int
	}{{12.4, 12}, {12.6, 13}, {-1.4, -1}, {-1.6, -2}, {0, 0}} {
		if got := roundInt(c.in); got != c.want {
			t.Errorf("roundInt(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

// getJSON decodes a 200 body and errors on non-200; both are the paths the
// weather fetches rely on.
func TestGetJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.UserAgent(), "SoundTap/") {
			w.WriteHeader(http.StatusTooManyRequests) // what keyless APIs do to Go's default UA
			return
		}
		if r.URL.Path == "/bad" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"current":{"temperature_2m":12.6,"weather_code":3}}`))
	}))
	defer ts.Close()

	var r struct {
		Current struct {
			Temp float64 `json:"temperature_2m"`
			Code int     `json:"weather_code"`
		} `json:"current"`
	}
	if err := getJSON(context.Background(), ts.Client(), ts.URL+"/ok", &r); err != nil {
		t.Fatalf("getJSON ok: %v", err)
	}
	if r.Current.Code != 3 || roundInt(r.Current.Temp) != 13 {
		t.Errorf("decoded %+v, want code 3 / temp 13", r.Current)
	}
	if err := getJSON(context.Background(), ts.Client(), ts.URL+"/bad", &r); err == nil {
		t.Error("getJSON should error on status 500")
	}
}

// No place, no fetch: the speaker never guesses where it is.
func TestFetchRefusesWithoutPlace(t *testing.T) {
	s := New()
	if _, err := s.Fetch(context.Background()); err == nil {
		t.Fatal("fetched without a place")
	}
	s.Configure(52.1, 6.3)
	s.Configure(0, 0)
	if _, err := s.Fetch(context.Background()); err == nil {
		t.Fatal("still had a place after it was cleared")
	}
}

func TestGeocodeKeepsPlacesWithATimezone(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "Utrecht" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"results":[
			{"name":"Utrecht","admin1":"Utrecht","country":"Nederland","latitude":52.09,"longitude":5.12,"timezone":"Europe/Amsterdam"},
			{"name":"Nowhere","latitude":1,"longitude":1}]}`))
	}))
	defer ts.Close()
	oldURL, oldHTTP := geocodeURL, geocodeHTTP
	geocodeURL, geocodeHTTP = ts.URL, ts.Client()
	defer func() { geocodeURL, geocodeHTTP = oldURL, oldHTTP }()
	got, err := Geocode(context.Background(), "Utrecht", "nl")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Timezone != "Europe/Amsterdam" || got[0].Region != "Utrecht" {
		t.Fatalf("got %+v", got)
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Open-Meteo answering without a reading must not become 0°C and clear sky on the clock.
func TestFetchCurrentRefusesNullReading(t *testing.T) {
	for body, ok := range map[string]bool{
		`{"current":{"temperature_2m":null,"weather_code":null}}`: false,
		`{"current":{"temperature_2m":4.6}}`:                      false,
		`{}`:                                                      false,
		`{"current":{"temperature_2m":0.2,"weather_code":0}}`: true,
	} {
		hc := &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteString(body)
			return rec.Result(), nil
		})}
		c, err := fetchCurrent(context.Background(), hc, 52, 5)
		if (err == nil) != ok {
			t.Errorf("%s: err = %v, want ok=%v", body, err, ok)
		}
		if ok && (c == nil || c.TempC != 0 || c.Code != 0) {
			t.Errorf("%s: got %+v", body, c)
		}
	}
}
