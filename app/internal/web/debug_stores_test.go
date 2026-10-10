// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stein155/soundtap/app/internal/recent"
	"github.com/stein155/soundtap/app/internal/web"
)

// A store that opened read-only is visible in the support bundle, one line per store:
// a production speaker has no shell to find it with, and its writes are refused.
func TestDebugBundleReportsEachStore(t *testing.T) {
	dir := t.TempDir()
	// favorites.json and settings.json as directories: EISDIR, so both are read-only.
	for _, name := range []string{"favorites.json", "settings.json"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	recentPath := filepath.Join(dir, "recent.json")
	if err := os.WriteFile(recentPath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, _, _ := newServerAt(t, dir)
	srv.ReconfigureForTest(func(o *web.Options) { o.Speaker.Recent = recent.Open(recentPath) })
	srv.ReconfigureForTest(func(o *web.Options) { o.Health.PresetsLoadErr = func() error { return nil } })

	body := do(t, srv.Handler(), "GET", "/api/debug", "").Body.String()
	for name, want := range map[string]string{
		"settings":  `settings\s+: state file unreadable; writes refused`,
		"favorites": `favorites\s+: state file unreadable; writes refused`,
		"recent":    `recent\s+: corrupt state file moved aside to .*recent\.json\.corrupt`,
		"presets":   `presets\s+: ok\n`,
	} {
		if !regexp.MustCompile(want).MatchString(body) {
			t.Errorf("%s line missing; want %q in the bundle", name, want)
		}
	}

	// And the favorites store really refuses the write the line warns about.
	rec := do(t, srv.Handler(), "POST", "/api/favorites", `{"source":"TUNEIN","name":"x","location":"s1"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("POST /api/favorites over an unreadable file = %d %s, want 500", rec.Code, rec.Body)
	}
	if info, err := os.Stat(filepath.Join(dir, "favorites.json")); err != nil || !info.IsDir() {
		t.Fatalf("favorites path = %v, %v; want the directory untouched", info, err)
	}
}
