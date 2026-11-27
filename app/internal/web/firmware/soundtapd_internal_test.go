// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"compress/gzip"
	"debug/buildinfo"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stein155/soundtap/app/internal/oplock"
)

// appFixture is an app update wired to this test binary: the Handler, its update, the
// channel its restarts arrive on and the binary's bytes.
type appFixture struct {
	handler   *Handler
	update    *soundtapdUpdate
	restarted chan struct{}
	bin       []byte
}

// soundtapdServer runs the update against this test binary: it is the one ELF
// executable of the right architecture every test run has.
func soundtapdServer(t *testing.T) appFixture {
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

	info, err := buildinfo.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}

	// run in a directory of its own, as on the speaker: the running copy is on tmpfs, the
	// override on /mnt/nv. A test that breaks the store must not break the upload too.
	dir := t.TempDir()
	base := filepath.Join(dir, "base")

	run := filepath.Join(t.TempDir(), "run")
	for _, path := range []string{base, run} {
		err := os.WriteFile(path, bin, 0o755)
		if err != nil {
			t.Fatal(err)
		}
	}

	restarted := make(chan struct{}, 1)
	handler := New(slog.New(slog.DiscardHandler), "")
	handler.EnableSoundTapdUpdate(run, base, dir, "", func() { restarted <- struct{}{} })
	handler.soundtapd.main = info.Path

	return appFixture{handler: handler, update: handler.soundtapd, restarted: restarted, bin: bin}
}

// postSoundTapd uploads body with confirm as its X-SoundTap-Confirm, none when empty.
func postSoundTapd(t *testing.T, handler *Handler, body []byte, confirm string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, soundtapdUpdatePath, bytes.NewReader(body))
	if confirm != "" {
		req.Header.Set("X-SoundTap-Confirm", confirm)
	}

	rec := httptest.NewRecorder()
	handler.postSoundTapd(rec, req)

	return rec
}

//nolint:paralleltest // it runs an uploaded binary: a parallel fork can hold the file open (ETXTBSY)
func TestSoundTapdUploadRunsAndIsKeptForTheNextBoot(t *testing.T) {
	app := soundtapdServer(t)
	update := app.update

	upload := append(append([]byte{}, app.bin...), 0) // differs from base, still the same ELF
	if rec := postSoundTapd(t, app.handler, upload, confirmSoundTapd); rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	if got, _ := os.ReadFile(update.run); !bytes.Equal(got, upload) {
		t.Error("the running copy is not the upload")
	}

	stored, err := os.Open(update.override + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	defer stored.Close()

	zr, err := gzip.NewReader(stored)
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := io.ReadAll(zr); !bytes.Equal(got, upload) {
		t.Error("the stored override is not the upload")
	}

	want, _ := fileSHA256(update.base)
	if got, _ := os.ReadFile(update.override + ".base"); string(got) != want {
		t.Errorf(".base = %q, want the firmware's sha256 %q", got, want)
	}

	_, err = os.Stat(update.override + ".trial")
	if err != nil {
		t.Error("no .trial: a hang after this restart would survive the next boot")
	}

	select {
	case <-app.restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("no restart")
	}
}

func TestSoundTapdUploadRefusals(t *testing.T) {
	t.Parallel()

	// A nil body stands for the test binary itself, and an empty main for its own.
	cases := map[string]struct {
		body    []byte
		confirm string
		main    string
		code    int
	}{
		"unconfirmed":   {nil, "", "", 400},
		"not an ELF":    {[]byte("#!/bin/sh\n"), confirmSoundTapd, "", 422},
		"too big":       {make([]byte, soundtapdMaxSize+1), confirmSoundTapd, "", 400},
		"other program": {nil, confirmSoundTapd, "example.com/other", 422},
	}
	for name, tcase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			app := soundtapdServer(t)
			update := app.update

			body := tcase.body
			if body == nil {
				body = app.bin
			}

			if tcase.main != "" {
				update.main = tcase.main
			}

			if rec := postSoundTapd(t, app.handler, body, tcase.confirm); rec.Code != tcase.code {
				t.Fatalf("status %d, want %d: %s", rec.Code, tcase.code, rec.Body)
			}

			_, err := os.Stat(update.override + ".base")
			if err == nil {
				t.Error("a refused upload was stored")
			}

			_, err = os.Stat(update.run + ".new")
			if err == nil {
				t.Error("a refused upload was left in the run directory")
			}
		})
	}
}

