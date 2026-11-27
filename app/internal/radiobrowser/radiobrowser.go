// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package radiobrowser looks a station up by its uuid, or by its exact name, in Radio
// Browser (radio-browser.info), the open station directory stock's Radio Browser presets
// name. A request carries the uuid or the name and the User-Agent and nothing else: no
// account, no key, nothing about the speaker or its owner (ADR 0129).
package radiobrowser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/safehttp"
)

// servers are the Radio Browser API servers asked in turn: the named ones the project
// runs, then the round-robin name that covers every server. Radio Browser keeps them at
// the same version and data.
func servers() []string {
	return []string{
		"https://de1.api.radio-browser.info",
		"https://de2.api.radio-browser.info",
		"https://fi1.api.radio-browser.info",
		"https://all.api.radio-browser.info",
	}
}

// timeout bounds one request to one server.
const timeout = 10 * time.Second

// Errors a lookup ends in.
var (
	// ErrUnknownStation is a server that answered and knows no station by that uuid.
	ErrUnknownStation = errors.New("radio browser knows no such station")
	// ErrInvalidUUID is a uuid that is not one: nothing is asked.
	ErrInvalidUUID = errors.New("not a station uuid")
	// ErrNoServer is a lookup no server answered.
	ErrNoServer = errors.New("no radio browser server answered")
)

// Station is the part of a Radio Browser station a preset needs.
type Station struct {
	UUID        string `json:"stationuuid"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	URLResolved string `json:"url_resolved"`
	Favicon     string `json:"favicon"`
	// CountryCode is the station's country, ISO 3166-1 alpha-2, "" when Radio Browser has none.
	CountryCode string `json:"countrycode"`
	// LastCheckOK is 1 when Radio Browser's last check of the stream found it playing.
	LastCheckOK int `json:"lastcheckok"`
}

// StreamURL is what plays the station: the resolved stream, or the address the station
// was entered with when Radio Browser resolved none.
func (s *Station) StreamURL() string {
	if resolved := strings.TrimSpace(s.URLResolved); resolved != "" {
		return resolved
	}

	return strings.TrimSpace(s.URL)
}

// uuidRe matches a station uuid in its usual hyphenated form.
var uuidRe = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

// ValidUUID reports whether id is a station uuid. It goes into a URL path, so nothing
// else is asked.
func ValidUUID(id string) bool { return uuidRe.MatchString(id) }

// Client asks the Radio Browser servers.
type Client struct {
	http      *http.Client
	userAgent string
	servers   []string
}

// New is a Client that asks Radio Browser's own servers over public addresses only, as
// userAgent.
func New(userAgent string) *Client {
	return NewWith(&http.Client{Timeout: timeout, Transport: safehttp.SafeTransport()}, userAgent, servers())
}

// NewWith is a Client that asks servers through client; a test points it at its own.
func NewWith(client *http.Client, userAgent string, servers []string) *Client {
	return &Client{http: client, userAgent: userAgent, servers: append([]string(nil), servers...)}
}

// Station looks the station with uuid up, asking the next server when one does not answer
// or answers with something that is not a station list. ErrUnknownStation: a server
// answered with no station by that uuid. ErrNoServer: none answered.
func (c *Client) Station(ctx context.Context, uuid string) (Station, error) {
	var none Station

	if !ValidUUID(uuid) {
		return none, fmt.Errorf("%w: %q", ErrInvalidUUID, uuid)
	}

	stations, err := c.ask(ctx, "/json/stations/byuuid/"+url.PathEscape(strings.ToLower(uuid)))
	if err != nil {
		return none, err
	}

	for _, station := range stations {
		if strings.EqualFold(station.UUID, uuid) {
			return station, nil
		}
	}

	return none, fmt.Errorf("%w: %s", ErrUnknownStation, uuid)
}

// ByName lists the stations Radio Browser names exactly name (bynameexact), asking the next
// server as Station does: none when a server answered with none. ErrNoServer: none answered.
func (c *Client) ByName(ctx context.Context, name string) ([]Station, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}

	return c.ask(ctx, "/json/stations/bynameexact/"+url.PathEscape(name))
}

// ask asks the servers in turn for the station list at path, up to the first that answers
// with one. ErrNoServer: none answered.
func (c *Client) ask(ctx context.Context, path string) ([]Station, error) {
	var errs []error

	for _, server := range c.servers {
		var stations []Station

		err := safehttp.GetJSON(ctx, c.http, c.userAgent, strings.TrimRight(server, "/")+path, &stations)
		if err == nil {
			return stations, nil
		}

		errs = append(errs, err)

		if ctx.Err() != nil {
			break
		}
	}

	return nil, fmt.Errorf("%w: %w", ErrNoServer, errors.Join(errs...))
}
