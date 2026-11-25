// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/stein155/soundtap/app/internal/spotify"
)

const (
	// spotifyStatusTimeout bounds reading librespot's status.
	spotifyStatusTimeout = 3 * time.Second
	// spotifyPollEvery is how often a Spotify command is checked for taking effect.
	spotifyPollEvery = 50 * time.Millisecond
	// spotifyImageTimeout bounds fetching a cover image.
	spotifyImageTimeout = 10 * time.Second
)

// SpotifyAccount is what soundtapd does to the Spotify login itself; the browser can
// only ask for it to be forgotten, never see it.
type SpotifyAccount interface {
	// Forget deletes the stored Connect credentials and restarts the receiver, which
	// comes back unpaired.
	Forget() error
}

func (s *Server) spotifyRoutes(mux *routeMux) {
	mux.HandleFunc("GET /api/spotify/status", s.spotifyStatus)
	mux.HandleFunc("GET /api/spotify/search", s.spotifySearch)
	mux.HandleFunc("GET /api/spotify/artists/{id}", s.spotifyArtist)
	mux.HandleFunc("GET /api/spotify/albums/{id}", s.spotifyAlbum)
	mux.HandleFunc("GET /api/spotify/playlists/{id}", s.spotifyPlaylist)
	mux.HandleFunc("GET /api/spotify/me/playlists", s.spotifyMyPlaylists)
	mux.HandleFunc("GET /api/spotify/me/tracks", s.spotifyLiked)
	mux.HandleFunc("GET /api/spotify/me/recent", s.spotifyRecent)
	mux.HandleFunc("GET /api/spotify/me/following", s.spotifyFollowing)
	mux.HandleFunc("GET /api/spotify/player", s.spotifyPlayer)
	mux.HandleFunc("GET /api/spotify/images/{id}", s.spotifyImage)
	mux.HandleFunc("POST /api/spotify/play", s.spotifyPlay)
	mux.HandleFunc("POST /api/spotify/pause", s.spotifyCommand("pause"))
	mux.HandleFunc("POST /api/spotify/resume", s.spotifyCommand("play"))
	mux.HandleFunc("POST /api/spotify/next", s.spotifyCommand("next"))
	mux.HandleFunc("POST /api/spotify/previous", s.spotifyCommand("prev"))
	mux.HandleFunc("POST /api/spotify/seek", s.spotifySeek)
	mux.HandleFunc("GET /api/spotify/liked", s.spotifyLikedNow)
	mux.HandleFunc("POST /api/spotify/liked", s.spotifySetLiked)
	mux.HandleFunc("POST /api/spotify/shuffle", s.spotifyShuffle)
	mux.HandleFunc("POST /api/spotify/repeat", s.spotifyRepeat)
	mux.HandleFunc("POST /api/spotify/disconnect", s.spotifyDisconnect)
}

// spotifyError answers with the stable error code for err. Details stay in the log.
func (s *Server) spotifyError(w http.ResponseWriter, what string, err error) {
	code, status := "spotify_playback_failed", http.StatusBadGateway

	switch {
	case errors.Is(err, spotify.ErrNotAuthenticated):
		code, status = "spotify_not_authenticated", http.StatusConflict
	case errors.Is(err, spotify.ErrNotConnected):
		code, status = "spotify_not_connected", http.StatusServiceUnavailable
	case errors.Is(err, spotify.ErrUnavailable):
		code, status = "spotify_content_unavailable", http.StatusNotFound
	case errors.Is(err, spotify.ErrSearchUnavailable):
		code, status = "spotify_search_unavailable", http.StatusServiceUnavailable
	case errors.Is(err, spotify.ErrInvalid):
		code, status = "spotify_invalid_request", http.StatusBadRequest
	case errors.Is(err, context.DeadlineExceeded):
		code = "spotify_not_connected"
	default:
		// Anything else needs nothing here.
	}

	if status >= 500 && !errors.Is(err, spotify.ErrNotConnected) {
		s.log.Warn("spotify "+what, "err", err.Error())
	}

	apiError(w, status, code)
}

func (s *Server) spotifyReady(w http.ResponseWriter) bool {
	if s.spotify == nil {
		apiError(w, http.StatusServiceUnavailable, "spotify_not_connected")

		return false
	}

	return true
}

func offsetParam(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil {
		return 0
	}

	return n
}