//nolint:paralleltest // it runs an uploaded binary: a parallel fork can hold the file open (ETXTBSY)
func TestSoundTapdDeleteGoesBackToTheFirmwaresOwn(t *testing.T) {
	app := soundtapdServer(t)

	upload := append(append([]byte{}, app.bin...), 0)
	if rec := postSoundTapd(t, app.handler, upload, confirmSoundTapd); rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	<-app.restarted
	app.handler.firmware.mu.Unlock() // the restart would have ended the process

	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, soundtapdUpdatePath, http.NoBody)
	req.Header.Set("X-SoundTap-Confirm", "soundtapd")

	rec := httptest.NewRecorder()
	app.handler.deleteSoundTapd(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	if got, _ := os.ReadFile(app.update.run); !bytes.Equal(got, app.bin) {
		t.Error("the running copy is not the firmware's")
	}

	for _, suffix := range []string{".gz", ".base", ".trial"} {
		_, err := os.Stat(app.update.override + suffix)
		if err == nil {
			t.Errorf("%s left behind", suffix)
		}
	}

	<-app.restarted
}

// ADR 0035: an app built for a newer SoundTap Core than the speaker runs is refused. The test
// binary needs level 0 (it does not know -os-api), so an OS below 0 stands in for an old one.
//
//nolint:paralleltest // it runs an uploaded binary: a parallel fork can hold the file open (ETXTBSY)
func TestSoundTapdUploadRefusesAnAppForANewerOS(t *testing.T) {
	app := soundtapdServer(t)
	app.update.osAPI = filepath.Join(t.TempDir(), "os-api")

	err := os.WriteFile(app.update.osAPI, []byte("-1\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	rec := postSoundTapd(t, app.handler, app.bin, confirmSoundTapd)
	if rec.Code != 422 || !bytes.Contains(rec.Body.Bytes(), []byte("SoundTap Core level")) {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	err = os.WriteFile(app.update.osAPI, []byte("0\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	if rec := postSoundTapd(t, app.handler, app.bin, confirmSoundTapd); rec.Code != 200 {
		t.Fatalf("an app for this Core level was refused: %d %s", rec.Code, rec.Body)
	}
}

// Only an app from before the os-api contract may answer -os-api with an error; a crash,
// a hang or garbage refuses the upload rather than counting as "needs nothing".
//
//nolint:paralleltest // it runs an uploaded binary: a parallel fork can hold the file open (ETXTBSY)
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
	handler := New(nil, "")
	handler.EnableSoundTapdUpdate("", "", "", "", func() {})

	for name, tcase := range cases {
		t.Run(name, func(t *testing.T) { //nolint:paralleltest // it runs the script it wrote (ETXTBSY)
			path := filepath.Join(t.TempDir(), "app")

			err := os.WriteFile(path, []byte("#!/bin/sh\n"+tcase.script+"\n"), 0o755)
			if err != nil {
				t.Fatal(err)
			}

			got, err := handler.soundtapd.needs(t.Context(), path)
			if (err == nil) != tcase.ok || got != tcase.want {
				t.Fatalf("needs = %d, %v; want %d, ok=%v", got, err, tcase.want, tcase.ok)
			}
		})
	}
}

// A failed store keeps the override that was already there.
//
//nolint:paralleltest // it runs an uploaded binary: a parallel fork can hold the file open (ETXTBSY)
func TestSoundTapdFailedStoreKeepsTheStoredApp(t *testing.T) {
	app := soundtapdServer(t)
	update := app.update

	upload := filepath.Join(t.TempDir(), "upload")

	err := os.WriteFile(upload, app.bin, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = update.store(upload)
	if err != nil {
		t.Fatal(err)
	}

	before, _ := os.ReadFile(update.override + ".gz")
	// The next store cannot write: the override's directory is read-only.
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}

	dir := filepath.Dir(update.override)

	err = os.Chmod(dir, 0o555)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = os.Chmod(dir, 0o755) }()

	err = update.store(upload)
	if err == nil {
		t.Fatal("store succeeded in a read-only directory")
	}
	// The upload handler must not clear it either.
	differing := append(append([]byte{}, app.bin...), 0)
	if rec := postSoundTapd(t, app.handler, differing, confirmSoundTapd); rec.Code != 500 {
		t.Fatalf("upload with a failing store: status %d: %s", rec.Code, rec.Body)
	}

	if got, _ := os.ReadFile(update.override + ".gz"); !bytes.Equal(got, before) {
		t.Error("the stored app was lost")
	}

	_, err = os.Stat(update.override + ".base")
	if err != nil {
		t.Error("the stored app no longer counts: .base is gone")
	}
}

// An upload the speaker cannot write is its own failure: a 500 that does not name the
// staging path, not a 400 that blames the client.
//
//nolint:paralleltest // it changes the mode of the run directory under the handler
func TestSoundTapdUploadThatCannotBeWrittenIsAServerError(t *testing.T) {
	app := soundtapdServer(t)
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}

	dir := filepath.Dir(app.update.run)

	err := os.Chmod(dir, 0o555)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = os.Chmod(dir, 0o755) }()

	rec := postSoundTapd(t, app.handler, append(append([]byte{}, app.bin...), 0), confirmSoundTapd)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), dir) {
		t.Fatalf("status %d: %s; want 500 without the path", rec.Code, rec.Body)
	}
}

