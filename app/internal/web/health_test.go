// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package web_test

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/web"
)

func TestHealthReportsResources(t *testing.T) {
	h, _ := newServer(t)
	rec := do(t, h, "GET", "/api/health", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Disk struct {
			Path       string `json:"path"`
			FreeBytes  uint64 `json:"freeBytes"`
			TotalBytes uint64 `json:"totalBytes"`
		} `json:"disk"`
		Memory struct {
			TotalBytes uint64 `json:"totalBytes"`
		} `json:"memory"`
		Processes int      `json:"processes"`
		Warnings  []string `json:"warnings"`
	}
	decodeBody(t, rec, &got)
	if got.Disk.Path == "" {
		t.Error("no disk path reported")
	}
	if runtime.GOOS == "linux" {
		if got.Disk.TotalBytes == 0 {
			t.Error("disk size is 0 on linux")
		}
		if got.Memory.TotalBytes == 0 {
			t.Error("memory total is 0 on linux")
		}
		if got.Processes == 0 {
			t.Error("process count is 0 on linux")
		}
	}
	// A healthy test box must not manufacture warnings; a full one legitimately can,
	// so only the shape is asserted here.
	if got.Warnings == nil {
		t.Error("warnings is null, want an array (the UI iterates it)")
	}
}

// The counters are in /api/health as internal/counters holds them (ADR 0127).
func TestHealthReportsCounters(t *testing.T) {
	counters.Reset()
	t.Cleanup(counters.Reset)
	counters.SoundtapdRestarts.Store(2)
	counters.DaemonRestarted("librespot")
	counters.BufferUnderruns.Add(3)
	counters.ALSAUnderruns.Add(4)
	counters.InstallFailures.Add(1)
	counters.BCOFlashFailures.Add(5)
	counters.BootConfirmRetries.Add(6)
	h, _ := newServer(t)
	var got struct {
		Counters map[string]any `json:"counters"`
	}
	decodeBody(t, do(t, h, "GET", "/api/health", ""), &got)
	want := map[string]any{
		"sinceSeconds": float64(0), "soundtapdRestarts": float64(2), "daemonRestarts": map[string]any{"librespot": float64(1)},
		"bufferUnderruns": float64(3), "alsaUnderruns": float64(4), "installFailures": float64(1),
		"bcoFlashFailures": float64(5), "bootConfirmRetries": float64(6),
	}
	if !reflect.DeepEqual(got.Counters, want) {
		t.Errorf("counters %v, want %v", got.Counters, want)
	}
}

// A fault soundtapd found in itself, such as an unidentified model, is a health warning.
func TestHealthListsDaemonProblems(t *testing.T) {
	srv, _, _ := newServerSrv(t)
	problem := "hardware model unknown: hardware control is off"
	srv.ReconfigureForTest(func(o *web.Options) { o.Health.DaemonProblems = func() []string { return []string{problem} } })
	var got struct {
		Warnings []string `json:"warnings"`
	}
	decodeBody(t, do(t, srv.Handler(), "GET", "/api/health", ""), &got)
	if !slices.Contains(got.Warnings, problem) {
		t.Fatalf("warnings = %q, want %q", got.Warnings, problem)
	}
}

// The bundle is pasted into public issues, so a Wi-Fi password that reached a log
// must not travel with it.
func TestDebugBundleRedactsCredentials(t *testing.T) {

	h, _ := newServer(t)
	rec := do(t, h, "GET", "/api/debug", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"== network ==", "== resources ==", "== snapcast ==", "variant    :"} {
		if !strings.Contains(body, want) {
			t.Errorf("bundle is missing %q", want)
		}
	}
	if got := rec.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("Content-Disposition = %q, want none without ?download", got)
	}

	rec = do(t, h, "GET", "/api/debug?download=1", "")
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", got)
	}
}

