// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/stein155/soundtap/app/internal/upnp"
)

// UPnPRegistry is the subset of upnp.Registry the web layer needs.
// Satisfied by *upnp.Registry; interface keeps web_test builds fast.
type UPnPRegistry interface {
	Servers() []upnp.MediaServer
	Search(ctx context.Context, log func(string, ...any))
	Browse(ctx context.Context, usn, objectID string) ([]upnp.Item, error)
}

// GET /api/upnp/servers — TTL-pruned list of discovered MediaServer:1 devices.
func (s *Server) upnpServers(w http.ResponseWriter, r *http.Request) {
	if s.upnpReg == nil {
		http.NotFound(w, r)

		return
	}

	writeJSON(w, http.StatusOK, s.upnpReg.Servers())
}

// upnpSearchBudget is as long as any other read, so the peer proxy's default outlasts it.
const upnpSearchBudget = slowReadDeadline

// POST /api/upnp/search — one discovery round now, then the list. A POST because it
// sends multicast on the speaker's behalf.
func (s *Server) upnpSearch(w http.ResponseWriter, r *http.Request) {
	if s.upnpReg == nil {
		http.NotFound(w, r)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), upnpSearchBudget)
	defer cancel()

	s.upnpReg.Search(ctx, s.log.Info)
	writeJSON(w, http.StatusOK, s.upnpReg.Servers())
}

// POST /api/upnp/play — play a media URL from a browse result as the UPNP source.
// The daemon's player streams it through the arbiter bus, the same path a DLNA
// controller's SetAVTransportURI takes.
//
// With the server and folder it came from, the rest of that folder plays after it.
func (s *Server) upnpPlay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string `json:"url"`
		Name   string `json:"name"`
		Server string `json:"server"`
		Folder string `json:"folder"`
	}

	err := decodeJSON(w, r, smallBody, &body)
	if err != nil {
		s.badRequest(w, "bad body", err)

		return
	}

	if !upnp.MediaURL(r.Context(), body.URL) {
		s.badRequest(w, "url must be an http(s) media URL", nil)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), writeDeadline)
	defer cancel()

	s.upnpMu.Lock()
	s.upnpGen++
	gen := s.upnpGen
	s.upnpMu.Unlock()
	s.snapshotRecent(ctx)
	s.backend.Wake(ctx)

	err = s.backend.Select(ctx, sourceUPnP, "", body.URL, body.Name, "", "")
	if err != nil {
		s.fail(w, "play failed", err)

		return
	}

	s.log.Info("play upnp", "name", body.Name)
	s.queueFolder(ctx, gen, body.Server, body.Folder, body.URL)
	s.hub.nudge()
	writeJSON(w, http.StatusOK, map[string]string{"status": statusPlaying})
}

// queueFolder makes the played folder the queue, after the track is selected, because the
// queue's watcher ends a queue the speaker is not on. A folder that cannot be read leaves
// the one track playing. gen is the play's number: the browse can outlast a newer play, and
// its result must not replace that play's queue or track details.
func (s *Server) queueFolder(ctx context.Context, gen uint64, server, folder, url string) {
	if s.upnpQueue == nil {
		return
	}

	s.upnpMu.Lock()
	defer s.upnpMu.Unlock()

	if s.upnpGen != gen {
		return
	}

	s.upnpQueue.Clear()

	if s.upnpReg == nil || server == "" || folder == "" {
		return
	}
	// The lock is not held over the browse, or a newer play would wait for it.
	s.upnpMu.Unlock()
	items, err := s.upnpReg.Browse(ctx, server, folder)
	s.upnpMu.Lock()
	if s.upnpGen != gen {
		return
	}

	if err != nil {
		s.log.Warn("upnp queue: browse", "err", err)

		return
	}

	s.upnpQueue.Set(items, url)
	// The tap only names the track; the folder knows who made it.
	if it, ok := s.upnpQueue.Step(0); ok {
		_ = s.backend.SetTrackMeta(ctx, sourceUPnP, "", it.Artist, it.Art) // the title still shows
		s.setAlbum(sourceUPnP, it.Album)
	}
}

// GET /api/upnp/browse?server=<usn>&id=<objectid>
// Returns a folder's DIDL-Lite items as JSON, paged server-side (at most 1000).
func (s *Server) upnpBrowse(w http.ResponseWriter, r *http.Request) {
	if s.upnpReg == nil {
		http.NotFound(w, r)

		return
	}

	usn := r.URL.Query().Get("server")
	itemID := r.URL.Query().Get("id")

	if usn == "" {
		s.badRequest(w, "server param required", nil)

		return
	}

	if itemID == "" {
		itemID = "0" // default: root
	}

	ctx, cancel := context.WithTimeout(r.Context(), slowReadDeadline)
	defer cancel()

	items, err := s.upnpReg.Browse(ctx, usn, itemID)
	if errors.Is(err, upnp.ErrUnknownServer) {
		apiError(w, http.StatusNotFound, "unknown server")

		return
	}

	if err != nil {
		s.fail(w, "browse failed", err)

		return
	}

	writeJSON(w, http.StatusOK, items)
}

// GET /api/upnp/modes: whether a folder is queued, and its shuffle and repeat.
func (s *Server) upnpModes(w http.ResponseWriter, _ *http.Request) {
	if s.upnpQueue == nil {
		http.NotFound(w, nil)

		return
	}

	active, shuffle, repeat := s.upnpQueue.Modes()
	writeJSON(w, http.StatusOK, map[string]any{keyActive: active, "shuffle": shuffle, "repeat": repeat})
}

// POST /api/upnp/modes {"shuffle": bool, "repeat": "off"|"all"|"track"}; either may be left out.
func (s *Server) upnpSetModes(w http.ResponseWriter, r *http.Request) {
	if s.upnpQueue == nil {
		http.NotFound(w, r)

		return
	}

	var body struct {
		Shuffle *bool   `json:"shuffle"`
		Repeat  *string `json:"repeat"`
	}

	err := decodeJSON(w, r, smallBody, &body)
	if err != nil {
		s.badRequest(w, "invalid request", err)

		return
	}

	if body.Repeat != nil && !s.upnpQueue.SetRepeat(*body.Repeat) {
		s.badRequest(w, "repeat must be off, all or track", nil)

		return
	}

	if body.Shuffle != nil {
		s.upnpQueue.SetShuffle(*body.Shuffle)
	}

	s.upnpModes(w, r)
}
