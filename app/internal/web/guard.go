// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/speakerstate"
	"github.com/stein155/soundtap/app/internal/stu"
)

func captivePortal(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "http://"+speakerstate.SetupAPAddress+"/?setup=wifi", http.StatusFound)
}

// maxRequestBody caps the body of a mutating request. The JSON/XML we accept is
// tiny (a few settings fields, a station id); anything larger is a mistake or an
// attempt to drive a large allocation on the memory-constrained speaker.
const maxRequestBody = 64 << 10

// guard wraps the API with the only access controls a LAN-only, login-less service
// still needs: it blocks DNS-rebinding (a Host that isn't this speaker) and CSRF (a
// cross-origin mutating request), and bounds request bodies. Without these, a web
// page a LAN user merely visits could drive POST/PUT endpoints — e.g. repoint the
// speaker at an attacker's MQTT broker via PUT /api/settings, or force a downgrade
// via POST /api/update — from a single drive-by.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No page may frame ours: a framed UI under a decoy lets any website the owner
		// visits steer their clicks into the open routes (clickjacking).
		w.Header().Set("X-Frame-Options", "DENY")
		if !hostAllowed(r.Host, s.localNames()) {
			// A rebinding attack reaches us under the attacker's own hostname; a
			// legitimate client uses the speaker's IP or its <name>.local.
			if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") {
				s.wrongHostPage(w, r)
				return
			}
			apiError(w, http.StatusForbidden, "forbidden host")
			return
		}
		// Public, read-only build metadata lets a hosted installer verify first boot
		// without trusting an opaque HTTP response. No other route or method is opened.
		if r.Method == http.MethodGet && r.URL.Path == "/api/version" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if sameSpeakerOrigin(origin, r.Host, s.localNames()) {
			// Vary regardless of the outcome would be more correct for shared caches,
			// but this API is no-store and LAN-only; setting it only when we echo keeps
			// the header off every same-origin response.
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			answerPreflight(w, r, origin, s.localNames())
			return
		}
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			if msg := crossSiteRefusal(r, origin, s.localNames()); msg != "" {
				apiError(w, http.StatusForbidden, msg)
				return
			}
			boundBody(w, r)
		}
		next.ServeHTTP(w, r)
	})
}

// answerPreflight tells a page this speaker served which requests it may send.
func answerPreflight(w http.ResponseWriter, r *http.Request, origin string, names []string) {
	if !sameSpeakerOrigin(origin, r.Host, names) {
		apiError(w, http.StatusForbidden, "cross-origin preflight refused")
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, X-SoundTap-Confirm, "+RequestHeader)
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// crossSiteRefusal says why a mutating request is CSRF, or "" when it is not. Browsers send
// Origin on every cross-origin mutating request (including "simple" text/plain POSTs), so a
// mismatch means CSRF. Sec-Fetch-Site is the belt to Origin's braces on browsers that send it.
func crossSiteRefusal(r *http.Request, origin string, names []string) string {
	if origin != "" {
		if !sameSpeakerOrigin(origin, r.Host, names) {
			return "cross-origin request refused"
		}
		return ""
	}
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
		return "cross-site request refused"
	}
	return ""
}

// bodyLimit is the most a mutating request to path may carry. A proxied upload to another
// speaker needs the same room as a local one, so it goes by the route path.
func bodyLimit(path string) int64 {
	switch path {
	case firmwareUploadPath:
		return stu.MaxSize // a firmware package, streamed and never stored; stu.Scan caps it too
	case soundtapdUpdatePath:
		return soundtapdMaxSize + 1
	case restorePath:
		return backupMaxBytes
	case "/api/speaker/notify":
		return notifyAudioMax // may carry an uploaded audio clip; JSON URL requests stay well under
	}
	return maxRequestBody
}

// bodyReadTimeout is how long a client has to send an ordinary request's body.
var bodyReadTimeout = 30 * time.Second

// uploadReadTimeout is how long an upload may take to arrive; the device proxy waits as
// long for a peer's.
const uploadReadTimeout = 20 * time.Minute

// boundBody caps the request's size and how long it may take: slow uploads are bounded too.
// Firmware is streamed to NAND and legitimately takes minutes; ordinary JSON gets a much
// tighter window. A request held for a key press is read only after the press, and what
// did not arrive with the headers is still on the socket then, so it gets the press
// window on top. A proxied one goes by the route path, like its size.
func boundBody(w http.ResponseWriter, r *http.Request) {
	path := routePath(r.URL.Path)
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit(path))
	deadline := time.Now().Add(bodyReadTimeout)
	if path == firmwareUploadPath || path == soundtapdUpdatePath {
		// A client that stops sending but keeps the connection (a phone tab suspended
		// when the screen locks) must not hold the install lock for the full window.
		rc := http.NewResponseController(w)
		r.Body = &idleBody{ReadCloser: r.Body, rc: rc, end: time.Now().Add(uploadReadTimeout)}
		return
	} else if needsPress(&http.Request{Method: r.Method, URL: &url.URL{Path: path, RawQuery: r.URL.RawQuery}}) {
		deadline = deadline.Add(ConfirmWindow)
	}
	_ = http.NewResponseController(w).SetReadDeadline(deadline)
}

