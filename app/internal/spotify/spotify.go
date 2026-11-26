// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package spotify gives soundtapd Spotify search, browsing and playback through the
// Spotify Connect session librespot already holds. There is no second login and no
// developer app: librespot (patched, see os/package/soundtap-librespot) serves a
// loopback control API, and this package turns what it returns into SoundTap's own
// models. Nothing here ever hands a credential or token to a browser.
//
//nolint:revive // max-public-structs: the package's models are the API types internal/web serves
package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/safego"
)

// CtlAddr is where librespot serves its control API. soundtapd passes it to librespot
// in SOUNDTAP_SPOTIFY_CTL; loopback only.
const CtlAddr = "127.0.0.1:4071"

// Errors the HTTP layer maps to stable API error codes.
var (
	ErrNotAuthenticated = errors.New("spotify_not_authenticated")
	ErrNotConnected     = errors.New("spotify_not_connected")
	ErrUnavailable      = errors.New("spotify_content_unavailable")
	ErrPlayback         = errors.New("spotify_playback_failed")
	ErrInvalid          = errors.New("spotify_invalid_request")
)

// errControl is a control-API call that failed; the text after it names the call.
var errControl = errors.New("control")

const (
	// ctlTimeout bounds every call to the control API.
	ctlTimeout = 15 * time.Second
	// maxStatusBody caps a /status answer, a few fields.
	maxStatusBody = 4096
	// maxCtlBody caps any other control-API answer: a page of metadata at most.
	maxCtlBody = 8 << 20
	// volumeTimeout bounds handing one volume level to librespot.
	volumeTimeout = 2 * time.Second
)

// Client talks to librespot's control API, and through it to Spotify's own client
// endpoints on the session librespot holds.
type Client struct {
	Ctl  string // base URL of the control API
	HTTP *http.Client

	cache metaCache // normalised tracks and albums, by id

	// heard is the last volume librespot reported itself (+1, so 0 means none).
	// FollowVolume does not send it back: while the app's slider moves, that echo
	// arrives stale and dragged the slider back and forth.
	heard atomic.Int64
}

// NoteVolume records a level that came from Spotify, so FollowVolume skips its echo.
func (c *Client) NoteVolume(level int) { c.heard.Store(int64(level) + 1) }

// New returns a client for the librespot control API on CtlAddr.
func New() *Client {
	return &Client{
		Ctl: "http://" + CtlAddr,
		// librespot control API is on loopback, which SafeTransport refuses.
		HTTP:  &http.Client{Timeout: ctlTimeout},
		cache: metaCache{mu: sync.Mutex{}, order: nil, byKey: nil},
		heard: atomic.Int64{},
	}
}

// Status is all the browser ever learns about the Spotify account.
type Status struct {
	Available      bool   `json:"available"`
	Authenticated  bool   `json:"authenticated"`
	Connected      bool   `json:"connected"`
	ReauthRequired bool   `json:"reauthRequired"`
	State          string `json:"state"` // unpaired, connecting, connected, reconnecting, auth_required, unavailable
}

type ctlStatus struct {
	State    string `json:"state"`
	Username string `json:"username"`
	Country  string `json:"country"`
	Context  string `json:"context"`
	Shuffle  bool   `json:"shuffle"`
	Repeat   string `json:"repeat"`
}

// Status reports the session state. librespot not running is "unavailable", not an error.
func (c *Client) Status(ctx context.Context) Status {
	session, err := c.ctlStatus(ctx)
	if err != nil {
		return Status{
			Available: false, Authenticated: false, Connected: false, ReauthRequired: false, State: "unavailable",
		}
	}

	out := Status{
		Available: true, Authenticated: false, Connected: false, ReauthRequired: false, State: session.State,
	}
	switch session.State {
	case "connected":
		out.Authenticated, out.Connected = true, true
	case "connecting", "reconnecting":
		out.Authenticated = true
	case "auth_required":
		out.ReauthRequired = true
	default:
	}

	return out
}

