// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stein155/soundtap/app/internal/release"
)

var (
	// errGitHubDown is a release download that never started.
	errGitHubDown = errors.New("github down")
	// errStaysUp is a reboot that does not happen in a test.
	errStaysUp = errors.New("stays up in the test")
)

// A Core release the speaker downloads itself goes through the upload's install: the
// signature before the first write, the version against the running one, the slot.
func TestInstallStreamIsTheUploadInstall(t *testing.T) {
	t.Parallel()

	keys, priv := testKeys(t)
	_, stranger := testKeys(t)
	versionFile := runningVersionFile(t, "0.1.0")
	pkg := universalST10Package(t)
	sum := sha256.Sum256(pkg)
	sign := func(k ed25519.PrivateKey, version string) string {
		return signed(t, k, release.KindCore, release.ProfileProduction, version, sum[:])
	}
	install := func(sig string) (*httptest.ResponseRecorder, *fakeFlash) {
		nand := newFakeFlash()
		handler := wlaInstallServer(t, nand, "root=ubi0:rootfs ubi.mtd=9,2048") // running PONG
		handler.firmwareModel = func() (string, error) { return "st10", nil }
		handler.releaseKeys, handler.osVersionFile = keys, versionFile
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/core-update", nil)

		err := handler.InstallStream(rec, req, func() (io.ReadCloser, int64, string, error) {
			return io.NopCloser(bytes.NewReader(pkg)), int64(len(pkg)), sig, nil
		})
		if err != nil {
			t.Fatal(err)
		}

		return rec, nand
	}

	rec, nand := install(sign(priv, "0.2.0"))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"installed":true`) {
		t.Fatalf("newer signed release = %d %s", rec.Code, rec.Body)
	}

	if !strings.Contains(strings.Join(nand.cmds, "\n"), "fw_setenv -c") {
		t.Fatal("the installed slot was not selected")
	}

	for name, sig := range map[string]string{
		"signed as older":  sign(priv, "0.0.9"),
		"signed as stock":  sign(priv, "stock"),
		"another key":      sign(stranger, "0.2.0"),
		"signed as an app": signed(t, priv, release.KindApp, release.ProfileProduction, "0.2.0", sum[:]),
	} {
		if rec, nand := install(sig); rec.Code < 400 || len(nand.cmds) != 0 {
			t.Errorf("%s: status %d, NAND %v; want a refusal and nothing written", name, rec.Code, nand.cmds)
		}
	}
}

// A package the speaker fetches itself installs only signed: the unsigned route is the
// upload behind the press (ADR 0048).
func TestInstallStreamRefusesAnUnsignedPackage(t *testing.T) {
	t.Parallel()

	nand := newFakeFlash()
	s := wlaInstallServer(t, nand, "root=ubi0:rootfs ubi.mtd=9,2048")
	pkg := universalST10Package(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/core-update", nil)
	rec := httptest.NewRecorder()

	err := s.InstallStream(rec, req, func() (io.ReadCloser, int64, string, error) {
		return io.NopCloser(bytes.NewReader(pkg)), int64(len(pkg)), "", nil
	})
	if !errors.Is(err, errUnsigned) || len(nand.cmds) != 0 {
		t.Fatalf("unsigned stream: err %v, NAND %v; want errUnsigned and nothing written", err, nand.cmds)
	}
}

func TestInstallStreamAnswersNothingItDidNotStart(t *testing.T) {
	t.Parallel()

	handler := installServer(t, newFakeFlash())
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/core-update", nil)
	upstream := errGitHubDown
	rec := httptest.NewRecorder()

	err := handler.InstallStream(rec, req, func() (io.ReadCloser, int64, string, error) { return nil, 0, "", upstream })
	if !errors.Is(err, upstream) || rec.Body.Len() != 0 {
		t.Fatalf("open failed: err %v, body %q", err, rec.Body)
	}

	if !handler.TryLock() {
		t.Fatal("the install lock was kept after a failed open")
	}
	defer handler.Unlock()

	opened := false

	err = handler.InstallStream(rec, req, func() (io.ReadCloser, int64, string, error) {
		opened = true

		return nil, 0, "", nil
	})
	if !errors.Is(err, ErrInstallBusy) || opened {
		t.Fatalf("while locked: err %v, opened %v; want ErrInstallBusy and no download", err, opened)
	}
}

// One tap updates Core: after the new slot is written and selected and the report saved,
// the speaker restarts into it on its own. It never restarts after a refusal, nor when the
// report could not be saved, and it keeps the install lock until the restart.
func TestInstallStreamRestartsOnlyAfterASuccessfulInstall(t *testing.T) {
	t.Parallel()

	keys, priv := testKeys(t)
	versionFile := runningVersionFile(t, "0.1.0")
	pkg := universalST10Package(t)
	sum := sha256.Sum256(pkg)

	for _, tcase := range []struct {
		name, version string
		noHome        bool
		restart       bool
	}{
		{"newer release", "0.2.0", false, true},
		{"refused as older", "0.0.9", false, false},
		{"report not saved", "0.2.0", true, false},
	} {
		handler := wlaInstallServer(t, newFakeFlash(), "root=ubi0:rootfs ubi.mtd=9,2048")
		handler.releaseKeys, handler.osVersionFile, handler.restartDelay = keys, versionFile, time.Nanosecond

		handler.firmwareModel = func() (string, error) { return "st10", nil }
		if tcase.noHome {
			handler.homeDir = ""
		}

		restarted := make(chan struct{}, 1)

		handler.SetReboot(func() error {
			restarted <- struct{}{}

			return errStaysUp
		})

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/core-update", nil)

		sig := signed(t, priv, release.KindCore, release.ProfileProduction, tcase.version, sum[:])

		err := handler.InstallStream(rec, req, func() (io.ReadCloser, int64, string, error) {
			return io.NopCloser(bytes.NewReader(pkg)), int64(len(pkg)), sig, nil
		})
		if err != nil {
			t.Fatal(err)
		}

		if got := strings.Contains(rec.Body.String(), `"restarting":true`); got != tcase.restart {
			t.Errorf("%s: restarting in the report %v, want %v: %s", tcase.name, got, tcase.restart, rec.Body)
		}

		if !tcase.restart {
			if handler.TryLock() {
				handler.Unlock()
			} else {
				t.Errorf("%s: the install lock was kept without a restart", tcase.name)
			}
			select {
			case <-restarted:
				t.Errorf("%s: the speaker restarted", tcase.name)
			default:
			}

			continue
		}

		<-restarted
		// The restart failed in the test, so the lock is given back for the next install.
		if !handler.lockSoon() {
			t.Fatalf("%s: the install lock was not given back after a failed restart", tcase.name)
		}
		handler.Unlock()
	}
}
