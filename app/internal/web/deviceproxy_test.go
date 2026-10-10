// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// quiet is the logger these tests hand a bare Server. web.New always sets one; nothing
// here goes through it.
func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// proxyTo builds a Server that knows one peer, backed by a test server standing in for
// that speaker's own soundtapd.
func proxyTo(t *testing.T, peer http.Handler) (*Server, string) {
	t.Helper()
	backend := httptest.NewServer(peer)
	t.Cleanup(backend.Close)
	host, port, ok := strings.Cut(strings.TrimPrefix(backend.URL, "http://"), ":")
	if !ok {
		t.Fatalf("unexpected test server URL %q", backend.URL)
	}
	var s Server
	s.log = quiet()
	s.SetDevice(Device{ID: "SN-keuken", Name: "Keuken", IP: host, Port: atoiPort(t, port)})
	s.devices.by["SN-keuken"].verified = backend.URL // TestProxyForwardsOnlyToTheAnnouncedSpeaker checks the check
	return &s, "SN-keuken"
}

// An mDNS entry may name any private address. The proxy forwards there only once that
// address answered /api/info as the announced speaker, so a LAN host cannot aim it at a
// router's admin pages.
func TestProxyForwardsOnlyToTheAnnouncedSpeaker(t *testing.T) {
	for _, tc := range []struct {
		answer string
		want   int
	}{
		{`{"id":"SN-keuken"}`, http.StatusOK},
		{`{"id":"SN-other"}`, http.StatusNotFound},
		{`<html>router login</html>`, http.StatusNotFound},
	} {
		var hits int
		s, id := proxyTo(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/info" {
				_, _ = io.WriteString(w, tc.answer)
				return
			}
			hits++
		}))
		s.devices.by[id].verified = ""
		rec := proxyRequest(s, "GET", "/api/devices/"+id+"/api/volume", "")
		if rec.Code != tc.want {
			t.Errorf("peer answering %s: status %d, want %d", tc.answer, rec.Code, tc.want)
		}
		if wantHits := map[bool]int{true: 1, false: 0}[tc.want == http.StatusOK]; hits != wantHits {
			t.Errorf("peer answering %s: %d forwarded requests, want %d", tc.answer, hits, wantHits)
		}
	}
}

func atoiPort(t *testing.T, port string) uint16 {
	t.Helper()
	var n uint16
	for _, c := range port {
		if c < '0' || c > '9' {
			t.Fatalf("bad port %q", port)
		}
		n = n*10 + uint16(c-'0')
	}
	return n
}

func proxyRequest(s *Server, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.SetPathValue("id", pathID(target))
	req.SetPathValue("rest", pathRest(target))
	rec := httptest.NewRecorder()
	s.deviceProxy(rec, req)
	return rec
}

// The mux fills these in production; the handler is exercised directly here so the test
// does not depend on the rest of the routing table.
func pathID(target string) string {
	rest := strings.TrimPrefix(target, "/api/devices/")
	id, _, _ := strings.Cut(rest, "/")
	return id
}

func pathRest(target string) string {
	rest := strings.TrimPrefix(target, "/api/devices/")
	_, after, _ := strings.Cut(rest, "/")
	if q := strings.IndexByte(after, '?'); q >= 0 {
		after = after[:q]
	}
	return after
}

func TestProxyReachesThePeersOwnAPI(t *testing.T) {
	var gotPath, gotQuery, gotOrigin string
	s, id := proxyTo(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotOrigin = r.URL.Path, r.URL.RawQuery, r.Header.Get("Origin")
		_, _ = io.WriteString(w, `{"volume":42}`)
	}))
	rec := proxyRequest(s, "GET", "/api/devices/"+id+"/api/volume?x=1", "")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/api/volume" {
		t.Fatalf("peer saw path %q, want /api/volume", gotPath)
	}
	if gotQuery != "x=1" {
		t.Fatalf("peer saw query %q, want x=1", gotQuery)
	}
	// The peer runs the same host and CSRF guard we do. Forwarding our Origin would make
	// every proxied write look cross-origin to it.
	if gotOrigin != "" {
		t.Fatalf("peer saw Origin %q, want it stripped", gotOrigin)
	}
	if rec.Body.String() != `{"volume":42}` {
		t.Fatalf("body %q", rec.Body.String())
	}
}

