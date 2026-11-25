// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/update"
	"github.com/stein155/soundtap/app/internal/web/apierror"
)

// infoTimeout bounds the backend reads behind GET /api/info.
const infoTimeout = 5 * time.Second

// osVersionFile holds the SoundTap Core version the running image was built as (ADR 0035).
//
//nolint:gochecknoglobals // a variable so tests can replace it
var osVersionFile = "/etc/soundtap/os-version"

// osBuild reads the image's version and commit; os-commit lies next to os-version.
// It returns the version, then the commit.
func osBuild() (string, string) {
	read := func(path string) string {
		b, _ := os.ReadFile(path)

		return strings.TrimSpace(string(b))
	}

	return read(osVersionFile), read(filepath.Join(filepath.Dir(osVersionFile), "os-commit"))
}

func (s *Server) versionInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.versionData())
}

// versionData is what GET /api/version answers, and version.json in the support archive.
func (s *Server) versionData() map[string]any {
	osVersion, osCommit := osBuild()

	caps := dropBluetooth(s.capabilities, "bluetooth")
	if caps == nil {
		caps = []string{} // [] in JSON, never null
	}

	data := map[string]any{
		keyVersion: s.version,
		// The two lanes as people read them: "SoundTap Core 0.1.0 · app 0.1.0". os is empty
		// off a speaker; app drops the tag prefix.
		"os":  osVersion,
		"app": strings.TrimPrefix(s.version, "app-v"),
		// The commit the image was built from; empty off a speaker. A hot-swapped
		// app names its own commit in version (dev-<sha>).
		"commit":    osCommit,
		"updatable": s.update.Updatable(),
		// updateMode lets the UI say why self-update is unavailable instead of
		// assuming the only reason is "not a speaker".
		"updateMode": s.update.Mode(),
		"api":        APILevel,
		// What this speaker has, so a client need not probe routes (ADR 0108).
		"capabilities": caps,
	}
	if pct, ok := s.updateProgress(); ok {
		data["updateProgress"] = pct
	}

	return data
}

// updateProgress is how far an update under way is, in percent: an app release downloading
// (POST /api/update) or a Core package streaming into the spare slot (POST /api/core-update,
// an upload). false when none runs or its size is unknown: the page then shows no number.
func (s *Server) updateProgress() (int, bool) {
	if s.update != nil {
		if pct, ok := s.update.Progress(); ok {
			return pct, true
		}
	}

	if s.fw != nil {
		return s.fw.Progress()
	}

	return 0, false
}

// APILevel is the level of this HTTP API (ADR 0091), also advertised over mDNS. Raise it
// in the commit that removes or changes the meaning of a route or a field; adding one
// does not need it.
const APILevel = 1

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), infoTimeout)
	defer cancel()

	info, err := s.backend.Info(ctx)
	if err != nil {
		s.fail(w, "speaker info failed", err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":       s.Identity(info.DeviceID),
		"deviceId": info.DeviceID,
		"name":     info.Name,
		"model":    info.Type,
		"ip":       info.IP,
	})
}

// Identity is what this speaker is called by anything that has to remember it across a
// rename or a new lease -- a device list, a group, a proxy route, a DNS-SD TXT record.
// deviceID is the legacy MAC, kept as the fallback for a board whose manufacturing
// partition cannot be read.
func (s *Server) Identity(deviceID string) string {
	if s.serial != "" {
		return s.serial
	}

	return deviceID
}

// releases lists what the app and Core can update to: the latest release this speaker is
// offered on each lane.
func (s *Server) releases(w http.ResponseWriter, r *http.Request) {
	osVersion, _ := osBuild()
	core := map[string]any{"current": osVersion, "latest": nil}

	_, err := s.update.Releases(r.Context())
	if err != nil {
		// No reachable release list is an expected state (offline, private repo,
		// rate limit, a clock NTP has not set yet), not a server error: say so and let
		// the UI show it.
		unavailable, code := "release list unreachable", apierror.UpstreamFailed
		if errors.Is(err, update.ErrClockNotSet) {
			unavailable, code = "the speaker's clock is not set yet", apierror.ClockNotSet
		} else {
			s.log.Warn("release list failed", "err", err)
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"current":         s.version,
			"updatable":       s.update.Updatable(),
			"stable":          nil,
			"core":            core,
			"unavailable":     unavailable,
			"unavailableCode": code,
		})

		return
	}
	// A newer SoundTap Core, installed through POST /api/core-update. Only on a
	// speaker: anywhere else there is no Core to replace.
	if s.update.Mode() == update.ModeFirmware {
		rel, coreErr := s.update.LatestCore(r.Context(), osVersion)
		if coreErr == nil && rel != nil {
			core["latest"] = map[string]any{
				"tag": strings.TrimSpace(rel.TagName), "name": strings.TrimSpace(rel.Name),
				"notes": releaseNotes(rel.Body), "publishedAt": rel.PublishedAt,
			}
		}
	}
	// "stable" is the highest app release this speaker is offered: a release, or on a
	// speaker that runs an alpha also a newer alpha (ADR 0035). The list is cached.
	var stable map[string]any

	latest, err := s.update.LatestRelease(r.Context())
	if err == nil && latest != nil {
		stable = map[string]any{
			"tag": strings.TrimSpace(latest.TagName), "name": strings.TrimSpace(latest.Name),
			"notes": releaseNotes(latest.Body), "publishedAt": latest.PublishedAt,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current":   s.version,
		"updatable": s.update.Updatable(),
		"stable":    stable,
		"core":      core,
	})
}

// releaseNotesMax bounds what a release body can push into a 128 MiB speaker's
// response. Notes longer than this are cut at a line break so the tail does not end
// mid-word, with a marker rather than a silent truncation.
const releaseNotesMax = 4 << 10

func releaseNotes(body string) string {
	body = strings.TrimSpace(body)
	if len(body) <= releaseNotesMax {
		return body
	}

	cut := body[:releaseNotesMax]
	if i := strings.LastIndexByte(cut, '\n'); i > releaseNotesMax/2 {
		cut = cut[:i]
	}

	return cut + "\n…"
}
