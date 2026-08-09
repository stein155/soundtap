// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/release"
)

// signApp is the .sig sign.sh writes for an app binary, with a throwaway key.
func signApp(t *testing.T, priv ed25519.PrivateKey, profile, version string, bin []byte) string {
	t.Helper()
	digest := sha256.Sum256(bin)
	sig, err := release.Sign(priv, release.Statement{Kind: release.KindApp, Profile: profile, Version: version, Digest: digest[:]})
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

// testManager runs on a clock NTP has set; TestReleaseCheckWaitsForTheClock takes it away.
func testManager(t *testing.T) *Manager {
	t.Helper()
	return syncedManager("app-v0.1.0")
}

func syncedManager(version string) *Manager {
	m := New(version, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.clockSynced = func() bool { return true }
	return m
}

// fakeApp is the install step POST /api/soundtapd ends in.
type fakeApp struct {
	staging   string
	installed []byte
	err       error
}

func (a *fakeApp) StagingPath() string { return a.staging }
func (a *fakeApp) InstallApp(path string) error {
	a.installed, _ = os.ReadFile(path)
	return a.err
}

// github serves a release list and the files under releases/download.
type github map[string]string

func (g github) RoundTrip(r *http.Request) (*http.Response, error) {
	key := r.URL.Path
	if strings.HasSuffix(key, "/releases") {
		key = "releases"
	}
	body, ok := g[key]
	code := 200
	if !ok {
		code = 404
	}
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func TestNotUpdatableWithoutFirmwareAndInstaller(t *testing.T) {
	m := testManager(t)
	m.SetAppInstaller(&fakeApp{})
	if m.Mode() != ModeUnmanaged || m.Updatable() {
		t.Fatal("a dev run must not be updatable")
	}
	if _, _, err := m.Start(context.Background(), ""); !errors.Is(err, ErrNotUpdatable) {
		t.Fatalf("Start off a speaker: %v, want ErrNotUpdatable", err)
	}
	// UpdateInfo off-speaker reports latest == installed without a GitHub call.
	installed, latest, url, updatable, err := m.UpdateInfo(context.Background())
	if err != nil || updatable || latest != installed || url != "" {
		t.Fatalf("UpdateInfo = (%q, %q, %q, %v, %v)", installed, latest, url, updatable, err)
	}

	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	m = testManager(t)
	if m.Mode() != ModeFirmware || m.Updatable() {
		t.Fatal("firmware without an install step must not be updatable")
	}
	m.SetAppInstaller(&fakeApp{})
	if !m.Updatable() {
		t.Fatal("firmware with the install step is not updatable")
	}
}

func TestStartBusy(t *testing.T) {
	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	m := testManager(t)
	m.SetAppInstaller(&fakeApp{})
	m.mu.Lock() // simulate an install in flight
	defer m.mu.Unlock()
	if _, _, err := m.Start(context.Background(), "app-v9.9.9"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Start while locked: %v, want ErrBusy", err)
	}
}

// OS releases carry an STU, which /api/firmware installs: the app updater lists and picks
// only app-v* releases, even when an OS release is the newest.
func TestReleasesAreAppReleasesOnly(t *testing.T) {
	m := testManager(t)
	m.client = &http.Client{Transport: github{"releases": `[{"tag_name":"os-v0.6.0"},{"tag_name":"app-v0.5.1","prerelease":true},` +
		`{"tag_name":"app-v0.5.0"},{"tag_name":"candidate-abc","draft":true},{"tag_name":"app-v0.4.0"}]`}}
	latest, err := m.Latest(context.Background())
	if err != nil || latest != "app-v0.5.0" {
		t.Fatalf("Latest = %q, %v; want app-v0.5.0", latest, err)
	}
	rels, _ := m.Releases(context.Background())
	if len(rels) != 3 {
		t.Fatalf("Releases = %+v", rels)
	}
}

func TestStartInstallsTheSignedAppRelease(t *testing.T) {
	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	pub, priv, _ := ed25519.GenerateKey(nil)
	defer func(k []string) { releaseKeys = k }(releaseKeys)
	releaseKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	bin := []byte("new app")
	asset := "/" + Repo + "/releases/download/app-v0.2.0/SoundTap-app-0.2.0-armv7.bin"
	gh := github{
		"releases":     `[{"tag_name":"app-v0.2.0"}]`,
		asset:          string(bin),
		asset + ".sig": signApp(t, priv, release.ProfileProduction, "0.2.0", bin),
	}
	app := &fakeApp{staging: filepath.Join(t.TempDir(), "soundtapd.new")}
	m := testManager(t)
	m.client, m.download = &http.Client{Transport: gh}, &http.Client{Transport: gh}
	m.SetAppInstaller(app)
	if err := m.installRelease(context.Background(), "app-v0.2.0", false); err != nil {
		t.Fatal(err)
	}
	if string(app.installed) != "new app" {
		t.Fatalf("installed %q", app.installed)
	}

	gh[asset] = "tampered"
	app.installed = nil
	if err := m.installRelease(context.Background(), "app-v0.2.0", false); err == nil || app.installed != nil {
		t.Fatalf("a tampered download reached the install step: %v", err)
	}
	if _, err := os.Stat(app.staging); !os.IsNotExist(err) {
		t.Fatal("the refused download was left in the staging path")
	}
	if err := m.installRelease(context.Background(), "os-v1.0.0", true); err == nil {
		t.Fatal("an OS tag was treated as an app release")
	}
}

// The .sig of an app release names that release: kind app and the tag's version. A
// genuine signature over the same bytes as another version, or as a Core package, is
// not this release.
func TestInstallReleaseNeedsTheTagsStatement(t *testing.T) {
	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	pub, priv, _ := ed25519.GenerateKey(nil)
	defer func(k []string) { releaseKeys = k }(releaseKeys)
	releaseKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	bin := []byte("new app")
	digest := sha256.Sum256(bin)
	asset := "/" + Repo + "/releases/download/app-v0.2.0/SoundTap-app-0.2.0-armv7.bin"
	core, _ := release.Sign(priv, release.Statement{Kind: release.KindCore, Profile: release.ProfileProduction, Version: "0.2.0", Digest: digest[:]})
	full := signApp(t, priv, release.ProfileProduction, "0.2.0", bin)
	for name, sig := range map[string]string{
		"older version":  signApp(t, priv, release.ProfileProduction, "0.1.0", bin),
		"newer version":  signApp(t, priv, release.ProfileProduction, "0.3.0", bin),
		"signed as Core": core,
		"truncated":      full[:strings.LastIndex(full, " sig=")],
	} {
		gh := github{"releases": `[{"tag_name":"app-v0.2.0"}]`, asset: string(bin), asset + ".sig": sig}
		app := &fakeApp{staging: filepath.Join(t.TempDir(), "soundtapd.new")}
		m := testManager(t)
		m.client, m.download = &http.Client{Transport: gh}, &http.Client{Transport: gh}
		m.SetAppInstaller(app)
		// Even with the press that lets an older release in.
		if err := m.installRelease(context.Background(), "app-v0.2.0", true); err == nil || app.installed != nil {
			t.Errorf("%s: installed (%v)", name, err)
		}
		if _, err := os.Stat(app.staging); !os.IsNotExist(err) {
			t.Errorf("%s: the refused download was left in the staging path", name)
		}
	}
}

// The app lane's policy (ADR 0048): only production releases on every image, and an
// older or the same app only through a named tag, which waits for the press.
func TestAdmitApp(t *testing.T) {
	digest := sha256.Sum256([]byte("app"))
	st := func(profile, version string) release.Statement {
		return release.Statement{Kind: release.KindApp, Profile: profile, Version: version, Digest: digest[:]}
	}
	m := New("app-v0.3.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, c := range []struct {
		name       string
		st         release.Statement
		tag        string
		allowOlder bool
		ok         bool
	}{
		{"newer", st(release.ProfileProduction, "0.4.0"), "app-v0.4.0", false, true},
		{"older without the press", st(release.ProfileProduction, "0.2.0"), "app-v0.2.0", false, false},
		{"same without the press", st(release.ProfileProduction, "0.3.0"), "app-v0.3.0", false, false},
		{"older with the press", st(release.ProfileProduction, "0.2.0"), "app-v0.2.0", true, true},
		{"alpha", st(release.ProfileAlpha, "0.4.0"), "app-v0.4.0", false, false},
		{"alpha with the press", st(release.ProfileAlpha, "0.4.0"), "app-v0.4.0", true, false},
		{"version is not the tag", st(release.ProfileProduction, "0.4.0"), "app-v0.5.0", true, false},
	} {
		if err := m.admitApp(c.st, c.tag, c.allowOlder); (err == nil) != c.ok {
			t.Errorf("%s: admitApp = %v, want ok %v", c.name, err, c.ok)
		}
	}
}

// A self-update binary installs only with a valid <asset>.sig, an app statement over its
// digest; no tag is exempt.
func TestVerifySignedNeedsTheReleaseKey(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	defer func(k []string) { releaseKeys = k }(releaseKeys)
	releaseKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	dir := t.TempDir()
	bin, sig := filepath.Join(dir, "soundtap.new"), filepath.Join(dir, "soundtap.new.sig")
	data := []byte("binary")
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(sig, []byte(signApp(t, priv, release.ProfileProduction, "0.2.0", data)+"\n"), 0o644)
	if st, err := verifySigned(bin, sig); err != nil || st.Version != "0.2.0" {
		t.Fatalf("valid signature refused: %v, %v", st, err)
	}
	// A Core statement over the same bytes is not an app release.
	digest := sha256.Sum256(data)
	core, _ := release.Sign(priv, release.Statement{Kind: release.KindCore, Profile: release.ProfileProduction, Version: "0.2.0", Digest: digest[:]})
	_ = os.WriteFile(sig, []byte(core), 0o644)
	if _, err := verifySigned(bin, sig); err == nil {
		t.Fatal("a core statement passed as an app release")
	}
	// The digest-only form of before 0.1.0 is not accepted.
	_ = os.WriteFile(sig, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, digest[:]))), 0o644)
	if _, err := verifySigned(bin, sig); err == nil {
		t.Fatal("a digest-only signature passed")
	}
	_ = os.WriteFile(sig, []byte(signApp(t, priv, release.ProfileProduction, "0.2.0", data)), 0o644)
	_ = os.WriteFile(bin, []byte("tampered"), 0o755)
	if _, err := verifySigned(bin, sig); err == nil {
		t.Fatal("tampered binary accepted")
	}
}

