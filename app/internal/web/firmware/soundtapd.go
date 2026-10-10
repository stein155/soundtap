// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/osapi"
	"github.com/stein155/soundtap/app/internal/release"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/web/apierror"
)

// soundtapdUpdatePath replaces just the daemon, without a firmware install: the upload
// runs from /run/audio-bin at once and from /mnt/nv at every boot after, until another
// STU is installed. init.d/soundtap owns the boot side and the rollback; see there.
const soundtapdUpdatePath = "/api/soundtapd"

// soundtapdMaxSize bounds the upload. A soundtapd is about 9 MB.
const soundtapdMaxSize = 32 << 20

type soundtapdUpdate struct {
	run      string // the copy that runs: /run/audio-bin/app/soundtapd (ADR 0066)
	base     string // the firmware's own: /opt/soundtap/soundtapd
	override string // the stored upload, without its .gz/.base/.trial suffixes
	main     string // the Go main package an upload must have been built from
	osAPI    string // the SoundTap Core level file (osapi.Path)
	version  string // the running app's version, to tell an older upload
	restart  func()
}

// EnableSoundTapdUpdate wires POST and DELETE /api/soundtapd. restart must end this
// process so that init.d/soundtap starts the new binary.
func (s *Handler) EnableSoundTapdUpdate(run, base, home, version string, restart func()) {
	s.soundtapd = &soundtapdUpdate{
		run:      run,
		base:     base,
		override: filepath.Join(home, "soundtapd.override"),
		main:     "github.com/stein155/soundtap/app/cmd/soundtapd",
		osAPI:    osapi.Path,
		version:  strings.TrimPrefix(version, "app-v"),
		restart:  restart,
	}
}

// confirmSoundTapd is the X-SoundTap-Confirm value of an app upload.
const confirmSoundTapd = "soundtapd"

// soundtapdConfirm is the confirmation an upload needs, from the version its .sig
// names, or the status and error that refuse it.
func (s *Handler) soundtapdConfirm(r *http.Request, current string) (string, int, error) {
	if r.Header.Get(signatureHeader) == "" {
		return confirmSoundTapd, 0, nil
	}
	st, err := s.verifyStatement(r, release.KindApp)
	if err != nil {
		return "", http.StatusForbidden, err
	}
	version := st.Version
	if versionConfirm(version, current) == confirmInstall {
		return confirmSoundTapd, 0, nil
	}
	if r.Header.Get("X-SoundTap-Confirm") != confirmDowngrade {
		return "", http.StatusPreconditionRequired, versionRefusal(version, current, confirmDowngrade)
	}
	return confirmDowngrade, 0, nil
}