func (s *Server) spotifyStatus(w http.ResponseWriter, r *http.Request) {
	if s.spotify == nil {
		writeJSON(w, http.StatusOK, spotify.Status{
			State: "unavailable", Available: false, Authenticated: false, Connected: false, ReauthRequired: false,
		})

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), spotifyStatusTimeout)
	defer cancel()

	writeJSON(w, http.StatusOK, s.spotify.Status(ctx))
}

// spotifyServe runs one catalogue call and writes its result.
func spotifyServe[T any](
	srv *Server, w http.ResponseWriter, r *http.Request, what string, call func(context.Context) (T, error),
) {
	if !srv.spotifyReady(w) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), spotifyDeadline)
	defer cancel()

	value, err := call(ctx)
	if err != nil {
		srv.spotifyError(w, what, err)

		return
	}

	writeJSON(w, http.StatusOK, value)
}

func (s *Server) spotifySearch(w http.ResponseWriter, r *http.Request) {
	spotifyServe(s, w, r, "search", func(ctx context.Context) (spotify.SearchResult, error) {
		return s.spotify.Search(ctx, r.URL.Query().Get("q"))
	})
}

func (s *Server) spotifyArtist(w http.ResponseWriter, r *http.Request) {
	spotifyServe(s, w, r, "artist", func(ctx context.Context) (spotify.ArtistPage, error) {
		return s.spotify.Artist(ctx, r.PathValue("id"))
	})
}

func (s *Server) spotifyAlbum(w http.ResponseWriter, r *http.Request) {
	type page struct {
		spotify.Album

		Next int `json:"next,omitempty"`
	}

	spotifyServe(s, w, r, "album", func(ctx context.Context) (page, error) {
		album, next, err := s.spotify.Album(ctx, r.PathValue("id"), offsetParam(r))
		if err != nil {
			return page{Album: album, Next: next}, fmt.Errorf("album: %w", err)
		}

		return page{Album: album, Next: next}, nil
	})
}

func (s *Server) spotifyPlaylist(w http.ResponseWriter, r *http.Request) {
	type page struct {
		spotify.Playlist

		Next int `json:"next,omitempty"`
	}

	spotifyServe(s, w, r, "playlist", func(ctx context.Context) (page, error) {
		playlist, next, err := s.spotify.Playlist(ctx, r.PathValue("id"), offsetParam(r))
		if err != nil {
			return page{Playlist: playlist, Next: next}, fmt.Errorf("playlist: %w", err)
		}

		return page{Playlist: playlist, Next: next}, nil
	})
}

func (s *Server) spotifyRecent(w http.ResponseWriter, r *http.Request) {
	spotifyServe(s, w, r, "recent plays", s.spotify.RecentlyPlayed)
}

func (s *Server) spotifyFollowing(w http.ResponseWriter, r *http.Request) {
	spotifyServe(s, w, r, "followed artists", s.spotify.FollowedArtists)
}

func (s *Server) spotifyMyPlaylists(w http.ResponseWriter, r *http.Request) {
	spotifyServe(s, w, r, "playlists", func(ctx context.Context) (spotify.Page[spotify.Playlist], error) {
		return s.spotify.MyPlaylists(ctx, offsetParam(r))
	})
}

func (s *Server) spotifyLiked(w http.ResponseWriter, r *http.Request) {
	spotifyServe(s, w, r, "liked", func(ctx context.Context) (spotify.Page[spotify.Track], error) {
		return s.spotify.LikedTracks(ctx, offsetParam(r))
	})
}

// spotifyPlayer is the now-playing state when Spotify owns the speaker. The receiver's
// event hook already reports track changes, so this reuses that rather than asking
// Spotify a second time.
func (s *Server) spotifyPlayer(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), backendCallTimeout)
	defer cancel()

	playing, err := s.backend.NowPlaying(ctx)
	if err != nil {
		s.fail(w, "now failed", err)

		return
	}

	if playing.Source != sourceSpotify {
		writeJSON(w, http.StatusOK, map[string]any{keyActive: false})

		return
	}

	s.np.Enrich(playing) //nolint:contextcheck // the metadata refresh runs on its own, past this request

	modes := spotify.Modes{Repeat: "off", Shuffle: false}
	if s.spotify != nil {
		modes = s.spotify.PlayModes(ctx)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		keyActive: true, "nowPlaying": playing, "shuffle": modes.Shuffle, "repeat": modes.Repeat,
	})
}

// spotifyPlayItem names what is playing, so a held preset key can store it.
type spotifyPlayItem struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
	Art  string `json:"art"`
}