// uploadIdleTimeout is how long an upload may go without a byte before its read ends.
var uploadIdleTimeout = time.Minute

// idleBody moves the read deadline on before every read: uploadIdleTimeout from now, never
// past end.
type idleBody struct {
	io.ReadCloser
	rc  *http.ResponseController
	end time.Time
}

func (b *idleBody) Read(p []byte) (int, error) {
	d := time.Now().Add(uploadIdleTimeout)
	if d.After(b.end) {
		d = b.end
	}
	_ = b.rc.SetReadDeadline(d)
	return b.ReadCloser.Read(p)
}

// sameSpeakerOrigin reports whether origin is a page this speaker itself served, on any
// of its ports. It is what widens "same-origin" to "same-speaker".
//
// This is a narrow widening, not an opening: the origin's host must still satisfy
// hostAllowed (so a real domain is rejected and DNS rebinding stays blocked) AND match
// the host the client used to reach us. Only another port on this same speaker
// qualifies — never another machine on the LAN.
func sameSpeakerOrigin(origin, host string, names []string) bool {
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return false
	}
	if !hostAllowed(u.Host, names) {
		return false
	}
	return strings.EqualFold(hostOnly(u.Host), hostOnly(host))
}

// hostOnly strips the port and any trailing root dot, so "keuken.local.:80" and
// "keuken.local" compare equal.
func hostOnly(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}

// hostAllowed reports whether the request's Host names this speaker: an IP literal,
// localhost (the on-speaker agent), or one of names, the .local names mDNS publishes for
// this speaker. Any other name is rejected, a public domain and a foreign .local alike:
// an attacker on the LAN can answer mDNS for evil.local and rebind it to our IP.
func hostAllowed(hostport string, names []string) bool {
	host := hostOnly(hostport)
	if host == "" {
		return false
	}
	if host == "localhost" || net.ParseIP(host) != nil {
		return true
	}
	for _, n := range names {
		if n != "" && strings.EqualFold(host, strings.TrimSuffix(n, ".")) {
			return true
		}
	}
	return false
}

// wrongHostPage tells a person who opened the speaker under a name it does not answer to
// where it does answer: its IP address and its .local name. It names no page of the
// UI, so a rebinding page learns nothing it could not see by connecting.
func (s *Server) wrongHostPage(w http.ResponseWriter, r *http.Request) {
	var links []string
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if host, _, err := net.SplitHostPort(a.String()); err == nil && net.ParseIP(host).To4() != nil {
			links = append(links, "http://"+host+"/")
		}
	}
	if names := s.localNames(); len(names) > 0 && names[0] != "" {
		links = append(links, "http://"+strings.TrimSuffix(names[0], ".")+"/")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	var b strings.Builder
	b.WriteString("<!doctype html><meta charset=utf-8><meta name=viewport content=\"width=device-width\"><title>SoundTap</title>")
	b.WriteString("<p>This speaker does not answer to this name. Open it at:</p><ul>")
	for _, l := range links {
		b.WriteString("<li><a href=\"" + html.EscapeString(l) + "\">" + html.EscapeString(l) + "</a></li>")
	}
	b.WriteString("</ul>")
	_, _ = io.WriteString(w, b.String())
}
