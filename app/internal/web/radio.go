// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/safehttp"
	"github.com/stein155/soundtap/app/internal/speaker"
	"github.com/stein155/soundtap/app/internal/tunein"
	"github.com/stein155/soundtap/app/internal/upnp"
)

const (
	// tuneinAnswerLimit bounds a TuneIn answer relayed to the UI.
	tuneinAnswerLimit = 256 << 10
	// stationAskTimeout bounds asking whether a station plays.
	stationAskTimeout = 3 * time.Second
)

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		writeJSON(w, http.StatusOK, []tunein.Station{})

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), slowReadDeadline)
	defer cancel()

	stations, err := s.tunein.Search(ctx, query)
	if err != nil {
		s.fail(w, "search failed", err)

		return
	}

	writeJSON(w, http.StatusOK, stations)
}

// tuneinProxy mirrors a request to https://opml.radiotime.com so the browser can
// hit TuneIn's OPML API same-origin (TuneIn sends no CORS headers). Only the
// path under /api/tunein/ and the query string are forwarded; the host is fixed.
// The path is forwarded as the browser escaped it: decoding it first would turn an
// encoded "?" or "#" into a real query or fragment. A ".." segment, plain or encoded,
// is refused rather than left for TuneIn to resolve.
func (s *Server) tuneinProxy(w http.ResponseWriter, r *http.Request) {
	path, ok := strings.CutPrefix(r.URL.EscapedPath(), "/api/tunein")
	if !ok || path == "" || path == "/" {
		apiError(w, http.StatusBadRequest, "missing path")

		return
	}

	for segment := range strings.SplitSeq(path, "/") {
		decoded, err := url.PathUnescape(segment)
		if err != nil || decoded == ".." {
			apiError(w, http.StatusBadRequest, "bad path")

			return
		}
	}

	target := tuneInBase + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	s.relay(w, r, target, "application/json", tuneinAnswerLimit, "")
}

// logoProxy fetches a TuneIn/CDN logo image given ?u=<absolute url>. Only http/
// https image hosts are followed and the body is size-capped.
func (s *Server) logoProxy(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	if raw == "" {
		apiError(w, http.StatusBadRequest, "missing u")

		return
	}

	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		apiError(w, http.StatusBadRequest, "bad url")

		return
	}
	// Logos live on public CDNs. Refuse loopback/LAN targets so the proxy can't
	// be used to reach the speaker's own loopback-only routes or other LAN hosts.
	if !publicHost(r.Context(), parsed.Hostname()) {
		apiError(w, http.StatusBadRequest, "bad url")

		return
	}
	// A logo at a given URL does not change; let the browser keep it instead of asking
	// the speaker (and the speaker the CDN) on every page load.
	s.relay(w, r, parsed.String(), "image/png", logoMaxBytes, "public, max-age=86400")
}

// publicHost reports whether host resolves exclusively to globally routable
// addresses — i.e. it is not loopback, RFC1918/ULA, link-local or unspecified.
// It is a fast up-front reject; publicOnlyTransport is the authoritative guard
// (it re-checks the actual address dialed, so a redirect or a DNS rebind between
// this check and the connection can't slip a private target through).
func publicHost(ctx context.Context, host string) bool {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return false
	}

	for _, ip := range ips {
		if !safehttp.PublicIP(ip.IP) {
			return false
		}
	}

	return true
}

// publicOnlyTransport is the transport for the outbound proxy client (logo/TuneIn
// mirrors, artwork + GitHub lookups): its dialer rejects any non-public address,
// per hop, closing the SSRF holes a pre-flight hostname check alone leaves open.
func publicOnlyTransport() *http.Transport { return safehttp.SafeTransport() }

// sandboxForeign marks an answer whose body came from somewhere else (a CDN, another
// speaker) so that it cannot run on this speaker's origin when opened directly: an SVG
// logo passes the image check and runs its scripts, and a peer in the registry, which
// unauthenticated multicast fills, can answer text/html. The sandbox gives the document an
// opaque origin; <img>, fetch and EventSource ignore it.
func sandboxForeign(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
}