// The registry is filled from unauthenticated multicast. A proxy that will fetch any path
// from any address in it is an open relay for anything that can get an entry in.
func TestProxyForwardsOnlyAPIPaths(t *testing.T) {
	s, id := proxyTo(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("peer was reached for a non-API path")
		w.WriteHeader(500)
	}))
	for _, path := range []string{"etc/passwd", "index.html", "apifoo/bar", "api/../etc/passwd", "api/./x/../../y"} {
		rec := proxyRequest(s, "GET", "/api/devices/"+id+"/"+path, "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status %d, want 403", path, rec.Code)
		}
	}
}

func TestProxyToAnUnknownDeviceIs404(t *testing.T) {
	s, _ := proxyTo(t, http.NotFoundHandler())
	rec := proxyRequest(s, "GET", "/api/devices/SN-nobody/api/info", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// A speaker that is unplugged, rebooting or re-flashing is the normal case, not a 500:
// the UI has to be able to say "offline" and keep working.
func TestProxyToAnUnreachablePeerIs502(t *testing.T) {
	var s Server
	s.log = quiet()
	s.SetDevice(Device{ID: "SN-gone", Name: "Zolder", IP: "127.0.0.1", Port: 9})
	rec := proxyRequest(&s, "GET", "/api/devices/SN-gone/api/info", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", rec.Code, rec.Body.String())
	}
}

func TestProxyToItselfIsServedHere(t *testing.T) {
	var s Server
	s.log = quiet()
	s.ReconfigureForTest(func(o *Options) { o.Speaker.Serial = "SN-self" })
	var got string
	s.local = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method + " " + r.URL.Path
		w.WriteHeader(http.StatusTeapot)
	})
	rec := proxyRequest(&s, "POST", "/api/devices/SN-self/api/play/1", "")
	if rec.Code != http.StatusTeapot || got != "POST /api/play/1" {
		t.Fatalf("status %d, local saw %q; want the local route table to serve POST /api/play/1", rec.Code, got)
	}
}

// A peer comes from unauthenticated multicast and can answer text/html; opened through
// this speaker, that page must not run on this speaker's origin.
func TestProxySandboxesThePeersAnswer(t *testing.T) {
	s, id := proxyTo(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<script>fetch("/api/power")</script>`)
	}))
	rec := proxyRequest(s, "GET", "/api/devices/"+id+"/api/x", "")
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("Content-Security-Policy = %q, want a sandbox", csp)
	}
}

// Anyone on the LAN can advertise a peer over mDNS; only a private address may become one,
// or the proxy would relay the browser's requests to any host on the internet.
func TestPeersArePrivateAddressesOnly(t *testing.T) {
	for ip, want := range map[string]bool{
		"192.168.1.5": true, "10.0.0.2": true, "172.16.4.1": true, "fd00::5": true,
		"198.51.100.10": false, "8.8.8.8": false, "100.64.0.1": false, "2001:db8::1": false,
		"127.0.0.1": false, "169.254.1.1": false, "224.0.0.251": false, "0.0.0.0": false,
	} {
		if got := strictPeerIP(net.ParseIP(ip)); got != want {
			t.Errorf("%s allowed = %v, want %v", ip, got, want)
		}
	}
	var s Server
	s.log = quiet()
	s.SetDevice(Device{ID: "SN-far", Name: "Far", IP: "198.51.100.10", Port: 80})
	if rec := proxyRequest(&s, "GET", "/api/devices/SN-far/api/info", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("public peer registered: %d", rec.Code)
	}
}
