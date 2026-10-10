// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testNames are the .local names mDNS publishes for the speaker under test.
var testNames = []string{"keuken.local", "soundtap.local"}

type fakeMDNS struct{}

func (fakeMDNS) Hostname() string   { return "keuken.local" }
func (fakeMDNS) SharedName() string { return "soundtap.local" }
func (fakeMDNS) SetName(string)     {}

func TestHostAllowed(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"192.168.1.42:8080", true},
		{"192.168.1.42", true},
		{"127.0.0.1:8000", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{"keuken.local", true},
		{"keuken.local:8080", true},
		{"keuken.local.:8080", true}, // trailing dot
		{"Keuken.LOCAL", true},       // names are case-insensitive
		{"soundtap.local", true},     // the shared name
		{"evil.local", false},        // a .local the attacker answers mDNS for
		{"evil.local:8080", false},
		{"evil.com:8080", false}, // rebinding target
		{"attacker.example", false},
		{"", false},
	}
	for _, c := range cases {
		if got := hostAllowed(c.host, testNames); got != c.want {
			t.Errorf("hostAllowed(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestGuardBlocksRebindingAndCSRF(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &Server{}
	h := srv.guard(ok)

	req := func(method, host, origin, secFetch string) int {
		r := httptest.NewRequest(method, "http://x/api/settings", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if secFetch != "" {
			r.Header.Set("Sec-Fetch-Site", secFetch)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	// GET from the speaker's own address passes.
	if code := req("GET", "192.168.1.42:8080", "", ""); code != http.StatusOK {
		t.Errorf("GET same-host = %d, want 200", code)
	}
	// No other site may frame the UI (clickjacking).
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://192.168.1.42/", nil))
	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	// DNS rebinding: request arrives under the attacker's hostname.
	if code := req("GET", "evil.com", "", ""); code != http.StatusForbidden {
		t.Errorf("GET rebinding host = %d, want 403", code)
	}
	// Rebinding under a .local the attacker answers mDNS for, with or without mDNS up.
	if code := req("PUT", "evil.local", "http://evil.local", ""); code != http.StatusForbidden {
		t.Errorf("PUT foreign .local, no mDNS = %d, want 403", code)
	}
	srv.SetMDNS(fakeMDNS{})
	if code := req("PUT", "evil.local", "http://evil.local", ""); code != http.StatusForbidden {
		t.Errorf("PUT foreign .local = %d, want 403", code)
	}
	if code := req("PUT", "keuken.local", "http://keuken.local", ""); code != http.StatusOK {
		t.Errorf("PUT own .local = %d, want 200", code)
	}
	// CSRF: cross-origin mutating request is refused (Origin mismatch).
	if code := req("PUT", "192.168.1.42:8080", "http://evil.com", ""); code != http.StatusForbidden {
		t.Errorf("PUT cross-origin = %d, want 403", code)
	}
	// Same-origin mutating request passes.
	if code := req("PUT", "192.168.1.42:8080", "http://192.168.1.42:8080", ""); code != http.StatusOK {
		t.Errorf("PUT same-origin = %d, want 200", code)
	}
	// Simple POST with no Origin but a cross-site Sec-Fetch-Site is refused.
	if code := req("POST", "192.168.1.42:8080", "", "cross-site"); code != http.StatusForbidden {
		t.Errorf("POST cross-site = %d, want 403", code)
	}
}

func TestSameSpeakerOrigin(t *testing.T) {
	cases := []struct {
		origin, host string
		want         bool
	}{
		// Another port on the same speaker counts as the same speaker.
		{"http://192.0.2.129", "192.0.2.129:8080", true},
		{"http://192.0.2.129:80", "192.0.2.129:8080", true},
		{"http://keuken.local", "keuken.local:8080", true},
		{"http://keuken.local.", "keuken.local:8080", true},
		{"http://192.0.2.129:8080", "192.0.2.129:8080", true}, // plain same-origin
		// Another machine on the LAN is still cross-origin, port notwithstanding.
		{"http://192.0.2.130", "192.0.2.129:8080", false},
		{"http://keuken.local", "woonkamer.local:8080", false},
		// A foreign .local is refused even when origin and host agree.
		{"http://evil.local", "evil.local:8080", false},
		// A real domain never qualifies, which is what keeps rebinding blocked.
		{"http://evil.com", "192.0.2.129:8080", false},
		{"https://192.0.2.129", "192.0.2.129:8080", false}, // we are http-only
		{"null", "192.0.2.129:8080", false},                // sandboxed iframe
		{"", "192.0.2.129:8080", false},
	}
	for _, c := range cases {
		if got := sameSpeakerOrigin(c.origin, c.host, testNames); got != c.want {
			t.Errorf("sameSpeakerOrigin(%q, %q) = %v, want %v", c.origin, c.host, got, c.want)
		}
	}
}

func TestGuardCORSForUIOnPort80(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &Server{}
	h := srv.guard(ok)

	do := func(method, origin string, acrm string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://x/api/settings", nil)
		r.Host = "192.0.2.129:8080"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if acrm != "" {
			r.Header.Set("Access-Control-Request-Method", acrm)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// Without the echoed header the browser discards the response, so the UI on :80
	// would render empty even though the request itself succeeded.
	res := do(http.MethodGet, "http://192.0.2.129", "")
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "http://192.0.2.129" {
		t.Errorf("GET Allow-Origin = %q, want the speaker's :80 origin", got)
	}
	if got := res.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
	// No credentialed cross-origin request exists: the API carries no cookie or auth
	// header, so advertising credential support would only widen what a same-origin
	// echo permits.
	if got := res.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Allow-Credentials = %q, want it unset", got)
	}

	// A JSON PUT is preflighted; refusing it would break every settings change.
	res = do(http.MethodOptions, "http://192.0.2.129", http.MethodPut)
	if res.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", res.Code)
	}
	if got := res.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("preflight did not advertise allowed methods")
	}

	// Same widening must not reach another host on the LAN.
	res = do(http.MethodOptions, "http://192.0.2.130", http.MethodPut)
	if res.Code != http.StatusForbidden {
		t.Errorf("foreign preflight status = %d, want 403", res.Code)
	}
	if got := do(http.MethodGet, "http://evil.com", "").Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("echoed Allow-Origin for a foreign origin: %q", got)
	}

	// The mutating path must accept the :80 UI now that it is same-speaker.
	if res = do(http.MethodPut, "http://192.0.2.129", ""); res.Code != http.StatusOK {
		t.Errorf("PUT from the :80 UI = %d, want 200", res.Code)
	}
	if res = do(http.MethodPut, "http://192.0.2.130", ""); res.Code != http.StatusForbidden {
		t.Errorf("PUT from another host = %d, want 403", res.Code)
	}
}

// TestGuardProtectsDestructiveEndpoints pins the surface that used to sit behind the
// admin password. With login-less LAN access, the guard is the only thing standing
// between a web page a user merely visits and a speaker that reflashes, factory-resets
// or repoints its Wi-Fi — so every one of these must refuse a cross-origin mutation.
func TestGuardProtectsDestructiveEndpoints(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &Server{}
	h := srv.guard(ok)

	paths := []string{
		"/api/update", "/api/firmware", "/api/reboot", "/api/restore",
		"/api/factory-reset", "/api/wifi", "/api/setup", "/api/backup",
		"/api/debug", "/api/settings", "/api/presets/1", "/api/presets/move",
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			// Cross-origin: an attacker's page driving the speaker with fetch().
			r := httptest.NewRequest(method, "http://x"+path, nil)
			r.Host = "192.168.1.42:8080"
			r.Header.Set("Origin", "http://evil.com")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s cross-origin = %d, want 403", method, path, w.Code)
			}

			// No Origin (a form post or img/iframe navigation) but a cross-site
			// Sec-Fetch-Site, which every current browser sends.
			r = httptest.NewRequest(method, "http://x"+path, nil)
			r.Host = "192.168.1.42:8080"
			r.Header.Set("Sec-Fetch-Site", "cross-site")
			w = httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s cross-site = %d, want 403", method, path, w.Code)
			}

			// Reached under an attacker-controlled hostname (DNS rebinding).
			r = httptest.NewRequest(method, "http://x"+path, nil)
			r.Host = "evil.com"
			w = httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s rebinding host = %d, want 403", method, path, w.Code)
			}
		}
	}
}