// relay performs a bounded upstream GET and copies the response back. fallbackCT
// is used only when the upstream omits a Content-Type; cache, when set, is the
// Cache-Control of a 200 answer.
func (s *Server) relay(w http.ResponseWriter, r *http.Request, target, fallbackCT string, limit int64, cache string) {
	ctx, cancel := context.WithTimeout(r.Context(), slowReadDeadline)
	defer cancel()

	// G704: callers pass TuneIn's fixed API base or a host publicHost accepted, and
	// s.proxy dials public addresses only.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody) //nolint:gosec // G704: see above
	if err != nil {
		s.fail(w, "proxy build", err)

		return
	}

	req.Header.Set("User-Agent", "SoundTap/1.0")

	resp, err := safehttp.Do(ctx, s.proxy, req)
	if err != nil {
		s.fail(w, "proxy fetch", err)

		return
	}

	defer func() { _ = resp.Body.Close() }()
	// Never reflect the upstream Content-Type verbatim: this proxy fetches
	// attacker-chosen public URLs, and echoing e.g. "text/html" back on the
	// speaker's own origin would let a crafted logo URL execute script here.
	// Keep the upstream type only when it stays within the expected family
	// (e.g. image/jpeg for a logo), otherwise fall back to the safe default,
	// and forbid MIME sniffing so the declared type is authoritative.
	contentType := fallbackCT

	upstream, fam := resp.Header.Get("Content-Type"), fallbackCT[:strings.IndexByte(fallbackCT, '/')+1]
	if fam == "image/" && !strings.HasPrefix(upstream, fam) {
		// The logo proxy takes any public URL: relaying only images keeps it from being
		// an open relay for pages fetched from the owner's address.
		apiError(w, http.StatusBadGateway, "not an image")

		return
	}

	if upstream != "" && strings.HasPrefix(upstream, fam) {
		contentType = upstream
	}

	w.Header().Set("Content-Type", contentType)
	sandboxForeign(w.Header())

	if cache != "" && resp.StatusCode == http.StatusOK {
		w.Header().Set("Cache-Control", cache)
	}

	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, limit))
}

func (s *Server) playStation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		StationID string `json:"stationId"`
		Name      string `json:"name"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil || !validStation(r.Context(), body.StationID) {
		s.badRequest(w, "valid stationId required", err)

		return
	}

	s.playStationID(w, r, body.StationID, body.Name)
}

// playStationID selects a NATIVE TUNEIN ContentItem; the speaker resolves and
// streams it itself.
func (s *Server) playStationID(w http.ResponseWriter, r *http.Request, stationID, name string) {
	ctx, cancel := context.WithTimeout(r.Context(), writeDeadline)
	defer cancel()

	if s.neverPlays(ctx, stationID) {
		apiError(w, http.StatusBadGateway, "station has no stream")

		return
	}

	s.snapshotRecent(ctx)
	s.backend.Wake(ctx)

	location := speaker.StationLocation(stationID)

	err := s.backend.Select(ctx, sourceTuneIn, "stationurl", location, name, "", speaker.InternetRadioIcon)
	if err != nil {
		s.fail(w, "play failed", err)

		return
	}

	s.log.Info("play", "station", stationID, "name", name)
	s.hub.nudge() // push the new playback state to browsers at once
	writeJSON(w, http.StatusOK, map[string]string{"status": statusPlaying, "station": stationID})
}

// neverPlays reports TuneIn saying a station has no stream. The speaker would retry it and
// fall silent with no answer for the client. No word from TuneIn is not that.
func (s *Server) neverPlays(ctx context.Context, stationID string) bool {
	if speaker.OwnStream(stationID) {
		return false
	}

	ask, cancel := context.WithTimeout(ctx, stationAskTimeout)
	defer cancel()

	_, err := s.tunein.Resolve(ask, stationID)

	return errors.Is(err, tunein.ErrNoStreams)
}

// stationIDRe matches a TuneIn guide id (e.g. "s6712"). The id is interpolated
// into the ContentItem location and into the upstream Tune.ashx query, so
// anything beyond plain alphanumerics is rejected up front.
var stationIDRe = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

func validStationID(id string) bool { return stationIDRe.MatchString(id) }

// maxStreamURL bounds a stream URL of the user's own: it is stored on NAND in a preset.
const maxStreamURL = 512

// validOwnStream takes what can be played: url.Parse lets a space in the path and any
// number as a port through, and such a URL would stay in its preset, silent.
func validOwnStream(id string) bool {
	if !speaker.OwnStream(id) || len(id) > maxStreamURL || strings.ContainsAny(id, " \t\r\n") {
		return false
	}

	u, err := url.Parse(id)
	if err != nil || u.Hostname() == "" {
		return false
	}

	port := u.Port()
	if port == "" {
		return true
	}

	n, err := strconv.ParseUint(port, 10, 16)

	return err == nil && n > 0
}

// ValidStreamURL reports whether id is a stream URL a preset may hold as its own stream:
// the check a stream address given through the API passes, for one that comes from
// elsewhere (a station looked up for a preset stock left behind).
func ValidStreamURL(id string) bool { return validOwnStream(id) }

// validStation accepts a TuneIn guide id, or a stream URL of the user's own.
func validStation(ctx context.Context, id string) bool {
	if speaker.OwnStream(id) {
		return validOwnStream(id) && upnp.MediaURL(ctx, id)
	}

	return validStationID(id)
}