// errFromState turns a control-API 503 into the error the caller should report.
func errFromState(state string) error {
	switch state {
	case "connecting", "reconnecting":
		return ErrNotConnected
	default:
		return ErrNotAuthenticated
	}
}

// ctlState is the body of a control-API 503: the session's state.
type ctlState struct {
	State string `json:"State"`
}

// ctlError is the body of a control-API 502: what Spotify answered.
type ctlError struct {
	Kind    string `json:"Kind"`
	Message string `json:"Message"`
}

// ---- SoundTap models ------------------------------------------------------------

type Ref struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type Track struct {
	URI        string `json:"uri"`
	Name       string `json:"name"`
	Artists    []Ref  `json:"artists"`
	Album      *Ref   `json:"album,omitempty"`
	Artwork    string `json:"artwork,omitempty"`
	DurationMs int    `json:"durationMs"`
}

type Album struct {
	URI         string  `json:"uri"`
	Name        string  `json:"name"`
	Artists     []Ref   `json:"artists"`
	Artwork     string  `json:"artwork,omitempty"`
	ReleaseDate string  `json:"releaseDate,omitempty"`
	Group       string  `json:"group,omitempty"` // on an artist page: album, single, compilation or appears_on
	Total       int     `json:"total,omitempty"` // every track on the album; Tracks may be one page of it
	Tracks      []Track `json:"tracks,omitempty"`
}

type Artist struct {
	URI     string `json:"uri"`
	Name    string `json:"name"`
	Artwork string `json:"artwork,omitempty"`
}

type Playlist struct {
	URI     string  `json:"uri"`
	Name    string  `json:"name"`
	Owner   string  `json:"owner,omitempty"`
	Artwork string  `json:"artwork,omitempty"`
	Total   int     `json:"total"`
	Tracks  []Track `json:"tracks,omitempty"`
}

type SearchResult struct {
	Tracks    []Track    `json:"tracks"`
	Artists   []Artist   `json:"artists"`
	Albums    []Album    `json:"albums"`
	Playlists []Playlist `json:"playlists"`
}

type ArtistPage struct {
	Artist    Artist  `json:"artist"`
	TopTracks []Track `json:"topTracks"`
	Albums    []Album `json:"albums"`
}

// Page is one slice of a longer list; Next is the offset to ask for, or 0 when done.
type Page[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
	Next  int `json:"next,omitempty"`
}

// ---- IDs and URIs --------------------------------------------------------------

var base62 = regexp.MustCompile(`^[0-9A-Za-z]{22}$`)

// ValidID reports whether id is a Spotify base62 id.
func ValidID(id string) bool { return base62.MatchString(id) }

// ParseURI splits spotify:<kind>:<id> and validates both parts. The one exception is
// the user's own collection (Liked Songs), whose URI names the user.
func ParseURI(uri string) (string, string, bool) {
	parts := strings.Split(uri, ":")
	if len(parts) != uriParts || parts[0] != "spotify" || !ValidID(parts[2]) {
		return "", "", false
	}

	switch parts[1] {
	case kindTrack, kindAlbum, kindArtist, kindPlaylist:
		return parts[1], parts[2], true
	default:
		return "", "", false
	}
}

// uriParts is how many parts spotify:<kind>:<id> has.
const uriParts = 3

// PlayingContext names what the device plays, whoever started it: a URI a preset can hold,
// or "" with known=false when the receiver cannot say. known with an empty URI is a context
// that cannot be played again from a preset, such as a radio station.
func (c *Client) PlayingContext(ctx context.Context) (string, bool) {
	status, err := c.ctlStatus(ctx)
	if err != nil || status.Context == "" {
		return "", false
	}
	// Liked Songs reports as spotify:user:<name>:collection; the name stays out of a preset.
	if strings.HasSuffix(status.Context, ":collection") {
		return LikedContext, true
	}

	if _, _, ok := ParseURI(status.Context); ok {
		return status.Context, true
	}

	return "", true
}

// ---- playback ----------------------------------------------------------------------

