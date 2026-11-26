// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package update owns the SoundTap app's self-update: discovering app releases on
// GitHub, downloading and verifying the new binary (checksum + ed25519 signature, via
// internal/release), and handing it to the same install step as POST /api/soundtapd.
// Both the web API (POST /api/update) and the MQTT update entity drive
// updates through the same Manager, so there is exactly one place that may replace
// the binary — and exactly one lock serializing it.
package update

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stein155/soundtap/app/internal/clocksync"
	"github.com/stein155/soundtap/app/internal/oplock"
	"github.com/stein155/soundtap/app/internal/release"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/safehttp"
)

// Repo is the GitHub repository releases come from.
const Repo = "stein155/soundtap"

const downloadTimeout = 5 * time.Minute

// apiTimeout bounds one GitHub API lookup.
const apiTimeout = 12 * time.Second

const (
	// appFileMode is the downloaded app binary's permission: executable.
	appFileMode = 0o755
	// sigFileMode is the downloaded signature's permission.
	sigFileMode = 0o644
	// percent is a whole download in Progress's unit, and maxProgress where Progress holds
	// from the last byte until the restart.
	percent     = 100
	maxProgress = 99
)

// Why a release is not installed; the release-specific part is added where they occur.
var (
	errNoLatestTag          = errors.New("latest release missing tag")
	errSignedAsApp          = errors.New("is signed as app")
	errSignedForDevelopment = errors.New("is signed for development speakers, not as a release")
	errNotNewer             = errors.New("is not newer than the running")
	errNotCoreRelease       = errors.New("is not a Core release")
	errSignedAs             = errors.New("is signed as")
	errSignatureTooLarge    = errors.New("signature too large")
)

// coreDownloadTimeout bounds a Core package's download and install together, as the
// install lane bounds an upload (uploadReadTimeout in internal/web).
const coreDownloadTimeout = 20 * time.Minute

// verifySigned checks the file at path against the <asset>.sig at sigPath, the signed
// statement about an app package (ADR 0048) under one of keys, and returns that statement.
func verifySigned(keys []string, path, sigPath string) (release.Statement, error) {
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return release.Statement{}, fmt.Errorf("read signature: %w", err)
	}

	statement, err := release.Verify(keys, string(sig), release.KindApp)
	if err != nil {
		return release.Statement{}, fmt.Errorf("verify signature: %w", err)
	}

	sum, err := release.SHA256File(path)
	if err != nil {
		return release.Statement{}, fmt.Errorf("hash package: %w", err)
	}

	digest, err := hex.DecodeString(sum)
	if err != nil {
		return release.Statement{}, fmt.Errorf("decode package digest: %w", err)
	}

	err = statement.Matches(digest)
	if err != nil {
		return statement, fmt.Errorf("match signature: %w", err)
	}

	return statement, nil
}