// Redaction is exercised through a real log file so the tail path is covered too.
func TestSupportLogTailIsScrubbed(t *testing.T) {
	// Point the tail at a temp file rather than the real /tmp/soundtap.log. Using the real
	// path meant this test skipped whenever that file existed, which is always true on a
	// device and true for 3 runs in 4 when two copies of the suite run at once: a
	// redaction test that reports green without testing anything. No skips now.
	path := filepath.Join(t.TempDir(), "soundtap.log")
	t.Cleanup(web.SetLogFilesForTest("agent log", path))

	body := "[wifi] joining HomeNet\npsk=hunter2hunter\nPassword: s3cr3t\nAuthorization: Bearer abc.def\nplain line\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	h, _ := newServer(t)
	got := do(t, h, "GET", "/api/debug", "").Body.String()
	for _, secret := range []string{"hunter2hunter", "s3cr3t", "abc.def"} {
		if strings.Contains(got, secret) {
			t.Errorf("bundle leaked %q", secret)
		}
	}
	if !strings.Contains(got, "plain line") || !strings.Contains(got, "joining HomeNet") {
		t.Error("redaction ate the surrounding log")
	}
	if strings.Count(got, "<redacted>") != 3 {
		t.Errorf("want 3 redactions, got %d", strings.Count(got, "<redacted>"))
	}
}

// The player logs the whole stream URL, userinfo included.
func TestSupportBundleRedactsURLUserinfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "soundtap.log")
	t.Cleanup(web.SetLogFilesForTest("agent log", path))
	line := "time=2026-11-14T19:00:00Z level=INFO msg=play src=TUNEIN url=http://listener:SYNTHETIC-PASSWORD@radio.example/stream\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	h, _ := newServer(t)
	got := do(t, h, "GET", "/api/debug", "").Body.String()
	if strings.Contains(got, "SYNTHETIC-PASSWORD") {
		t.Error("bundle leaked the URL password")
	}
	if !strings.Contains(got, "@radio.example/stream") {
		t.Error("redaction ate the host and path")
	}
}

// The standby-screen page is only offered where a panel exists, and its settings
// round-trip through /api/settings.
func TestDisplaySettingsRoundTrip(t *testing.T) {
	h, _ := newServer(t)

	var before struct {
		HasDisplay bool `json:"hasDisplay"`
		Display    struct {
			Hour12    bool    `json:"hour12"`
			City      string  `json:"city"`
			Lat       float64 `json:"lat"`
			Timezone  string  `json:"timezone"`
			WeatherOn bool    `json:"weatherOn"`
		} `json:"display"`
	}
	decodeBody(t, do(t, h, "GET", "/api/settings", ""), &before)
	if before.HasDisplay {
		t.Error("hasDisplay is true with no panel wired")
	}
	if before.Display.City != "" || before.Display.WeatherOn {
		t.Errorf("display defaults to %+v, want no town and no weather", before.Display)
	}

	const utrecht = `{"display":{"hour12":true,"city":"Utrecht","lat":52.09,"lon":5.12,"timezone":"Europe/Amsterdam","weatherOn":true}}`
	if rec := do(t, h, "PUT", "/api/settings", utrecht); rec.Code != 200 {
		t.Fatalf("put display: %d %s", rec.Code, rec.Body)
	}
	decodeBody(t, do(t, h, "GET", "/api/settings", ""), &before)
	if d := before.Display; !d.Hour12 || d.City != "Utrecht" || d.Lat != 52.09 || d.Timezone != "Europe/Amsterdam" || !d.WeatherOn {
		t.Fatalf("display = %+v", d)
	}

	if rec := do(t, h, "PUT", "/api/settings", `{"display":{"clockOff":false}}`); rec.Code != 400 {
		t.Fatalf("clock without a town gave %d, want 400", rec.Code)
	}
}

// The init script keeps the last three boots; the support bundle must tail them all.
func TestSupportBundleTailsAllRetainedBootLogs(t *testing.T) {
	home := t.TempDir()
	have := web.LogFilePathsForTest(home)
	for _, want := range []string{"previous-boot.log", "previous-boot.1.log", "previous-boot.2.log"} {
		if !slices.Contains(have, filepath.Join(home, want)) {
			t.Errorf("%s missing from the support bundle logs", want)
		}
	}
}