// A request held for a key press reads its body after the press. Past the server's
// buffer the rest comes off the socket then, so the read deadline has to cover the
// press window: a restore pressed after 30 s was refused as unreadable.
func TestBodyStaysReadableThroughThePressWindow(t *testing.T) {
	defer func(d time.Duration) { bodyReadTimeout = d }(bodyReadTimeout)
	bodyReadTimeout = 50 * time.Millisecond
	body := strings.Repeat("a", 10<<10) // more than the server's 4 KB read buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		boundBody(w, r)
		time.Sleep(4 * bodyReadTimeout) // the press comes late, but inside the window
		n, err := io.Copy(io.Discard, r.Body)
		fmt.Fprintf(w, "%d %v", n, err)
	}))
	defer srv.Close()
	post := func(path string) string {
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\n\r\n", path, len(body))
		time.Sleep(bodyReadTimeout / 2) // the body after the headers, as a browser may send it
		_, _ = io.WriteString(conn, body)
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		got, _ := io.ReadAll(resp.Body)
		return string(got)
	}
	if got, want := post("/api/restore"), fmt.Sprintf("%d <nil>", len(body)); got != want {
		t.Errorf("press route read %q, want %q", got, want)
	}
	// Every other route keeps the short bound on a slow client.
	if got := post("/api/volume"); !strings.Contains(got, "timeout") {
		t.Errorf("ordinary route read %q, want a timeout", got)
	}
}