// Release is one GitHub release, as much of it as the updater needs.
type Release struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"` // release notes, shown before the user confirms
	PublishedAt string `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
	Draft       bool   `json:"draft"`
}

// ErrNotUpdatable means this instance can't self-update (not an installed speaker).
var ErrNotUpdatable = errors.New("updates are only available on an installed speaker")

// ErrBusy means an update is already in progress.
var ErrBusy = errors.New("an update is already in progress")

// ErrClockNotSet means NTP has not set the speaker's clock since this boot, so it does not
// ask GitHub yet. TLS checks a certificate against the clock: one restored from flash after
// a power cut fails the check with an error that says nothing useful, or, worse, passes a
// certificate that is no longer valid. The caller says so and asks again later.
var ErrClockNotSet = errors.New("the speaker's clock is not set yet")

// How long a release check waits for the clock's first sync, and how often it looks. A
// check soon after boot gets the sync that is about to land; a speaker without NTP gets
// ErrClockNotSet, not a request that hangs.
//
//nolint:gochecknoglobals // test seams: tests wait 100 ms and poll every millisecond
var (
	clockWait = 3 * time.Second
	clockPoll = 250 * time.Millisecond
)

// UnknownTagError reports a requested tag that is not a published release.
type UnknownTagError struct{ Tag string }

func (e UnknownTagError) Error() string { return "unknown release " + e.Tag }

// AppInstaller puts a verified SoundTap app in place and restarts into it: the same step
// POST /api/soundtapd ends in (ADR 0035), so the stored override, the Core-level check and
// the boot-time rollback are shared.
type AppInstaller interface {
	// StagingPath is where the download goes: next to the copy that runs, on a
	// filesystem with room for it (/tmp is too small on these speakers).
	StagingPath() string
	// InstallApp checks, stores and starts the binary at path. On success the process
	// restarts shortly after it returns.
	InstallApp(path string) error
}

// Manager performs self-updates of the SoundTap app. Create with New; safe for
// concurrent use.
type Manager struct {
	version string
	log     *slog.Logger
	client  *http.Client // GitHub API lookups, over an SSRF-safe transport
	// Downloads: the same safe transport, with a timeout long enough for a binary.
	download *http.Client
	app      AppInstaller

	// mu is held for the whole install (TryLock — a failed acquire means "busy").
	// Start hands it off to the background install goroutine.
	mu oplock.Lock

	// The release list, kept for releasesTTL. GitHub allows 60 unauthenticated calls an
	// hour per public address, which every speaker in a house shares, and each settings
	// page asked again.
	relMu  sync.Mutex
	rels   []Release // every non-draft release, app and Core
	relsAt time.Time
	// Core downloads: the package streams straight into the install, which writes NAND as
	// it goes, so the client may take as long as the install lane gives an upload.
	coreDownload *http.Client
	// clockSynced reports whether NTP has set the clock since boot (clocksync.Synced); a
	// field so the tests can give the signal.
	clockSynced func() bool
	// progress is an app download's percentage plus one while it runs with a known size,
	// and 0 otherwise (Progress).
	progress atomic.Int64
	// keys are the keys a release's signature must verify with (release.PublicKeys); a
	// field so the tests sign with their own pair.
	keys []string
}

const releasesTTL = 10 * time.Minute

// New builds a Manager for the running version. It cannot update anything until
// SetAppInstaller gives it the install step.
func New(version string, log *slog.Logger) *Manager {
	return &Manager{
		version: version,
		log:     log,
		client:  &http.Client{Timeout: apiTimeout, Transport: safehttp.SafeTransport()},
		// Without the safe transport a 302 or DNS rebind on the artifact host could
		// reach loopback/RFC1918.
		download:     &http.Client{Timeout: downloadTimeout, Transport: safehttp.SafeTransport()},
		app:          nil,
		mu:           oplock.Lock{},
		relMu:        sync.Mutex{},
		rels:         nil,
		relsAt:       time.Time{},
		coreDownload: &http.Client{Timeout: coreDownloadTimeout, Transport: safehttp.SafeTransport()},
		clockSynced:  clocksync.Synced,
		progress:     atomic.Int64{},
		keys:         release.PublicKeys(),
	}
}

// SetAppInstaller enables updates through app.
func (m *Manager) SetAppInstaller(app AppInstaller) { m.app = app }

// Version returns the running version the Manager was built with.
func (m *Manager) Version() string { return m.version }

// Update modes, reported to the UI so it can say why self-update is off.
const (
	// ModeFirmware: soundtapd runs from a flashed SoundTap Core. The app updates on its own
	// from app-v* releases; Core through an STU.
	ModeFirmware = "firmware"
	// ModeUnmanaged: not an installed instance at all (a dev run, a container).
	ModeUnmanaged = "unmanaged"
)

// Mode reports how this instance is deployed, and so how it can be updated.
func (*Manager) Mode() string {
	if os.Getenv("SOUNDTAP_FIRMWARE") == "1" {
		return ModeFirmware
	}

	return ModeUnmanaged
}

// Updatable reports whether self-update is possible here: on a speaker whose OS lets
// the app be replaced.
func (m *Manager) Updatable() bool { return m.Mode() == ModeFirmware && m.app != nil }

// Start resolves tag ("" means the latest stable release), verifies it is a
// release we actually publish, and — unless it is already running — installs it
// and restarts in the background. It returns the resolved target and whether it
// was already current. Errors: ErrNotUpdatable, ErrBusy, UnknownTagError, or a
// wrapped release-lookup failure.
func (m *Manager) Start(ctx context.Context, tag string) (string, bool, error) {
	if !m.Updatable() {
		return "", false, ErrNotUpdatable
	}

	if !m.mu.TryLock() {
		return "", false, ErrBusy
	}

	target, current, err := m.resolve(ctx, strings.TrimSpace(tag))
	if err != nil || current {
		m.mu.Unlock()

		return target, current, err
	}
	// Only a named tag may be older, a signed release like any other, so without a press
	// (ADR 0048, ADR 0118); the tagless update, also Home Assistant's, never goes back.
	m.install(ctx, target, tag != "")

	return target, false, nil
}

// UpdateInfo reports the running version, the latest available stable release, its
// release URL, and whether updating is possible here (only on an installed speaker).
// It backs the MQTT `update` entity so hubs can show an update notification.
// Off-speaker (not updatable) it reports latest == installed and skips the GitHub
// call, so no false update is offered.
//
//nolint:revive // function-result-limit: implements mqttbridge.Updater, whose five results are fixed
func (m *Manager) UpdateInfo(ctx context.Context) (string, string, string, bool, error) {
	installed := m.version

	if !m.Updatable() {
		return installed, installed, "", false, nil
	}

	latest, err := m.Latest(ctx)
	if err != nil {
		return installed, "", "", true, err
	}

	if latest == "" || !newer(latest, installed) {
		latest = installed
	}

	releaseURL := ""
	if latest != installed {
		releaseURL = "https://github.com/" + Repo + "/releases/tag/" + latest
	}

	return installed, latest, releaseURL, true, nil
}

// UpdateToLatest installs the latest stable release and restarts, reusing the same
// path as POST /api/update. It backs the MQTT update entity's Install
// action. It returns quickly: the download + restart run in the background on
// success, and an already-current version is not an error.
func (m *Manager) UpdateToLatest(ctx context.Context) error {
	_, _, err := m.Start(ctx, "")

	return err
}

// Latest returns the tag of the highest app release this speaker is offered, or "".
func (m *Manager) Latest(ctx context.Context) (string, error) {
	rel, err := m.LatestRelease(ctx)
	if err != nil || rel == nil {
		return "", err
	}

	return strings.TrimSpace(rel.TagName), nil
}

// LatestRelease returns the highest app release this speaker is offered (offered), or nil.
// GitHub lists by publication, so a maintenance release of an older line can come first.
func (m *Manager) LatestRelease(ctx context.Context) (*Release, error) {
	rels, err := m.Releases(ctx)
	if err != nil {
		return nil, err
	}

	running, _ := appVersion(m.version) // a dev build runs no prerelease

	var (
		best  *Release
		bestV release.Version
	)

	for idx, rel := range rels {
		candidate, ok := appVersion(strings.TrimSpace(rel.TagName))
		if !ok || !offered(rel, candidate, running) || (best != nil && candidate.Compare(bestV) <= 0) {
			continue
		}

		best, bestV = &rels[idx], candidate
	}

	return best, nil
}

// offered is the prerelease rule of ADR 0035: a release is offered to every speaker, a
// prerelease (an X.Y.Z-alpha.N version, or one GitHub marks as a prerelease) only to a
// speaker that runs an alpha itself. A speaker on a release never moves onto an alpha by
// itself; one on an alpha moves on to newer alphas and to the release.
func offered(rel Release, v, running release.Version) bool {
	return !rel.Prerelease && !v.Prerelease() || running.Prerelease()
}

// appVersion parses an app-v<version> tag (release.Version); anything else is not a
// version we can order.
func appVersion(tag string) (release.Version, bool) {
	number, ok := strings.CutPrefix(tag, "app-v")
	if !ok {
		var none release.Version

		return none, false
	}

	return release.ParseVersion(number)
}

// newer reports whether tag is a higher app version than running. A running version
// that is not one (a "dev" build) has nothing to compare with, so any release is newer.
func newer(tag, running string) bool {
	want, isApp := appVersion(tag)
	if !isApp {
		return false
	}

	have, isApp := appVersion(running)
	if !isApp {
		return true
	}

	return want.Compare(have) > 0
}

// Releases returns the repo's published app releases (app-v*, ADR 0035), newest first
// (GitHub's order). OS releases carry an STU, which goes through /api/firmware instead.
func (m *Manager) Releases(ctx context.Context) ([]Release, error) {
	all, err := m.allReleases(ctx)
	if err != nil {
		return nil, err
	}

	var appRels []Release

	for _, rel := range all {
		if _, ok := appAsset(rel.TagName); ok {
			appRels = append(appRels, rel)
		}
	}

	return appRels, nil
}

// WithdrawnMarker opens the line of release notes that withdraws a release (RELEASING.md,
// "Withdrawing a release"): "Withdrawn: <reason>" at the start of a line.
const WithdrawnMarker = "Withdrawn:"

// Withdrawn reports whether the maintainer withdrew this release: a line of its notes
// starts with WithdrawnMarker. Generated notes never start a line with it: their
// changes are list items.
func (r Release) Withdrawn() bool {
	for line := range strings.SplitSeq(r.Body, "\n") {
		if strings.HasPrefix(line, WithdrawnMarker) {
			return true
		}
	}

	return false
}

// appAsset names the binary an app release carries: app-v1.2.3 has
// SoundTap-app-1.2.3-armv7.bin (release.yml).
func appAsset(tag string) (string, bool) {
	version, ok := strings.CutPrefix(strings.TrimSpace(tag), "app-v")
	if !ok || version == "" || strings.ContainsAny(version, "/?#") {
		return "", false
	}

	return "SoundTap-app-" + version + "-armv7.bin", true
}

// Progress is how far an app update under way is, in percent (0-99), and false when none
// runs or the download's size is unknown. It covers the download, which writes the binary
// next to the running one; it holds at 99 from the last byte until the restart, and goes
// when the install fails.
func (m *Manager) Progress() (int, bool) {
	v := m.progress.Load()

	return int(v - 1), v > 0
}

// ErrCoreCurrent means no published Core release is newer than the running one.
var ErrCoreCurrent = errors.New("no newer SoundTap Core release")

// coreVersion parses an os-v<version> tag (release.yml tags Core from os/VERSION).
func coreVersion(tag string) (release.Version, bool) {
	number, ok := strings.CutPrefix(tag, "os-v")
	if !ok {
		var none release.Version

		return none, false
	}

	return release.ParseVersion(number)
}

// coreAsset names the package a Core release carries: os-v1.2.3 has SoundTap-Core-1.2.3.stu.
func coreAsset(tag string) (string, bool) {
	if _, ok := coreVersion(tag); !ok {
		return "", false
	}

	return "SoundTap-Core-" + strings.TrimPrefix(tag, "os-v") + ".stu", true
}

// LatestCore returns the highest Core release this speaker is offered (offered) if it is
// newer than running, the version in /etc/soundtap/os-version, and nil otherwise. A running
// version that is not one (a development image) is offered nothing: it has no line to move
// along.
func (m *Manager) LatestCore(ctx context.Context, running string) (*Release, error) {
	have, ok := release.ParseVersion(strings.TrimSpace(running))
	if !ok {
		return nil, nil //nolint:nilnil // no release and no error is "nothing newer", the doc's contract
	}

	all, err := m.allReleases(ctx)
	if err != nil {
		return nil, err
	}

	var (
		best  *Release
		bestV release.Version
	)

	for idx, rel := range all {
		candidate, ok := coreVersion(strings.TrimSpace(rel.TagName))
		if !ok || !offered(rel, candidate, have) || candidate.Compare(have) <= 0 ||
			(best != nil && candidate.Compare(bestV) <= 0) {
			continue
		}

		best, bestV = &all[idx], candidate
	}

	return best, nil
}

// CorePackage is a Core release on its way to the install lane: the package as a
// stream, never stored (the speaker has no room for it), with its <asset>.sig.
type CorePackage struct {
	Tag       string
	Body      io.ReadCloser
	Size      int64 // -1 when the server does not say
	Signature string
}

// sigMax bounds a .sig: one signed statement (ADR 0048).
const sigMax = release.SigMax

// OpenCore fetches the signature of the newest Core release that is newer than running
// and opens its package for reading. Only that release: an older one or Bose's firmware
// goes in through an upload, with its own confirmation (ADR 0048). The signature is
// checked by the install lane before the first byte is written. The caller closes Body.
func (m *Manager) OpenCore(ctx context.Context, running string) (CorePackage, error) {
	if m.Mode() != ModeFirmware {
		return CorePackage{}, ErrNotUpdatable
	}

	rel, err := m.LatestCore(ctx, running)
	if err != nil {
		return CorePackage{}, err
	}

	if rel == nil {
		return CorePackage{}, ErrCoreCurrent
	}

	tag := strings.TrimSpace(rel.TagName)

	asset, ok := coreAsset(tag)
	if !ok {
		return CorePackage{}, fmt.Errorf("release %q %w", tag, errNotCoreRelease)
	}

	base := "https://github.com/" + Repo + "/releases/download/" + tag + "/" + asset

	sig, err := m.fetchSignature(ctx, base+".sig")
	if err != nil {
		return CorePackage{}, err
	}
	// The install lane verifies again and holds the digest to the bytes; this refuses,
	// before the download, a .sig that is not this release: Core, production, the tag's
	// version (ADR 0048).
	statement, err := release.Verify(m.keys, sig, release.KindCore)
	if err != nil {
		return CorePackage{}, fmt.Errorf("verify %s: %w", tag, err)
	}

	if statement.Profile != release.ProfileProduction || statement.Version != strings.TrimPrefix(tag, "os-v") {
		return CorePackage{}, fmt.Errorf("%s %w %s %s", tag, errSignedAs, statement.Profile, statement.Version)
	}

	body, size, err := m.get(ctx, m.coreDownload, base)
	if err != nil {
		return CorePackage{}, err
	}

	return CorePackage{Tag: tag, Body: body, Size: size, Signature: sig}, nil
}

// clockSet waits up to clockWait for the clock's first NTP sync since boot and returns
// ErrClockNotSet if it does not come, or ctx ends first. Only on a speaker: anywhere else
// the host keeps its own clock and clock.sh never runs.
func (m *Manager) clockSet(ctx context.Context) error {
	if m.Mode() != ModeFirmware || m.clockSynced() {
		return nil
	}

	deadline := time.NewTimer(clockWait)
	defer deadline.Stop()

	poll := time.NewTicker(clockPoll)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return ErrClockNotSet
		case <-deadline.C:
			return ErrClockNotSet
		case <-poll.C:
			if m.clockSynced() {
				return nil
			}
		}
	}
}

// resolve is the release Start installs for tag ("" for the latest), and whether it is
// the one running already.
func (m *Manager) resolve(ctx context.Context, tag string) (string, bool, error) {
	if tag == "" {
		latest, err := m.Latest(ctx)
		if err != nil {
			return "", false, fmt.Errorf("latest release check failed: %w", err)
		}

		if latest == "" {
			return "", false, errNoLatestTag
		}
		// Latest never downgrades: an older app goes in only by naming its tag (ADR 0118);
		// it is a signed release, so it needs no press (ADR 0048).
		if !newer(latest, m.version) {
			return m.version, true, nil
		}

		return latest, latest == m.version, nil
	}
	// Only ever install a tag we actually publish — never an arbitrary ref —
	// so a crafted request can't point the speaker at a foreign download.
	published, err := m.isOffered(ctx, tag)
	if err != nil {
		return "", false, fmt.Errorf("release check failed: %w", err)
	}

	if !published {
		return "", false, UnknownTagError{Tag: tag}
	}

	return tag, tag == m.version, nil
}

// allReleases is the repo's releases that are neither drafts nor withdrawn, one GitHub
// call per releasesTTL for the app and the Core lane together. A withdrawn release is
// handled as if it were not there: never latest, never installed by its tag, never
// offered as a Core update. A speaker that already runs it stays on it, since nothing
// here moves a speaker back.
// Every GitHub contact starts here, so none is made before the clock is set; the wait
// is outside the lock.
func (m *Manager) allReleases(ctx context.Context) ([]Release, error) {
	err := m.clockSet(ctx)
	if err != nil {
		return nil, err
	}

	m.relMu.Lock()
	defer m.relMu.Unlock()

	if m.rels != nil && time.Since(m.relsAt) < releasesTTL {
		return m.rels, nil
	}

	var rels []Release

	err = m.getJSON(ctx, "https://api.github.com/repos/"+Repo+"/releases?per_page=100", &rels)
	if err != nil {
		return nil, err
	}

	kept := []Release{}

	for _, rel := range rels {
		if !rel.Draft && !rel.Withdrawn() {
			kept = append(kept, rel)
		}
	}

	m.rels, m.relsAt = kept, time.Now()

	return kept, nil
}

// isOffered reports whether tag is a real release of this repo, neither a draft nor
// withdrawn.
func (m *Manager) isOffered(ctx context.Context, tag string) (bool, error) {
	rels, err := m.Releases(ctx)
	if err != nil {
		return false, err
	}

	for _, rel := range rels {
		if strings.TrimSpace(rel.TagName) == tag {
			return true, nil
		}
	}

	return false, nil
}

// install downloads and installs release target in the background. The caller must
// already hold mu (via TryLock); install hands the lock off to the goroutine and
// releases it when the install fails. On success the process restarts with it held.
// allowOlder lets a release older than the running app in; only a request naming its tag sets it.
// The install outlives the request that started it: ctx gives it no deadline or cancel.
func (m *Manager) install(ctx context.Context, target string, allowOlder bool) {
	from := m.version
	ctx = context.WithoutCancel(ctx)

	safego.Go("update.install", func() {
		err := m.installRelease(ctx, target, allowOlder)
		if err != nil {
			m.progress.Store(0)
			m.mu.Unlock()
			m.log.Warn("self-update failed", "from", from, "to", target, "err", err)

			return
		}

		m.log.Info("self-update installed; restarting", "from", from, "to", target)
	})
}

// admitApp is the app lane's policy (ADR 0048) for a release whose signature verified:
// its statement names the tag's version, every release is signed production, and
// without allowOlder only a newer app goes in.
func (m *Manager) admitApp(statement release.Statement, tag string, allowOlder bool) error {
	switch {
	case statement.Version != strings.TrimPrefix(tag, "app-v"):
		// A signed older app published under a newer tag is not that release.
		return fmt.Errorf("%s %w %s", tag, errSignedAsApp, statement.Version)
	case statement.Profile != release.ProfileProduction:
		return fmt.Errorf("%s %w", tag, errSignedForDevelopment)
	case !allowOlder && !newer(tag, m.version):
		return fmt.Errorf("%s %w %s: an older app is installed by naming its tag", tag, errNotNewer, m.version)
	default:
		return nil
	}
}

func (m *Manager) installRelease(ctx context.Context, tag string, allowOlder bool) error {
	asset, ok := appAsset(tag)
	if !ok {
		return UnknownTagError{Tag: tag}
	}

	path := m.app.StagingPath()

	sigPath := path + ".sig"
	defer os.Remove(sigPath)

	base := "https://github.com/" + Repo + "/releases/download/" + tag + "/" + asset

	err := m.downloadApp(ctx, base, path)
	if err != nil {
		_ = os.Remove(path)

		return err
	}
	// Every release is signed the one way ADR 0048 describes: <asset>.sig, the signed
	// statement naming the asset's kind, profile, version and SHA-256. No tag is exempt.
	err = m.downloadFile(ctx, base+".sig", sigPath, sigFileMode)
	if err != nil {
		_ = os.Remove(path)

		return err
	}

	statement, err := verifySigned(m.keys, path, sigPath)
	if err == nil {
		err = m.admitApp(statement, tag, allowOlder)
	}

	if err != nil {
		_ = os.Remove(path)

		return err
	}

	err = m.app.InstallApp(path)
	if err != nil {
		return fmt.Errorf("install app: %w", err)
	}

	return nil
}

func (m *Manager) getJSON(ctx context.Context, target string, out any) error {
	err := safehttp.GetJSON(ctx, m.client, "SoundTap/"+m.version, target, out)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", target, err)
	}

	return nil
}

func (m *Manager) downloadFile(ctx context.Context, target, path string, mode fs.FileMode) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	err := safehttp.Download(ctx, m.download, "SoundTap/"+m.version, target, path, mode)
	if err != nil {
		return fmt.Errorf("download %s: %w", target, err)
	}

	return nil
}

// downloadApp is downloadFile for the app binary, with its percentage for Progress.
func (m *Manager) downloadApp(ctx context.Context, target, path string) error {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	err := safehttp.DownloadWatched(ctx, m.download, "SoundTap/"+m.version, target, path, appFileMode,
		func(done, total int64) {
			if total > 0 {
				m.progress.Store(min(done*percent/total, maxProgress) + 1)
			}
		})
	if err != nil {
		return fmt.Errorf("download %s: %w", target, err)
	}

	return nil
}

func (m *Manager) fetchSignature(ctx context.Context, target string) (string, error) {
	body, _, err := m.get(ctx, m.client, target)
	if err != nil {
		return "", err
	}
	defer func() { _ = body.Close() }()

	sig, err := io.ReadAll(io.LimitReader(body, sigMax+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", target, err)
	}

	if len(sig) > sigMax {
		return "", fmt.Errorf("%s: %w", target, errSignatureTooLarge)
	}

	return strings.TrimSpace(string(sig)), nil
}

// get starts a GET whose status is 200 and returns its body and length (-1 when the
// server does not say); the caller closes the body.
func (m *Manager) get(ctx context.Context, client *http.Client, target string) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return nil, 0, fmt.Errorf("build request for %s: %w", target, err)
	}

	req.Header.Set("User-Agent", "SoundTap/"+m.version)

	resp, err := safehttp.Do(ctx, client, req)
	if err != nil {
		return nil, 0, fmt.Errorf("get %s: %w", target, err)
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()

		return nil, 0, safehttp.HTTPStatusError{URL: target, Status: resp.StatusCode}
	}

	return resp.Body, resp.ContentLength, nil
}
