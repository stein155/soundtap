// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"compress/gzip"
	"debug/buildinfo"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/oplock"
)

// soundtapdServer runs the update against this test binary: it is the one ELF
// executable of the right architecture every test run has.
func soundtapdServer(t *testing.T) (*Handler, *soundtapdUpdate, chan struct{}, []byte) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the upload check wants an ELF; this test binary is not one")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := buildinfo.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "base")
	run := filepath.Join(dir, "run")
	for _, p := range []string{base, run} {
		if err := os.WriteFile(p, bin, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	restarted := make(chan struct{}, 1)
	s := &Handler{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.EnableSoundTapdUpdate(run, base, dir, "", func() { restarted <- struct{}{} })
	s.soundtapd.main = bi.Path
	return s, s.soundtapd, restarted, bin
}

func postSoundTapd(s *Handler, body []byte, confirm bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", soundtapdUpdatePath, bytes.NewReader(body))
	if confirm {
		req.Header.Set("X-SoundTap-Confirm", "soundtapd")
	}
	rec := httptest.NewRecorder()
	s.postSoundTapd(rec, req)
	return rec
}

func TestSoundTapdUploadRunsAndIsKeptForTheNextBoot(t *testing.T) {
	s, u, restarted, bin := soundtapdServer(t)
	upload := append(append([]byte{}, bin...), 0) // differs from base, still the same ELF
	if rec := postSoundTapd(s, upload, true); rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got, _ := os.ReadFile(u.run); !bytes.Equal(got, upload) {
		t.Error("the running copy is not the upload")
	}
	f, err := os.Open(u.override + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := io.ReadAll(zr); !bytes.Equal(got, upload) {
		t.Error("the stored override is not the upload")
	}
	want, _ := fileSHA256(u.base)
	if got, _ := os.ReadFile(u.override + ".base"); string(got) != want {
		t.Errorf(".base = %q, want the firmware's sha256 %q", got, want)
	}
	if _, err := os.Stat(u.override + ".trial"); err != nil {
		t.Error("no .trial: a hang after this restart would survive the next boot")
	}
	select {
	case <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("no restart")
	}
}

func TestSoundTapdUploadRefusals(t *testing.T) {
	s, u, _, bin := soundtapdServer(t)
	cases := map[string]struct {
		body    []byte
		confirm bool
		main    string
		code    int
	}{
		"unconfirmed":   {bin, false, u.main, 400},
		"not an ELF":    {[]byte("#!/bin/sh\n"), true, u.main, 422},
		"too big":       {make([]byte, soundtapdMaxSize+1), true, u.main, 400},
		"other program": {bin, true, "example.com/other", 422},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			u.main = c.main
			if rec := postSoundTapd(s, c.body, c.confirm); rec.Code != c.code {
				t.Fatalf("status %d, want %d: %s", rec.Code, c.code, rec.Body)
			}
			if _, err := os.Stat(u.override + ".base"); err == nil {
				t.Error("a refused upload was stored")
			}
			if _, err := os.Stat(u.run + ".new"); err == nil {
				t.Error("a refused upload was left in the run directory")
			}
		})
	}
}

func TestSoundTapdDeleteGoesBackToTheFirmwaresOwn(t *testing.T) {
	s, u, restarted, bin := soundtapdServer(t)
	if rec := postSoundTapd(s, append(append([]byte{}, bin...), 0), true); rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	<-restarted
	s.firmware.mu.Unlock() // the restart would have ended the process
	req := httptest.NewRequest("DELETE", soundtapdUpdatePath, nil)
	req.Header.Set("X-SoundTap-Confirm", "soundtapd")
	rec := httptest.NewRecorder()
	s.deleteSoundTapd(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got, _ := os.ReadFile(u.run); !bytes.Equal(got, bin) {
		t.Error("the running copy is not the firmware's")
	}
	for _, suffix := range []string{".gz", ".base", ".trial"} {
		if _, err := os.Stat(u.override + suffix); err == nil {
			t.Errorf("%s left behind", suffix)
		}
	}
	<-restarted
}

// ADR 0035: an app built for a newer SoundTap Core than the speaker runs is refused. The test
// binary needs level 0 (it does not know -os-api), so an OS below 0 stands in for an old one.
func TestSoundTapdUploadRefusesAnAppForANewerOS(t *testing.T) {
	s, u, _, bin := soundtapdServer(t)
	u.osAPI = filepath.Join(t.TempDir(), "os-api")
	if err := os.WriteFile(u.osAPI, []byte("-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := postSoundTapd(s, bin, true); rec.Code != 422 || !bytes.Contains(rec.Body.Bytes(), []byte("SoundTap Core level")) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if err := os.WriteFile(u.osAPI, []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := postSoundTapd(s, bin, true); rec.Code != 200 {
		t.Fatalf("an app for this Core level was refused: %d %s", rec.Code, rec.Body)
	}
}

// Only an app from before the os-api contract may answer -os-api with an error; a crash,
// a hang or garbage refuses the upload rather than counting as "needs nothing".
func TestSoundTapdNeedsRefusesWhatItCannotRead(t *testing.T) {
	cases := map[string]struct {
		script string
		want   int
		ok     bool
	}{
		"level":        {"echo 3", 3, true},
		"pre-ADR app":  {"echo 'flag provided but not defined: -os-api' >&2; exit 2", 0, true},
		"crash":        {"exit 1", 0, false},
		"other exit 2": {"echo boom >&2; exit 2", 0, false},
		"garbage":      {"echo level three", 0, false},
		"negative":     {"echo -1", 0, false},
		"silent":       {"true", 0, false},
	}
	u := &soundtapdUpdate{}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "app")
			if err := os.WriteFile(p, []byte("#!/bin/sh\n"+c.script+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			got, err := u.needs(p)
			if (err == nil) != c.ok || got != c.want {
				t.Fatalf("needs = %d, %v; want %d, ok=%v", got, err, c.want, c.ok)
			}
		})
	}
}

// A failed store keeps the override that was already there.
func TestSoundTapdFailedStoreKeepsTheStoredApp(t *testing.T) {
	s, u, _, bin := soundtapdServer(t)
	upload := filepath.Join(t.TempDir(), "upload")
	if err := os.WriteFile(upload, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := u.store(upload); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(u.override + ".gz")
	// The next store cannot write: the override's directory is read-only.
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	dir := filepath.Dir(u.override)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o755) }()
	if err := u.store(upload); err == nil {
		t.Fatal("store succeeded in a read-only directory")
	}
	// The upload handler must not clear it either.
	if rec := postSoundTapd(s, append(append([]byte{}, bin...), 0), true); rec.Code != 500 {
		t.Fatalf("upload with a failing store: status %d: %s", rec.Code, rec.Body)
	}
	if got, _ := os.ReadFile(u.override + ".gz"); !bytes.Equal(got, before) {
		t.Error("the stored app was lost")
	}
	if _, err := os.Stat(u.override + ".base"); err != nil {
		t.Error("the stored app no longer counts: .base is gone")
	}
}

// The self-update and a manual upload stage into different files, so neither can touch
// the other's bytes.
func TestSelfUpdateStagesApartFromAManualUpload(t *testing.T) {
	s, u, _, _ := soundtapdServer(t)
	if s.StagingPath() == u.run+".new" {
		t.Fatalf("self-update stages into the upload's file %s", s.StagingPath())
	}
}

// update.Manager holds the operations lock across the install. It is a flock, so a
// second Lock in this process would be refused and no update could ever land.
func TestSelfUpdateInstallsUnderTheManagersLock(t *testing.T) {
	s, _, _, bin := soundtapdServer(t)
	oplock.Path = filepath.Join(t.TempDir(), "operation.lock")
	t.Cleanup(func() { oplock.Path = "" })
	var manager oplock.Lock
	if !manager.TryLock() {
		t.Fatal("lock not free")
	}
	defer manager.Unlock()
	if err := os.WriteFile(s.StagingPath(), bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.InstallApp(s.StagingPath()); err != nil {
		t.Fatalf("install under the manager's lock: %v", err)
	}
}