// LikedContext is the pseudo URI the UI uses for "play my Liked Songs".
const LikedContext = "spotify:collection"

// Play starts uri (a track) and/or context (album, playlist, artist or Liked Songs)
// on this speaker, making it the active Connect device. A track with a context starts
// the context at that track, so next/previous and ordering stay the context's.
func (c *Client) Play(ctx context.Context, uri, contextURI string) error {
	query, err := c.loadQuery(ctx, uri, contextURI)
	if err != nil {
		return err
	}

	_, err = c.ctl(ctx, http.MethodPost, "/load", query)
	if err != nil {
		if errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotConnected) || errors.Is(err, ErrNotAuthenticated) {
			return err
		}

		return fmt.Errorf("%w: %w", ErrPlayback, err)
	}

	return nil
}

// Command sends play, pause, next or prev.
func (c *Client) Command(ctx context.Context, name string) error {
	switch name {
	case "play", "pause", "next", "prev":
	default:
		return ErrInvalid
	}

	_, err := c.ctl(ctx, http.MethodPost, "/"+name, nil)
	if err != nil {
		if errors.Is(err, ErrNotConnected) || errors.Is(err, ErrNotAuthenticated) {
			return err
		}

		return fmt.Errorf("%w: %w", ErrPlayback, err)
	}

	return nil
}

// FollowVolume returns a hook that hands the speaker's volume (percent) to librespot,
// so the Spotify apps' slider follows the buttons, the remote and the web UI. It never
// blocks: a burst of key presses collapses to the latest level, sent from one
// goroutine so they cannot arrive out of order. librespot ignores a level it already
// has, which is what ends the echo of a change that came from Spotify itself.
func (c *Client) FollowVolume(ctx context.Context) func(int) {
	latest := make(chan int, 1)

	safego.Go("spotify.followVolume", func() {
		for {
			select {
			case <-ctx.Done():
				return
			case level := <-latest:
				sendCtx, cancel := context.WithTimeout(ctx, volumeTimeout)
				_, _ = c.ctl(sendCtx, http.MethodPost, "/volume", url.Values{"percent": {strconv.Itoa(level)}})

				cancel()
			}
		}
	})

	return func(level int) {
		// Spotify's own level: skip it. Any other change clears the mark, so a later
		// return to that level from the buttons is still sent.
		if c.heard.Swap(0) == int64(level)+1 {
			return
		}

		select {
		case <-latest:
		default:
		}

		select {
		case latest <- level:
		default:
		}
	}
}

// Modes are shuffle and repeat as the device has them, whoever set them. Repeat is
// off, context or track.
type Modes struct {
	Shuffle bool   `json:"shuffle"`
	Repeat  string `json:"repeat"`
}

// PlayModes reports shuffle and repeat. An older receiver without them reads as off.
func (c *Client) PlayModes(ctx context.Context) Modes {
	status, err := c.ctlStatus(ctx)
	if err != nil || status.Repeat == "" {
		return Modes{Shuffle: false, Repeat: "off"}
	}

	return Modes{Shuffle: status.Shuffle, Repeat: status.Repeat}
}

const uriParam = "uri"

// Likeable says whether uri can go in Liked Songs: a track, not a podcast episode.
func Likeable(uri string) bool { return strings.HasPrefix(uri, "spotify:track:") }

// Liked says whether a track is in Liked Songs. Spotify answers a list of booleans, here
// of one, whose last byte is that boolean however the list is packed.
func (c *Client) Liked(ctx context.Context, uri string) (bool, error) {
	if !Likeable(uri) {
		return false, ErrInvalid
	}

	body, err := c.ctl(ctx, http.MethodGet, "/liked", url.Values{uriParam: {uri}})
	if err != nil {
		return false, err
	}

	return len(body) > 0 && body[len(body)-1] == 1, nil
}

// SetLiked puts a track in Liked Songs or takes it out.
func (c *Client) SetLiked(ctx context.Context, uri string, on bool) error {
	if !Likeable(uri) {
		return ErrInvalid
	}

	_, err := c.ctl(ctx, http.MethodPost, "/liked", url.Values{uriParam: {uri}, "on": {onParam(on)}})

	return err
}