// receiveSoundTapd tells a write on the speaker (installError) from the client's body
// failing or being too large.
func TestReceiveSoundTapdTellsWhoFailed(t *testing.T) {
	t.Parallel()

	var failed installError

	_, err := receiveSoundTapd(strings.NewReader("x"), filepath.Join(t.TempDir(), "missing", "run.new"))
	if !errors.As(err, &failed) || strings.Contains(failed.msg, "missing") {
		t.Errorf("unwritable path: %v, want an installError without the path in its message", err)
	}

	_, err = receiveSoundTapd(iotest.ErrReader(io.ErrUnexpectedEOF), filepath.Join(t.TempDir(), "run.new"))
	if err == nil || errors.As(err, &failed) {
		t.Errorf("failing body: %v, want a client error", err)
	}

	sum, err := receiveSoundTapd(strings.NewReader("x"), filepath.Join(t.TempDir(), "run.new"))
	if err != nil || sum != "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881" {
		t.Errorf("good body: %q, %v", sum, err)
	}
}

// The self-update and a manual upload stage into different files, so neither can touch
// the other's bytes.
func TestSelfUpdateStagesApartFromAManualUpload(t *testing.T) {
	t.Parallel()

	app := soundtapdServer(t)
	if app.handler.StagingPath() == app.update.run+".new" {
		t.Fatalf("self-update stages into the upload's file %s", app.handler.StagingPath())
	}
}

// update.Manager holds the operations lock across the install. It is a flock, so a
// second Lock in this process would be refused and no update could ever land.
//
//nolint:paralleltest // it points the process-wide oplock.Path at its own lock file
func TestSelfUpdateInstallsUnderTheManagersLock(t *testing.T) {
	app := soundtapdServer(t)
	oplock.Path = filepath.Join(t.TempDir(), "operation.lock")
	t.Cleanup(func() { oplock.Path = "" })

	var manager oplock.Lock
	if !manager.TryLock() {
		t.Fatal("lock not free")
	}
	defer manager.Unlock()

	err := os.WriteFile(app.handler.StagingPath(), app.bin, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = app.handler.InstallApp(app.handler.StagingPath())
	if err != nil {
		t.Fatalf("install under the manager's lock: %v", err)
	}
}
