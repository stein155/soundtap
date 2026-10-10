// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Everything under /api/devices/{id}/ is forwarded to that speaker's own API.
//
// We own both ends, so one Access-Control-Allow-Origin header would let the browser call
// each speaker directly and this handler would not exist. The reason to proxy anyway is
// not CORS: it is that .local resolution is the fragile part of this design and is most
// fragile on Android, which only answers one-shot queries, only on 12 and later, and only
// with local-network permission. Through the proxy the phone resolves exactly one name --
// the one the person typed -- and every other speaker is reached by IP from a box already
// on the LAN with no resolver limits at all. Direct browser-to-peer calls would make the
// number of .local lookups grow with the number of speakers, on the platform least able
// to do them.

// proxyMargin is how much longer the proxy waits than the peer's handler, for the hop and
// for the peer to write its answer. Each proxy timeout is the deadline of the handler it
// forwards to plus this, and stays under the web UI's own deadline for that call
// (TestProxyOutlastsThePeerAndEndsBeforeTheUI).
const proxyMargin = 2 * time.Second

// peerTimeout bounds a proxied request end to end. It has to outlast the peer's own
// deadline for the request (playing a station or preset, or a search), so the peer's
// answer reaches the UI rather than our "device unreachable": at 6 s, playing on any
// speaker but the one serving the page failed as "not responding" while it played on
// that one. An unplugged speaker still fails fast: the dial gives up after 2 s, and the
// browser keeps a deadline of its own.
const peerTimeout = max(slowReadDeadline, writeDeadline) + proxyMargin

// pressSlack is what a route the peer holds for a key press gets on top of the
// press window and the peer's own deadline for the work after it.
const pressSlack = 10 * time.Second

// proxyTimeout is peerTimeout, except for routes that legitimately take longer on the
// peer itself: uploads stream for minutes, and a Wi-Fi scan, Spotify or clearing Bluetooth
// run under their own handler deadlines.
func proxyTimeout(route string) time.Duration {
	switch {
	case route == "/api/wifi":
		return ConfirmWindow + wifiJoinDeadline + pressSlack
	case route == "/api/bluetooth/clear":
		return ConfirmWindow + bluetoothClearDeadline + pressSlack
	case route == restorePath, route == "/api/factory-reset":
		return ConfirmWindow + restoreDeadline + pressSlack
	case needsPress(&http.Request{Method: http.MethodPost, URL: &url.URL{Path: route}}) &&
		route != firmwareUploadPath && route != soundtapdUpdatePath && route != coreUpdatePath:
		return ConfirmWindow + pressSlack
	case route == firmwareUploadPath, route == soundtapdUpdatePath,
		route == "/api/speaker/notify":
		return uploadReadTimeout
	case route == coreUpdatePath:
		// The press, then the speaker downloads and writes the package before it answers.
		return ConfirmWindow + uploadReadTimeout + pressSlack
	case route == "/api/wifi/scan":
		return wifiScanDeadline + proxyMargin
	case route == ownerKeyPath:
		// PUT and DELETE wait for the press, then write the environment twice.
		return ConfirmWindow + writeDeadline + pressSlack
	case route == "/api/debug", route == "/api/update":
		// /api/update waits for a press only for a picked release, which the route
		// alone cannot tell; the latest-stable path answers at once anyway.
		return ConfirmWindow + pressSlack
	case strings.HasPrefix(route, "/api/spotify/"):
		return spotifyDeadline + proxyMargin
	case strings.HasPrefix(route, "/api/bluetooth"):
		// Forgetting every paired device takes the longest.
		return bluetoothClearDeadline + proxyMargin
	}
	return peerTimeout
}

// peerTransport is shared, so connections to a speaker are reused across requests and a
// dead peer's dial failure is bounded rather than waiting out the OS default.
// Speaker peers are private addresses, so this deliberately is not SafeTransport, but the
// address actually dialled still has to pass peerIPAllowed: a host name such as "localhost."
// resolves past any check made on the name.
var peerTransport = &http.Transport{
	DialContext:         (&net.Dialer{Timeout: 2 * time.Second, Control: peerDialControl}).DialContext,
	TLSHandshakeTimeout: 2 * time.Second,
	MaxIdleConnsPerHost: 2,
	IdleConnTimeout:     30 * time.Second,
}

// errPeerAddress refuses a dial to an address no peer may have.
var errPeerAddress = errors.New("not a peer address")

// peerDialControl runs after name resolution, on the address about to be dialled.
func peerDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || !peerIPAllowed(net.ParseIP(host)) {
		return fmt.Errorf("%w: %s", errPeerAddress, address)
	}
	return nil
}

// peerAddress returns the base URL for a known device, or "" when the id is unknown, has
// no address yet, has gone stale or said goodbye, or is this speaker itself.
func (s *Server) peerAddress(id string) string {
	s.devices.mu.Lock()
	defer s.devices.mu.Unlock()
	rec := s.devices.by[id]
	// A stale entry's address may belong to another host by now (DHCP reuse).
	// After a goodbye the address may be handed out again before the entry goes stale.
	if rec == nil || rec.device.IP == "" || rec.device.Departure || s.devices.clock().Sub(rec.seen) > deviceStale {
		return ""
	}
	port := rec.device.Port
	if port == 0 {
		port = 80
	}
	return "http://" + net.JoinHostPort(rec.device.IP, strconv.Itoa(int(port)))
}

