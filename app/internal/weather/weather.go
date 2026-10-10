// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package weather fetches the current temperature and sky condition for the town
// the owner picked for the ST20 standby clock, and looks towns up by name. Both
// use Open-Meteo (keyless). Everything is best-effort: any failure returns an
// error and the caller just shows the clock without a weather line.
package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/safehttp"
)

// Current is the live weather for the standby screen. The condition is the raw
// WMO weather code; the display layer turns it into a localized word (English by
// default), so this package stays language-neutral.
type Current struct {
	TempC int // temperature, rounded to whole degrees
	Code  int // raw WMO weather code
}

// Service fetches the current weather for the configured place. Safe for
// concurrent use.
type Service struct {
	http *http.Client

	mu       sync.Mutex
	lat, lon float64
	located  bool
}

// New builds a Service; it fetches nothing until Configure gives it a place.
func New() *Service {
	return &Service{http: &http.Client{Timeout: 8 * time.Second, Transport: safehttp.SafeTransport()}}
}

// Configure sets the place at runtime, so a settings change takes effect without a
// restart. 0,0 means no place.
func (s *Service) Configure(lat, lon float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lat, s.lon, s.located = lat, lon, lat != 0 || lon != 0
}

// Fetch returns the current weather for the configured place.
func (s *Service) Fetch(ctx context.Context) (*Current, error) {
	s.mu.Lock()
	located, lat, lon := s.located, s.lat, s.lon
	s.mu.Unlock()
	if !located {
		return nil, fmt.Errorf("no place set")
	}
	return fetchCurrent(ctx, s.http, lat, lon)
}

// Place is one geocoding match: what the user picks as the standby screen's town.
type Place struct {
	Name     string  `json:"name"`
	Region   string  `json:"region"`
	Country  string  `json:"country"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Timezone string  `json:"timezone"`
}

// Geocode looks a town up by name with Open-Meteo's keyless geocoder, which also
// returns the IANA timezone. Only the typed name leaves the speaker.
func Geocode(ctx context.Context, name, lang string) ([]Place, error) {
	q := url.Values{"name": {name}, "count": {"6"}, "format": {"json"}}
	if lang != "" {
		q.Set("language", lang)
	}
	var r struct {
		Results []struct {
			Name      string  `json:"name"`
			Admin1    string  `json:"admin1"`
			Country   string  `json:"country"`
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
			Timezone  string  `json:"timezone"`
		} `json:"results"`
	}
	if err := getJSON(ctx, geocodeHTTP, geocodeURL+"?"+q.Encode(), &r); err != nil {
		return nil, err
	}
	out := make([]Place, 0, len(r.Results))
	for _, p := range r.Results {
		if p.Timezone == "" {
			continue // a place without a zone cannot drive the clock
		}
		out = append(out, Place{p.Name, p.Admin1, p.Country, p.Latitude, p.Longitude, p.Timezone})
	}
	return out, nil
}

// Variables so the test can point them at a local server.
var (
	geocodeURL  = "https://geocoding-api.open-meteo.com/v1/search"
	geocodeHTTP = &http.Client{Timeout: 8 * time.Second, Transport: safehttp.SafeTransport()}
)

// fetchCurrent reads Open-Meteo's current temperature + weather code.
func fetchCurrent(ctx context.Context, hc *http.Client, lat, lon float64) (*Current, error) {
	url := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,weather_code", lat, lon)
	var r struct {
		Current struct {
			Temp *float64 `json:"temperature_2m"`
			Code *int     `json:"weather_code"`
		} `json:"current"`
	}
	if err := getJSON(ctx, hc, url, &r); err != nil {
		return nil, err
	}
	// A missing or null reading decodes to zero, which would show as 0°C and clear sky.
	if r.Current.Temp == nil || r.Current.Code == nil {
		return nil, errors.New("weather: no current reading")
	}
	return &Current{
		TempC: roundInt(*r.Current.Temp),
		Code:  *r.Current.Code,
	}, nil
}

func getJSON(ctx context.Context, hc *http.Client, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// Keyless APIs rate-limit Go's default User-Agent harder; say who we are.
	req.Header.Set("User-Agent", "SoundTap/1.0")
	resp, err := safehttp.Do(ctx, hc, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

func roundInt(f float64) int {
	if f < 0 {
		return int(f - 0.5)
	}
	return int(f + 0.5)
}