// An upload that stops arriving ends after uploadIdleTimeout, not after the whole
// 20-minute window; one that keeps arriving, however slowly, reads to the end.
func TestStalledUploadEndsAfterTheIdleWindow(t *testing.T) {
	defer func(d time.Duration) { uploadIdleTimeout = d }(uploadIdleTimeout)
	uploadIdleTimeout = 100 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		boundBody(w, r)
		n, err := io.Copy(io.Discard, r.Body)
		fmt.Fprintf(w, "%d %v", n, err)
	}))
	defer srv.Close()
	post := func(chunks int, stall bool) string {
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		chunk := strings.Repeat("a", 8<<10)
		fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\n\r\n", firmwareUploadPath, (chunks+1)*len(chunk))
		for range chunks {
			_, _ = io.WriteString(conn, chunk)
			time.Sleep(uploadIdleTimeout / 2) // slow, never idle
		}
		if !stall {
			_, _ = io.WriteString(conn, chunk)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		got, _ := io.ReadAll(resp.Body)
		return string(got)
	}
	if got, want := post(4, false), fmt.Sprintf("%d <nil>", 5*8<<10); got != want {
		t.Errorf("slow upload read %q, want %q", got, want)
	}
	if got := post(1, true); !strings.Contains(got, "timeout") {
		t.Errorf("stalled upload read %q, want a timeout", got)
	}
}

// The names a home router gives the speaker pass; an attacker's still do not.
func TestRouterNames(t *testing.T) {
	resolv := filepath.Join(t.TempDir(), "resolv.conf")
	if err := os.WriteFile(resolv, []byte("search fritz.box home.arpa.\ndomain lan\nnameserver 192.168.1.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	names := append(append([]string{}, testNames...), routerNames("keuken.local", resolv)...)
	for host, want := range map[string]bool{
		"keuken":              true,
		"keuken.fritz.box":    true,
		"KEUKEN.home.arpa:80": true,
		"keuken.lan":          true,
		"keuken.evil.com":     false,
		"evil.fritz.box":      false,
		"keukenx.fritz.box":   false,
		"fritz.box":           false,
	} {
		if got := hostAllowed(host, names); got != want {
			t.Errorf("hostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
	if got := routerNames("keuken.local", filepath.Join(t.TempDir(), "none")); len(got) != 1 || got[0] != "keuken" {
		t.Errorf("without resolv.conf: %v, want only the bare name", got)
	}
}

// A browser under a name the speaker does not answer to gets a page with addresses that
// work; the API keeps its JSON refusal.
func TestWrongHostPage(t *testing.T) {
	h := (&Server{}).guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	get := func(path string) *httptest.ResponseRecorder {
		ctx := context.WithValue(t.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.IPv4(192, 168, 1, 42), Port: 80})
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, "http://evil.example"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	page := get("/")
	if page.Code != http.StatusForbidden || !strings.Contains(page.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(page.Body.String(), "http://192.168.1.42/") {
		t.Errorf("page: %d %q %q", page.Code, page.Header().Get("Content-Type"), page.Body.String())
	}
	if api := get("/api/settings"); api.Code != http.StatusForbidden || !strings.Contains(api.Body.String(), "forbidden host") {
		t.Errorf("api: %d %q", api.Code, api.Body.String())
	}
}

func TestGuardPublicVersionOnly(t *testing.T) {
	for _, tc := range []struct {
		method, path, host string
		wantCORS           bool
		wantStatus         int
	}{
		{"GET", "/api/version", "192.168.1.42", true, 200},
		{"GET", "/api/settings", "192.168.1.42", false, 200},
		{"GET", "/api/version/", "192.168.1.42", false, 200},
		{"GET", "/api/devices/peer/api/version", "192.168.1.42", false, 200},
		{"POST", "/api/version", "192.168.1.42", false, 403},
		{"OPTIONS", "/api/version", "192.168.1.42", false, 403},
		{"GET", "/api/version", "evil.example", false, 403},
	} {
		t.Run(tc.method+tc.path+tc.host, func(t *testing.T) {
			s := &Server{}
			h := s.guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			r := httptest.NewRequestWithContext(t.Context(), tc.method, "http://"+tc.host+tc.path, nil)
			r.Header.Set("Origin", "https://installer.example")
			if tc.method == http.MethodOptions {
				r.Header.Set("Access-Control-Request-Method", "GET")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.wantStatus || (w.Header().Get("Access-Control-Allow-Origin") == "*") != tc.wantCORS {
				t.Fatalf("status=%d headers=%v", w.Code, w.Header())
			}
			if w.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("public version must not permit credentials")
			}
		})
	}
}