// deviceProxy forwards /api/devices/{id}/<path> to http://<peer>/<path>.
//
// Only /api/ paths are forwarded. A proxy that will fetch any path from any address in
// its registry is an open relay for anything that can get an entry into that registry,
// and the registry is filled from unauthenticated multicast.
func (s *Server) deviceProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rest := strings.TrimPrefix(r.PathValue("rest"), "/")
	if id == "" || rest == "" {
		apiError(w, http.StatusBadRequest, "device and path required")
		return
	}
	// Dot segments are refused, not cleaned: "api/../x" would pass the prefix check and
	// reach /x on the peer.
	segs := strings.Split(rest, "/")
	if !strings.HasPrefix(rest, "api/") || slices.Contains(segs, "..") || slices.Contains(segs, ".") {
		apiError(w, http.StatusForbidden, "only /api/ paths are proxied")
		return
	}
	// A request through our own id is served here, not forwarded. The UI keeps the chosen
	// speaker per browser and the shared soundtap.local can move to that very speaker: a 400
	// then broke every control of it ("music to Zolder won't start").
	if s.serial != "" && id == s.serial {
		if s.local == nil {
			apiError(w, http.StatusBadRequest, "that is this speaker")
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path, r2.URL.RawPath = "/"+rest, ""
		s.local.ServeHTTP(w, r2)
		return
	}
	base := s.peerAddress(id)
	if base == "" {
		apiError(w, http.StatusNotFound, "unknown device")
		return
	}
	switch err := s.peerVerified(r.Context(), id, base); {
	case errors.Is(err, errNotThatPeer):
		apiError(w, http.StatusNotFound, "unknown device")
		return
	case err != nil:
		s.log.Info("peer unreachable", "device", id, "err", err)
		apiError(w, http.StatusBadGateway, "device unreachable")
		return
	}
	target, err := url.Parse(base)
	if err != nil {
		s.fail(w, "peer address", err)
		return
	}

	// The event stream is meant to stay open; a deadline would cut it every few seconds.
	// The browser closing it still ends it, through r.Context().
	ctx, cancel := context.WithTimeout(r.Context(), proxyTimeout("/"+rest))
	if rest == "api/events" {
		cancel()
		ctx, cancel = context.WithCancel(r.Context())
	}
	defer cancel()
	outbound := r.Clone(ctx)
	// The peer applies its own host and CSRF guard to what arrives. Forwarding this
	// speaker's Origin would make every proxied write look cross-origin to it.
	outbound.Header.Del("Origin")
	outbound.Header.Del("Referer")

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = "/" + rest
			pr.Out.URL.RawQuery = r.URL.RawQuery
			pr.Out.Host = target.Host
		},
		Transport: peerTransport,
		ModifyResponse: func(resp *http.Response) error {
			sandboxForeign(resp.Header)
			if loc := resp.Header.Get("Location"); loc != "" {
				if loc = peerLocation(loc, id, target.Host); loc != "" {
					resp.Header.Set("Location", loc)
				} else {
					resp.Header.Del("Location")
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			// A speaker that is unplugged, rebooting or re-flashing is the normal case
			// here, not an error worth a 500: the UI shows it offline and moves on.
			s.log.Info("peer unreachable", "device", id, "err", err)
			apiError(w, http.StatusBadGateway, "device unreachable")
		},
	}
	proxy.ServeHTTP(w, outbound)
}

// errNotThatPeer is an address that answered, but not as the speaker it was announced as.
var errNotThatPeer = errors.New("address does not answer as that speaker")

// peerVerified checks, once per address, that base is the speaker id before the proxy
// forwards anything there. The registry is filled from mDNS, which any LAN host can
// answer, so without this an entry could aim the proxy at any private host and port: a
// router's or NAS's admin pages.
func (s *Server) peerVerified(ctx context.Context, id, base string) error {
	s.devices.mu.Lock()
	rec := s.devices.by[id]
	done := rec != nil && rec.verified == base
	s.devices.mu.Unlock()
	if done {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, peerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/info", nil)
	if err != nil {
		return err
	}
	resp, err := peerTransport.RoundTrip(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var info struct {
		ID string `json:"id"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, maxRequestBody)).Decode(&info) != nil || info.ID != id {
		return errNotThatPeer
	}
	s.devices.mu.Lock()
	if rec := s.devices.by[id]; rec != nil {
		rec.verified = base
	}
	s.devices.mu.Unlock()
	return nil
}

// peerLocation keeps a peer's redirect on that peer: its own paths come back under
// /api/devices/{id}/, so the browser does not land on this speaker. A redirect anywhere
// else is not followed through the proxy: the browser gets the status without a Location.
// A relative one already resolves under the proxied path.
func peerLocation(loc, id, peerHost string) string {
	u, err := url.Parse(loc)
	if err != nil || u.Opaque != "" {
		return ""
	}
	if u.Scheme != "" || u.Host != "" {
		if (u.Scheme != "http" && u.Scheme != "") || u.Host != peerHost {
			return ""
		}
	}
	if u.Scheme == "" && u.Host == "" && !strings.HasPrefix(u.Path, "/") {
		return loc // relative to the proxied path, so it stays on the peer already
	}
	out := url.URL{Path: "/api/devices/" + id + u.Path, RawQuery: u.RawQuery, Fragment: u.Fragment}
	return out.String()
}