// onParam is how the control API spells a switch's new state.
//
//nolint:revive // flag-parameter: on is the state sent to librespot, data and not a mode of this function
func onParam(on bool) string {
	if on {
		return "1"
	}

	return "0"
}

// SetShuffle turns shuffle on or off for what plays.
func (c *Client) SetShuffle(ctx context.Context, on bool) error {
	_, err := c.ctl(ctx, http.MethodPost, "/shuffle", url.Values{"on": {onParam(on)}})

	return err
}

// SetRepeat sets repeat to off, context or track.
func (c *Client) SetRepeat(ctx context.Context, mode string) error {
	if mode != "off" && mode != "context" && mode != "track" {
		return ErrInvalid
	}

	_, err := c.ctl(ctx, http.MethodPost, "/repeat", url.Values{"mode": {mode}})

	return err
}

// Seek moves playback to ms from the start of the track.
func (c *Client) Seek(ctx context.Context, ms int) error {
	if ms < 0 || ms > 24*60*60*1000 {
		return ErrInvalid
	}

	_, err := c.ctl(ctx, http.MethodPost, "/seek", url.Values{"ms": {strconv.Itoa(ms)}})

	return err
}

func (c *Client) ctlStatus(ctx context.Context) (ctlStatus, error) {
	var status ctlStatus

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Ctl+"/status", http.NoBody)
	if err != nil {
		return status, fmt.Errorf("control /status: %w", err)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return status, fmt.Errorf("control /status: %w", err)
	}
	defer resp.Body.Close()

	err = json.NewDecoder(io.LimitReader(resp.Body, maxStatusBody)).Decode(&status)
	if err != nil {
		return status, fmt.Errorf("control /status: %w", err)
	}

	return status, nil
}

// ctl calls one control-API endpoint and returns its body.
func (c *Client) ctl(ctx context.Context, method, path string, query url.Values) ([]byte, error) {
	target := c.Ctl + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, target, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("%w %s: %w", errControl, path, err)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, ErrNotConnected // librespot is not running (yet)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCtlBody))
	if err != nil {
		return nil, fmt.Errorf("%w %s: %w", errControl, path, err)
	}

	switch {
	case resp.StatusCode == http.StatusServiceUnavailable:
		var state ctlState

		_ = json.Unmarshal(body, &state)

		return nil, errFromState(state.State)
	case resp.StatusCode == http.StatusBadRequest:
		return nil, ErrInvalid
	case resp.StatusCode == http.StatusBadGateway:
		var failed ctlError

		_ = json.Unmarshal(body, &failed)
		if failed.Kind == "NotFound" || failed.Kind == "InvalidArgument" {
			return nil, fmt.Errorf("%w: %s", ErrUnavailable, failed.Message)
		}

		return nil, fmt.Errorf("%w %s: %s %s", errControl, path, failed.Kind, failed.Message)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w %s: %s", errControl, path, strings.TrimSpace(string(body)))
	default:
		return body, nil
	}
}

// loadQuery is the /load query for Play's uri and contextURI.
func (c *Client) loadQuery(ctx context.Context, uri, contextURI string) (url.Values, error) {
	query := url.Values{}

	if uri != "" {
		if kind, _, ok := ParseURI(uri); !ok || kind != kindTrack {
			return nil, ErrInvalid
		}

		query.Set(uriParam, uri)
	}

	switch {
	case contextURI == LikedContext:
		status, err := c.ctlStatus(ctx)
		if err != nil || status.Username == "" {
			return nil, ErrNotConnected
		}

		query.Set("context", "spotify:user:"+status.Username+":collection")
	case contextURI != "":
		if kind, _, ok := ParseURI(contextURI); !ok || kind == kindTrack {
			return nil, ErrInvalid
		}

		query.Set("context", contextURI)
	case uri == "":
		return nil, ErrInvalid
	default:
	}

	return query, nil
}