// postSoundTapd installs an uploaded soundtapd. The body is the raw binary.
func (s *Handler) postSoundTapd(w http.ResponseWriter, r *http.Request) {
	u := s.soundtapd
	if u == nil {
		apiError(w, http.StatusNotImplemented, "this build cannot replace soundtapd")
		return
	}
	// An older app only with the downgrade confirmation, as for an STU: it can bring
	// back a bug a newer release fixed (ADR 0048).
	want, code, err := s.soundtapdConfirm(r, u.version)
	if err != nil {
		if code == http.StatusForbidden {
			apiErrorCode(w, code, apierror.SignatureInvalid, err.Error())
		} else {
			apiError(w, code, err.Error())
		}
		return
	}
	if r.Header.Get("X-SoundTap-Confirm") != want {
		apiErrorCode(w, http.StatusBadRequest, apierror.ConfirmRequired, "soundtapd update confirmation required")
		return
	}
	if !s.firmware.mu.TryLock() {
		apiErrorCode(w, http.StatusConflict, apierror.Busy, "another firmware operation is in progress")
		return
	}
	unlock := true
	defer func() {
		if unlock {
			s.firmware.mu.Unlock()
		}
	}()

	if err := s.signaturePresent(r); err != nil {
		apiErrorCode(w, http.StatusForbidden, apierror.SignatureInvalid, err.Error())
		return
	}
	tmp := u.run + ".new"
	sum, err := receiveSoundTapd(r.Body, tmp)
	if err != nil {
		_ = os.Remove(tmp)
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Before it is executed, even for the -os-api probe in check.
	digest, _ := hex.DecodeString(sum) // receiveSoundTapd's own hex.EncodeToString: always decodes
	if err := s.checkSignature(r, release.KindApp, digest); err != nil {
		_ = os.Remove(tmp)
		apiErrorCode(w, http.StatusForbidden, apierror.SignatureInvalid, err.Error())
		return
	}
	if err := u.install(tmp); err != nil {
		var refused refusedError
		var failed installError
		switch {
		case errors.As(err, &refused):
			apiError(w, http.StatusUnprocessableEntity, err.Error())
		case errors.As(err, &failed):
			s.fail(w, failed.msg, failed.err)
		default:
			s.fail(w, "installing the new soundtapd failed", err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installed": true, "sha256": sum, "restarting": true})
	unlock = false
	u.restartSoon()
}

// refusedError is an upload that is not a soundtapd for this speaker.
type refusedError struct{ error }

// installError is a speaker-side failure: msg goes to the client, err to the log.
type installError struct {
	msg string
	err error
}

func (e installError) Error() string { return e.msg + ": " + e.err.Error() }
func (e installError) Unwrap() error { return e.err }

// install checks the verified binary at tmp, stores it and puts it where it runs. It
// removes tmp whatever happens.
func (u *soundtapdUpdate) install(tmp string) error {
	defer os.Remove(tmp)
	if err := u.check(tmp); err != nil {
		return refusedError{err}
	}
	// Stored before it runs: a speaker that loses power in between boots the firmware's
	// own copy rather than one that cannot come back.
	// store cleans up after itself and keeps the override already there when it fails
	// before replacing it, so nothing is cleared here.
	if err := u.store(tmp); err != nil {
		return installError{"storing the new soundtapd failed", err}
	}
	if err := os.Rename(tmp, u.run); err != nil {
		u.clear()
		return installError{"installing the new soundtapd failed", err}
	}
	return nil
}

func (u *soundtapdUpdate) restartSoon() {
	safego.Go("soundtapd.restart", func() {
		time.Sleep(time.Second)
		u.restart()
	})
}

// StagingPath is where the app self-update downloads to (update.AppInstaller), on the
// tmpfs the new binary runs from. Not the upload's ".new": the self-update downloads
// and verifies before it takes the operations lock, and a shared file let it and a
// manual upload overwrite or delete each other's bytes. Only the self-update,
// one at a time under update.Manager's lock, writes this one.
func (s *Handler) StagingPath() string { return s.soundtapd.run + ".update" }

// InstallApp installs a downloaded app release whose signature update has verified,
// through the same steps as an upload, and restarts into it. update.Manager holds the
// operations lock from before the download, so this takes none: a second Lock on the same
// file would never get it. The lock stays held: the restart ends this process.
func (s *Handler) InstallApp(path string) error {
	u := s.soundtapd
	if path != s.StagingPath() {
		_ = os.Remove(path)
		return fmt.Errorf("%s is not the staging path", path)
	}
	if err := u.install(path); err != nil {
		return err
	}
	u.restartSoon()
	return nil
}

// deleteSoundTapd goes back to the firmware's own soundtapd.
func (s *Handler) deleteSoundTapd(w http.ResponseWriter, r *http.Request) {
	u := s.soundtapd
	if u == nil {
		apiError(w, http.StatusNotImplemented, "this build cannot replace soundtapd")
		return
	}
	if r.Header.Get("X-SoundTap-Confirm") != confirmSoundTapd {
		apiError(w, http.StatusBadRequest, "soundtapd update confirmation required")
		return
	}
	if !s.firmware.mu.TryLock() {
		apiErrorCode(w, http.StatusConflict, apierror.Busy, "another firmware operation is in progress")
		return
	}
	u.clear()
	if err := copyFile(u.base, u.run); err != nil {
		s.firmware.mu.Unlock()
		s.fail(w, "restoring the firmware's soundtapd failed; it returns at the next boot", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installed": false, "restarting": true})
	safego.Go("soundtapd.restart", func() {
		time.Sleep(time.Second)
		u.restart()
	})
}

func receiveSoundTapd(body io.Reader, path string) (string, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, soundtapdMaxSize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}
	if n > soundtapdMaxSize {
		return "", fmt.Errorf("upload larger than %d bytes", soundtapdMaxSize)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// check refuses anything but a soundtapd for this speaker's CPU: an executable that
// cannot start here leaves the speaker without its API until the rollback kicks in.
func (u *soundtapdUpdate) check(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("not an ELF executable: %w", err)
	}
	defer f.Close()
	b, err := elf.Open(u.base)
	if err != nil {
		return fmt.Errorf("reading the firmware's soundtapd: %w", err)
	}
	defer b.Close()
	if f.Class != b.Class || f.Machine != b.Machine || f.Type != elf.ET_EXEC {
		return fmt.Errorf("built for %v %v, this speaker runs %v %v executables", f.Class, f.Machine, b.Class, b.Machine)
	}
	bi, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("not a Go binary: %w", err)
	}
	if bi.Path != u.main {
		return fmt.Errorf("built from %s, not %s", bi.Path, u.main)
	}
	need, err := u.needs(path)
	if err != nil {
		return err
	}
	if have := osapi.Have(u.osAPI); need > have {
		return fmt.Errorf("this app needs SoundTap Core level %d, the speaker has level %d: install a newer SoundTap Core first", need, have)
	}
	return nil
}

// needs asks the upload which SoundTap Core level it was built for (ADR 0035). Only a build
// from before the contract, which Go's flag package turns away with exit status 2 and
// this message, needs nothing. Any other failure refuses the upload: a crash or a hang
// must not pass as "no requirement".
func (u *soundtapdUpdate) needs(path string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, path, "-os-api")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil && exit.ExitCode() == 2 &&
		strings.Contains(stderr.String(), "flag provided but not defined: -os-api") {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("asking the app which SoundTap Core level it needs: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("the app answered %q for its SoundTap Core level", strings.TrimSpace(string(out)))
	}
	return n, nil
}

// store keeps the upload for the next boots, gzipped: the ST20 shares /mnt/nv with
// stock and has a few MB left. The .base file is written last and is what makes the
// override count. A stored override stays in place until the new one is complete on
// disk, so a failed upload keeps the app the speaker already had.
func (u *soundtapdUpdate) store(path string) error {
	baseSum, err := fileSHA256(u.base)
	if err != nil {
		return err
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	// Default, not best: 32 KB larger on 12 MB, and far shorter on the ST10 core, where the
	// upload already sits in RAM next to the running copy.
	err = atomicjson.WriteStream(u.override+".gz", 0o644, func(w io.Writer) error {
		zw, err := gzip.NewWriterLevel(w, gzip.DefaultCompression)
		if err != nil {
			return err
		}
		if _, err := io.Copy(zw, in); err != nil {
			return err
		}
		return zw.Close()
	})
	if err != nil {
		return err
	}
	// The old override is gone from here on: a failure leaves none, not a half one.
	// .trial is written by the boot that runs it too, but this process restarts it now,
	// and a hang from here on must also be undone by the next boot.
	err = os.WriteFile(u.override+".trial", nil, 0o644)
	if err == nil {
		err = atomicjson.WriteFile(u.override+".base", []byte(baseSum), 0o644)
	}
	if err != nil {
		u.clear()
	}
	return err
}

func (u *soundtapdUpdate) clear() {
	for _, suffix := range []string{".base", ".gz", ".trial"} {
		_ = os.Remove(u.override + suffix)
	}
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyFile replaces dst with a copy of src, executable, atomically (atomicjson.WriteStream).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return atomicjson.WriteStream(dst, 0o755, func(w io.Writer) error {
		_, err := io.Copy(w, in)
		return err
	})
}