func (s *Server) spotifyPlay(w http.ResponseWriter, r *http.Request) {
	if !s.spotifyReady(w) {
		return
	}

	var body struct {
		URI     string           `json:"uri"`
		Context string           `json:"context"`
		Item    *spotifyPlayItem `json:"item"`
	}

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, smallBody)).Decode(&body)
	if err != nil {
		s.badRequest(w, "spotify_invalid_request", err)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), spotifyDeadline)
	defer cancel()

	err = s.spotify.Play(ctx, body.URI, body.Context)
	if err != nil {
		s.spotifyError(w, "play", err)

		return
	}

	if rb, ok := s.backend.(interface{ RememberSpotify(uri, name, art string) }); ok && body.Item != nil {
		if validSpotifyPreset(body.Item.URI) {
			rb.RememberSpotify(body.Item.URI, body.Item.Name, body.Item.Art)
		}
	}

	s.hub.nudge()
	writeOK(w)
}

func (s *Server) spotifyCommand(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.spotifyReady(w) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), spotifyDeadline)
		defer cancel()

		err := s.spotify.Command(ctx, name)
		if err != nil {
			s.spotifyError(w, name, err)

			return
		}
		// librespot accepts every command, then drops it while its Connect session is
		// not active ("will be ignored while Not Active"), and the UI showed a pause or
		// a resume that never happened. Its hook reports both transitions, so
		// wait for that report rather than record the command's hopes.
		if (name == "pause" || name == "play") && !s.spotifyConfirmed(r.Context(), name) {
			apiError(w, http.StatusConflict, "spotify_command_ignored")

			return
		}

		s.hub.nudge()
		writeOK(w)
	}
}

// spotifyAckWait bounds how long a pause or resume may take to be reported by
// librespot's hook. A variable so tests need not wait it out.
//
//nolint:gochecknoglobals // a variable so tests can replace it
var spotifyAckWait = 3 * time.Second

// spotifyConfirmed waits for /api/now to show Spotify paused after the "pause" command, or
// not paused after "play". It answers true when Spotify does not own the amp: then there is
// no status to confirm.
func (s *Server) spotifyConfirmed(ctx context.Context, command string) bool {
	paused := command == "pause"

	ctx, cancel := context.WithTimeout(ctx, spotifyAckWait)
	defer cancel()

	tick := time.NewTicker(spotifyPollEvery)
	defer tick.Stop()

	for {
		np, err := s.backend.NowPlaying(ctx)
		if err == nil && (np.Source != sourceSpotify || (np.PlayStatus == "PAUSE_STATE") == paused) {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

func (s *Server) spotifySeek(w http.ResponseWriter, r *http.Request) {
	if !s.spotifyReady(w) {
		return
	}

	var body struct {
		PositionMs int `json:"positionMs"`
	}

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, smallBody)).Decode(&body)
	if err != nil {
		s.badRequest(w, "spotify_invalid_request", err)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), spotifyDeadline)
	defer cancel()

	err = s.spotify.Seek(ctx, body.PositionMs)
	if err != nil {
		s.spotifyError(w, "seek", err)

		return
	}

	writeOK(w)
}

// spotifyLikedNow says whether what plays is in Liked Songs. Available is false for what
// cannot be: an episode, or nothing yet.
func (s *Server) spotifyLikedNow(w http.ResponseWriter, r *http.Request) {
	if !s.spotifyReady(w) {
		return
	}

	uri := s.spotifyTrack.Load()
	if uri == nil || !spotify.Likeable(*uri) {
		writeJSON(w, http.StatusOK, map[string]bool{"available": false, "liked": false})

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), spotifyDeadline)
	defer cancel()

	liked, err := s.spotify.Liked(ctx, *uri)
	if err != nil {
		s.spotifyError(w, "liked", err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"available": true, "liked": liked})
}

// spotifySetLiked puts what plays in Liked Songs or takes it out.
func (s *Server) spotifySetLiked(w http.ResponseWriter, r *http.Request) {
	if !s.spotifyReady(w) {
		return
	}

	var body struct {
		On bool `json:"on"`
	}

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, smallBody)).Decode(&body)
	if err != nil {
		s.badRequest(w, "spotify_invalid_request", err)

		return
	}

	uri := s.spotifyTrack.Load()
	if uri == nil {
		s.spotifyError(w, "liked", spotify.ErrInvalid)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), spotifyDeadline)
	defer cancel()

	err = s.spotify.SetLiked(ctx, *uri, body.On)
	if err != nil {
		s.spotifyError(w, "liked", err)

		return
	}

	writeOK(w)
}