// A tagless update picks the highest stable app by version, not GitHub's list order, and
// never installs one that is not newer than the running app: an older app goes in
// only by naming its tag, which waits for a press (ADR 0118).
func TestLatestNeverDowngrades(t *testing.T) {
	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	// A maintenance release of an older line, published after the newer one.
	rels := `[{"tag_name":"app-v1.1.9"},{"tag_name":"app-v1.10.0","prerelease":true},{"tag_name":"app-v1.2.0"},{"tag_name":"app-v1.0.0"}]`
	for _, c := range []struct{ running, want string }{
		{"app-v1.2.0", "app-v1.2.0"}, // already current, though 1.1.9 is listed first
		{"app-v1.3.0", "app-v1.3.0"}, // a Core image bundles a newer app than any release
	} {
		app := &fakeApp{staging: filepath.Join(t.TempDir(), "soundtapd.new")}
		m := syncedManager(c.running)
		m.client = &http.Client{Transport: github{"releases": rels}}
		m.SetAppInstaller(app)
		if latest, err := m.Latest(context.Background()); err != nil || latest != "app-v1.2.0" {
			t.Fatalf("Latest = %q, %v; want app-v1.2.0", latest, err)
		}
		target, current, err := m.Start(context.Background(), "")
		if err != nil || !current || target != c.want {
			t.Fatalf("running %s: Start = (%q, %v, %v), want (%q, true, nil)", c.running, target, current, err, c.want)
		}
		if !m.mu.TryLock() {
			t.Fatalf("running %s: an install was started", c.running)
		}
		m.mu.Unlock()
		if _, latest, url, _, _ := m.UpdateInfo(context.Background()); latest != c.running || url != "" {
			t.Fatalf("running %s: UpdateInfo offers %q (%q)", c.running, latest, url)
		}
	}
	if !newer("app-v1.2.0", "app-v1.1.9") || newer("app-v1.1.9", "app-v1.2.0") || !newer("app-v1.10.0", "app-v1.9.0") ||
		!newer("app-v1.0.0", "dev") || newer("beta-pr-1", "app-v1.0.0") {
		t.Fatal("newer compares versions wrongly")
	}
}

