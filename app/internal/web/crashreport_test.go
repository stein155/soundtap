// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/web"
)

type crashAnswer struct {
	Enabled bool          `json:"enabled"`
	Crash   *offeredCrash `json:"crash"`
}

// offeredCrash is the crash GET /api/crash-report offers.
type offeredCrash struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

func getCrash(t *testing.T, h http.Handler) crashAnswer {
	t.Helper()

	rec := do(t, h, "GET", "/api/crash-report", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/crash-report: %d %s", rec.Code, rec.Body)
	}

	var got crashAnswer
	decodeBody(t, rec, &got)

	return got
}

// Off unless the owner turns it on: a speaker with a crash on it offers nothing, and does
// not even look (ADR 0126).
func TestCrashReportIsOffByDefault(t *testing.T) {
	t.Parallel()

	srv, _, home := newServerSrv(t)

	err := os.WriteFile(filepath.Join(home, web.PanicLogName), []byte("panic: boom\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	handler := srv.Handler()
	if got := getCrash(t, handler); got.Enabled || got.Crash != nil {
		t.Fatalf("with reports off: %+v", got)
	}

	var settings map[string]any
	decodeBody(t, do(t, handler, "GET", "/api/settings", ""), &settings)

	if !isFalse(settings["crashReports"]) {
		t.Errorf("GET /api/settings crashReports = %v, want false", settings["crashReports"])
	}
}

// The report leaves the speaker in a public issue. Built from a speaker whose every file
// holds secrets, neither its title, its body nor its link carries one; each crash is
// offered once, in order, until it is reported or dismissed.
//
//nolint:paralleltest // it points the package-level system root elsewhere
func TestCrashReportCarriesNoSecretsAndIsOfferedOnce(t *testing.T) {
	root := t.TempDir()
	home := seedSpeaker(t, root)
	t.Cleanup(web.SetSystemRootForTest(root))
	t.Cleanup(counters.Reset)
	srv, _, _ := newServerAt(t, home)

	handler := srv.Handler()
	if rec := do(t, handler, "PUT", "/api/settings", `{"crashReports":true}`); rec.Code != http.StatusOK {
		t.Fatalf("turn reports on: %d %s", rec.Code, rec.Body)
	}

	plain := []string{
		homeSSID, homeKey, atticSSID, atticKey, cafeSSID, wpaPMK, knownHex, neighbour,
		mqttPass, spotifyUser, spotifyBlob, confirmTok, household, bearer, apiKey, streamPass,
		publicIPv4, publicIPv6,
	}
	encoded := []string{homeSSID, homeKey, atticSSID, atticKey, cafeSSID}

	secrets := make([]string, 0, len(plain)+2*len(encoded))
	secrets = append(secrets, plain...)

	for _, s := range encoded {
		secrets = append(secrets, b64(s), hex.EncodeToString([]byte(s)))
	}

	check := func(kind string) string {
		t.Helper()

		got := getCrash(t, handler)
		if !got.Enabled || got.Crash == nil || got.Crash.Kind != kind {
			t.Fatalf("want a %s crash, got %+v", kind, got)
		}

		found := got.Crash

		link, err := url.Parse(found.URL)
		if err != nil || link.Scheme+"://"+link.Host+link.Path != "https://github.com/stein155/soundtap/issues/new" {
			t.Fatalf("link %q", found.URL)
		}

		if len(found.URL) > 6000 {
			t.Errorf("link is %d bytes", len(found.URL))
		}
		// The preview is the exact text the link carries.
		if q := link.Query(); q.Get("title") != found.Title || q.Get("body") != found.Body {
			t.Error("the link carries other text than the preview shows")
		}

		for _, field := range []string{found.Title, found.Body, found.URL} {
			for _, secret := range secrets {
				if strings.Contains(field, secret) || strings.Contains(field, url.QueryEscape(secret)) {
					t.Errorf("%s report carries the secret %q", kind, secret)
				}
			}
		}

		for _, want := range []string{
			"**What happened:**", "- SoundTap app: test", "- Model: ", "- Uptime: speaker ",
			"- Crash id: " + found.ID, "**Support bundle:**", "sent nothing by itself",
		} {
			if !strings.Contains(found.Body, want) {
				t.Errorf("%s report lacks %q:\n%s", kind, want, found.Body)
			}
		}
		// Redacted, not dropped: the trace is still there to read.
		if !strings.Contains(found.Body, "plain line kept") && kind != "daemon" {
			t.Errorf("%s report lost the trace:\n%s", kind, found.Body)
		}

		return found.ID
	}

	panicID := check("panic")

	rec := do(t, handler, "POST", "/api/crash-report", `{"id":"`+panicID+`","outcome":"dismissed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}

	kernelID := check("kernel")

	rec = do(t, handler, "POST", "/api/crash-report", `{"id":"`+kernelID+`","outcome":"reported"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reported: %d %s", rec.Code, rec.Body)
	}

	if got := getCrash(t, handler); got.Crash != nil {
		t.Fatalf("a handled crash offered again: %+v", got.Crash)
	}

	// A media daemon that crash-loops is the next one.
	counters.DaemonCrashLooped("librespot", 20, "exit status 1", time.Now())

	daemonID := check("daemon")

	rec = do(t, handler, "POST", "/api/crash-report", `{"id":"`+daemonID+`","outcome":"dismissed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss daemon: %d %s", rec.Code, rec.Body)
	}

	// After a restart of soundtapd the handled crashes stay handled.
	again, _, _ := newServerAt(t, home)
	if got := getCrash(t, again.Handler()); got.Crash != nil {
		t.Fatalf("after a restart a handled crash is offered again: %+v", got.Crash)
	}
	// A new panic is a new crash.
	err := os.WriteFile(
		filepath.Join(home, web.PanicLogName),
		[]byte("2026-11-21T10:00:00Z goroutine \"audio\": panic: other\n\nstack"),
		0o600,
	)
	if err != nil {
		t.Fatal(err)
	}

	if got := getCrash(t, again.Handler()); got.Crash == nil || got.Crash.ID == panicID ||
		got.Crash.Title != "Crash: soundtapd panic: other" {
		t.Fatalf("a new panic: %+v", got.Crash)
	}
}

func TestCrashReportRefusesWhatIsNotAnOfferedCrash(t *testing.T) {
	t.Parallel()

	srv, _, home := newServerSrv(t)

	err := os.WriteFile(filepath.Join(home, web.PanicLogName), []byte("panic: boom\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	handler := srv.Handler()
	for body, want := range map[string]int{
		`{"id":"panic-000000000000","outcome":"dismissed"}`: http.StatusNotFound,
		`{"id":"panic-000000000000","outcome":"maybe"}`:     http.StatusBadRequest,
		`{"id":"panic-000000000000"}`:                       http.StatusBadRequest,
		`{"id":"x","outcome":"dismissed","extra":1}`:        http.StatusBadRequest,
	} {
		if rec := do(t, handler, "POST", "/api/crash-report", body); rec.Code != want {
			t.Errorf("%s: %d, want %d", body, rec.Code, want)
		}
	}
}