func (s *Server) spotifyShuffle(w http.ResponseWriter, r *http.Request) {
	if !s.spotifyReady(w) {
		return
	}

	var body struct {
		On bool `json:"on"`
	}

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, smallBody)).Decode(&body)
	if err != nil {
		s.badRequest(w, "spotify_invalid_request", err)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), spotifyDeadline)
	defer cancel()

	err = s.spotify.SetShuffle(ctx, body.On)
	if err != nil {
		s.spotifyError(w, "shuffle", err)

		return
	}

	writeOK(w)
}

func (s *Server) spotifyRepeat(w http.ResponseWriter, r *http.Request) {
	if !s.spotifyReady(w) {
		return
	}

	var body struct {
		Mode string `json:"mode"`
	}

	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, smallBody)).Decode(&body)
	if err != nil {
		s.badRequest(w, "spotify_invalid_request", err)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), spotifyDeadline)
	defer cancel()

	err = s.spotify.SetRepeat(ctx, body.Mode)
	if err != nil {
		s.spotifyError(w, "repeat", err)

		return
	}

	writeOK(w)
}

func (s *Server) spotifyDisconnect(w http.ResponseWriter, _ *http.Request) {
	if s.spotifyAccount == nil {
		apiError(w, http.StatusServiceUnavailable, "spotify_not_connected")

		return
	}

	err := s.spotifyAccount.Forget()
	if err != nil {
		s.fail(w, "spotify disconnect failed", err)

		return
	}

	s.log.Info("spotify account forgotten")
	writeOK(w)
}

// ---- artwork -----------------------------------------------------------------------

// Artwork goes through the speaker so the browser never talks to Spotify, and repeat
// views of the same list do not refetch. The cache is small on purpose: 128 MB of RAM.
const (
	imageCacheBytes = 4 << 20
	imageMaxBytes   = 512 << 10
)

type imageEntry struct {
	id   string
	typ  string
	data []byte
}

type imageCache struct {
	mu    sync.Mutex
	size  int
	order *list.List // front = most recently used
	byID  map[string]*list.Element
}

func (c *imageCache) get(imageID string) (imageEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.byID == nil {
		return imageEntry{id: "", typ: "", data: nil}, false
	}

	elem, ok := c.byID[imageID]
	if !ok {
		return imageEntry{id: "", typ: "", data: nil}, false
	}

	c.order.MoveToFront(elem)
	entry, ok := elem.Value.(imageEntry)

	return entry, ok
}

func (c *imageCache) put(entry imageEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.byID == nil {
		c.byID, c.order = map[string]*list.Element{}, list.New()
	}

	if _, ok := c.byID[entry.id]; ok {
		return
	}

	c.byID[entry.id] = c.order.PushFront(entry)

	c.size += len(entry.data)
	for c.size > imageCacheBytes {
		old := c.order.Back()
		oe, _ := old.Value.(imageEntry) // the list holds only imageEntry values
		c.order.Remove(old)
		delete(c.byID, oe.id)
		c.size -= len(oe.data)
	}
}

func (s *Server) spotifyImage(w http.ResponseWriter, r *http.Request) {
	imageID := r.PathValue("id")

	src, ok := spotify.ImageURL(imageID)
	if !ok {
		http.NotFound(w, r)

		return
	}

	entry, hit := s.spotifyImages.get(imageID)
	if !hit {
		ctx, cancel := context.WithTimeout(r.Context(), spotifyImageTimeout)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, http.NoBody)
		if err != nil {
			http.NotFound(w, r)

			return
		}

		resp, err := s.proxy.Do(req)
		if err != nil {
			apiError(w, http.StatusBadGateway, "image unavailable")

			return
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(io.LimitReader(resp.Body, imageMaxBytes+1))

		typ := resp.Header.Get("Content-Type")
		if err != nil || resp.StatusCode != http.StatusOK || len(data) > imageMaxBytes ||
			(typ != "image/jpeg" && typ != "image/png" && typ != "image/webp") {
			apiError(w, http.StatusBadGateway, "image unavailable")

			return
		}

		entry = imageEntry{id: imageID, typ: typ, data: data}
		s.spotifyImages.put(entry)
	}

	w.Header().Set("Content-Type", entry.typ)
	// Spotify image ids are content hashes: the bytes behind one never change.
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	_, _ = w.Write(entry.data)
}