// The Core lane: the highest stable os-v release, offered only when newer than the
// running image, from the same cached list as the app lane.
func TestLatestCore(t *testing.T) {
	rels := `[{"tag_name":"os-v0.1.9"},{"tag_name":"os-v0.3.0","prerelease":true},{"tag_name":"os-v0.4.0","draft":true},` +
		`{"tag_name":"app-v9.0.0"},{"tag_name":"os-v0.2.0"},{"tag_name":"os-vbeta"},{"tag_name":"os-v0.1.10"}]`
	calls := 0
	gh := github{"releases": rels}
	m := testManager(t)
	m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return gh.RoundTrip(r)
	})}
	for _, c := range []struct{ running, want string }{
		{"0.1.0", "os-v0.2.0"},
		{"0.1.10", "os-v0.2.0"},
		{"0.2.0", ""}, // current
		{"0.2.1", ""}, // a newer image than any release
		{"dev", ""},   // a development image has no line to follow
		{" 0.1.9\n", "os-v0.2.0"},
	} {
		rel, err := m.LatestCore(context.Background(), c.running)
		got := ""
		if rel != nil {
			got = rel.TagName
		}
		if err != nil || got != c.want {
			t.Fatalf("running %q: LatestCore = %q, %v; want %q", c.running, got, err, c.want)
		}
	}
	if _, err := m.Releases(context.Background()); err != nil || calls != 1 {
		t.Fatalf("the app and Core lanes asked GitHub %d times (%v), want once", calls, err)
	}
}

// ADR 0035: a speaker on a release is never offered a prerelease, on either lane; a speaker
// on an alpha is offered newer alphas and releases, the highest first. A prerelease is an
// X.Y.Z-alpha.N tag or one GitHub marks as a prerelease.
func TestPrereleasesOnlyForAlphaSpeakers(t *testing.T) {
	list := func(lane string, tags ...string) string {
		var out []string
		for _, tag := range tags {
			pre, _ := strings.CutSuffix(tag, "*") // * marks GitHub's prerelease flag
			out = append(out, fmt.Sprintf(`{"tag_name":"%s-v%s","prerelease":%v}`, lane, pre, pre != tag))
		}
		return "[" + strings.Join(out, ",") + "]"
	}
	for _, c := range []struct {
		name, running string
		tags          []string
		want          string // the newer version offered, "" for none
	}{
		{"release: no alpha", "0.1.0", []string{"0.2.0-alpha.1*", "0.1.0", "0.1.0-alpha.2*"}, ""},
		{"release: the next release", "0.1.0", []string{"0.2.0-alpha.1*", "0.1.1", "0.1.0"}, "0.1.1"},
		{"release: an alpha GitHub does not flag", "0.1.0", []string{"0.2.0-alpha.1", "0.1.0"}, ""},
		{"release: a flagged release number", "0.1.0", []string{"0.2.0*", "0.1.0"}, ""},
		{"older release: no alpha", "0.0.9", []string{"0.1.0-alpha.2*", "0.1.0-alpha.1*"}, ""},
		{"alpha: the next alpha", "0.1.0-alpha.1", []string{"0.1.0-alpha.2*", "0.1.0-alpha.1*"}, "0.1.0-alpha.2"},
		{"alpha: the release after it", "0.1.0-alpha.2", []string{"0.1.0", "0.1.0-alpha.2*", "0.1.0-alpha.1*"}, "0.1.0"},
		{"alpha: the highest, alpha or release", "0.1.0-alpha.1", []string{"0.2.0-alpha.1*", "0.1.0", "0.1.0-alpha.2*"}, "0.2.0-alpha.1"},
		{"alpha: the release over its alphas", "0.1.0-alpha.1", []string{"0.1.0-alpha.3*", "0.1.0", "0.1.0-alpha.2*"}, "0.1.0"},
		{"alpha: nothing newer", "0.1.0-alpha.2", []string{"0.1.0-alpha.2*", "0.1.0-alpha.1*"}, ""},
	} {
		// The app: what the Updates page and Home Assistant offer, the running app when
		// nothing newer is.
		t.Setenv("SOUNDTAP_FIRMWARE", "1")
		m := syncedManager("app-v" + c.running)
		m.client = &http.Client{Transport: github{"releases": list("app", c.tags...)}}
		m.SetAppInstaller(&fakeApp{staging: filepath.Join(t.TempDir(), "soundtapd.new")})
		want := "app-v" + c.running
		if c.want != "" {
			want = "app-v" + c.want
		}
		if _, latest, _, _, err := m.UpdateInfo(context.Background()); err != nil || latest != want {
			t.Errorf("%s: app UpdateInfo offers %q, %v; want %q", c.name, latest, err, want)
		}
		m = syncedManager("dev")
		m.client = &http.Client{Transport: github{"releases": list("os", c.tags...)}}
		rel, err := m.LatestCore(context.Background(), c.running)
		got := ""
		if rel != nil {
			got = strings.TrimPrefix(rel.TagName, "os-v")
		}
		if err != nil || got != c.want {
			t.Errorf("%s: LatestCore = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

// A release whose notes carry a "Withdrawn:" line is handled as if it were not there, on
// both lanes: not latest, not installable by its tag, not offered as a Core update. A
// speaker that already runs it is not moved back (RELEASING.md, "Withdrawing a release").
func TestWithdrawnReleasesAreNotOffered(t *testing.T) {
	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	rels := `[{"tag_name":"app-v0.3.0","body":"Withdrawn: Spotify does not start\n\n## SoundTap app 0.3.0"},` +
		`{"tag_name":"os-v0.3.0","body":"## SoundTap Core 0.3.0\r\nWithdrawn: the ST20 loses Wi-Fi\r\n"},` +
		`{"tag_name":"app-v0.2.0","body":"## SoundTap app 0.2.0\n\n- docs: say what Withdrawn: means\n Withdrawn: indented"},` +
		`{"tag_name":"os-v0.2.0","body":"## SoundTap Core 0.2.0"}]`
	for _, running := range []string{"app-v0.1.0", "app-v0.3.0"} {
		app := &fakeApp{staging: filepath.Join(t.TempDir(), "soundtapd.new")}
		m := New(running, slog.New(slog.NewTextHandler(io.Discard, nil)))
		m.clockSynced = func() bool { return true }
		m.client = &http.Client{Transport: github{"releases": rels}}
		m.SetAppInstaller(app)
		if latest, err := m.Latest(context.Background()); err != nil || latest != "app-v0.2.0" {
			t.Fatalf("running %s: Latest = %q, %v; want app-v0.2.0", running, latest, err)
		}
		if _, _, err := m.Start(context.Background(), "app-v0.3.0"); !errors.As(err, new(UnknownTagError)) {
			t.Fatalf("running %s: the withdrawn tag was offered: %v", running, err)
		}
		if _, latest, _, _, _ := m.UpdateInfo(context.Background()); running == "app-v0.3.0" && latest != running {
			t.Fatalf("a speaker on the withdrawn app is offered %q", latest)
		}
		rels, _ := m.Releases(context.Background())
		for _, rel := range rels {
			if rel.TagName == "app-v0.3.0" {
				t.Fatal("the withdrawn app is listed")
			}
		}
	}
	m := testManager(t)
	m.client = &http.Client{Transport: github{"releases": rels}}
	for running, want := range map[string]string{"0.1.0": "os-v0.2.0", "0.2.0": "", "0.3.0": ""} {
		rel, err := m.LatestCore(context.Background(), running)
		got := ""
		if rel != nil {
			got = rel.TagName
		}
		if err != nil || got != want {
			t.Errorf("running Core %s: LatestCore = %q, %v; want %q", running, got, err, want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenCore(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	defer func(k []string) { releaseKeys = k }(releaseKeys)
	releaseKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	core := func(profile, version string) string {
		digest := sha256.Sum256([]byte("package bytes"))
		sig, err := release.Sign(priv, release.Statement{Kind: release.KindCore, Profile: profile, Version: version, Digest: digest[:]})
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
	asset := "/" + Repo + "/releases/download/os-v0.2.0/SoundTap-Core-0.2.0.stu"
	sig := core(release.ProfileProduction, "0.2.0")
	gh := github{
		"releases":     `[{"tag_name":"os-v0.2.0"},{"tag_name":"os-v0.1.0"}]`,
		asset:          "package bytes",
		asset + ".sig": sig + "\n",
	}
	m := testManager(t)
	m.client, m.coreDownload = &http.Client{Transport: gh}, &http.Client{Transport: gh}
	if _, err := m.OpenCore(context.Background(), "0.1.0"); !errors.Is(err, ErrNotUpdatable) {
		t.Fatalf("OpenCore off a speaker: %v, want ErrNotUpdatable", err)
	}

	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	pkg, err := m.OpenCore(context.Background(), "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(pkg.Body)
	_ = pkg.Body.Close()
	if pkg.Tag != "os-v0.2.0" || string(body) != "package bytes" || pkg.Signature != sig {
		t.Fatalf("OpenCore = %+v, body %q", pkg, body)
	}
	if _, err := m.OpenCore(context.Background(), "0.2.0"); !errors.Is(err, ErrCoreCurrent) {
		t.Fatalf("OpenCore when current: %v, want ErrCoreCurrent", err)
	}

	// A .sig that is not this release's statement is not opened: the release lane is
	// production Core packages signed as the tag's version.
	appDigest := sha256.Sum256([]byte("package bytes"))
	app, _ := release.Sign(priv, release.Statement{Kind: release.KindApp, Profile: release.ProfileProduction, Version: "0.2.0", Digest: appDigest[:]})
	for name, bad := range map[string]string{
		"alpha":           core(release.ProfileAlpha, "0.2.0"),
		"another version": core(release.ProfileProduction, "0.3.0"),
		"stock":           core(release.ProfileProduction, "stock"),
		"an app":          app,
		"truncated":       sig[:strings.LastIndex(sig, " sig=")],
	} {
		gh[asset+".sig"] = bad
		if _, err := m.OpenCore(context.Background(), "0.1.0"); err == nil {
			t.Errorf("%s: opened", name)
		}
	}

	// Without its signature a package is not opened at all.
	delete(gh, asset+".sig")
	if _, err := m.OpenCore(context.Background(), "0.1.0"); err == nil {
		t.Fatal("a release without a .sig was opened")
	}
	gh[asset+".sig"] = strings.Repeat("x", sigMax+1)
	if _, err := m.OpenCore(context.Background(), "0.1.0"); err == nil {
		t.Fatal("an oversized .sig was accepted")
	}
}

// No release check asks GitHub before NTP has set the clock: TLS would check GitHub's
// certificate against a clock restored from flash. The wait is bounded, and a sync that
// lands during it lets the check go ahead.
func TestReleaseCheckWaitsForTheClock(t *testing.T) {
	t.Setenv("SOUNDTAP_FIRMWARE", "1")
	defer func(wait, poll time.Duration) { clockWait, clockPoll = wait, poll }(clockWait, clockPoll)
	clockWait, clockPoll = 100*time.Millisecond, time.Millisecond

	var calls atomic.Int32
	gh := github{"releases": `[{"tag_name":"app-v0.2.0"},{"tag_name":"os-v0.2.0"}]`}
	m := testManager(t)
	m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return gh.RoundTrip(r)
	})}
	m.SetAppInstaller(&fakeApp{staging: filepath.Join(t.TempDir(), "soundtapd.new")})
	// The fake signal: unset, until syncAfter asks have gone by.
	var asked, syncAfter atomic.Int32
	syncAfter.Store(1 << 30)
	m.clockSynced = func() bool { return asked.Add(1) > syncAfter.Load() }
	ctx := context.Background()

	start := time.Now()
	if _, err := m.Releases(ctx); !errors.Is(err, ErrClockNotSet) {
		t.Fatalf("Releases before the sync: %v, want ErrClockNotSet", err)
	}
	if waited := time.Since(start); waited < clockWait || waited > 5*time.Second {
		t.Fatalf("waited %v for the clock, want about %v", waited, clockWait)
	}
	if _, _, err := m.Start(ctx, ""); !errors.Is(err, ErrClockNotSet) {
		t.Fatalf("Start before the sync: %v, want ErrClockNotSet", err)
	}
	if !m.mu.TryLock() {
		t.Fatal("Start kept the update lock after the clock check")
	}
	m.mu.Unlock()
	if _, _, err := m.Start(ctx, "app-v0.2.0"); !errors.Is(err, ErrClockNotSet) {
		t.Fatalf("Start of a named tag before the sync: %v, want ErrClockNotSet", err)
	}
	if _, _, _, _, err := m.UpdateInfo(ctx); !errors.Is(err, ErrClockNotSet) {
		t.Fatalf("UpdateInfo before the sync: %v, want ErrClockNotSet", err)
	}
	if _, err := m.OpenCore(ctx, "0.1.0"); !errors.Is(err, ErrClockNotSet) {
		t.Fatalf("OpenCore before the sync: %v, want ErrClockNotSet", err)
	}

	// A request that ends does not wait out the bound.
	clockWait = time.Hour
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.Latest(gone); !errors.Is(err, ErrClockNotSet) {
		t.Fatalf("Latest for a request that ended: %v, want ErrClockNotSet", err)
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("asked GitHub %d times before the clock was set", n)
	}

	// The sync lands while the check waits: it goes ahead.
	syncAfter.Store(asked.Load() + 3)
	if latest, err := m.Latest(ctx); err != nil || latest != "app-v0.2.0" {
		t.Fatalf("Latest after the sync = %q, %v; want app-v0.2.0", latest, err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("asked GitHub %d times after the sync, want once", n)
	}
}

// Off a speaker nothing writes the sync marker and the host keeps its own clock.
func TestClockGateOnlyOnASpeaker(t *testing.T) {
	m := testManager(t)
	m.clockSynced = func() bool { return false }
	m.client = &http.Client{Transport: github{"releases": `[{"tag_name":"app-v0.2.0"}]`}}
	if latest, err := m.Latest(context.Background()); err != nil || latest != "app-v0.2.0" {
		t.Fatalf("Latest off a speaker = %q, %v; want app-v0.2.0", latest, err)
	}
}
