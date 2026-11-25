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

const (
	// executableMode is the mode of an app binary: the upload, its stored copy and the
	// firmware's own restored.
	executableMode = 0o755
	// storedMode is the mode of the stored override's files on /mnt/nv.
	storedMode = 0o644
	// coreLevelTimeout bounds the -os-api question to an upload.
	coreLevelTimeout = 5 * time.Second
	// flagExitCode is the exit status Go's flag package ends an unknown flag with.
	flagExitCode = 2
)

var (
	// errUploadTooLarge refuses an app upload over soundtapdMaxSize.
	errUploadTooLarge = errors.New("upload larger than")
	// errNotStagingPath refuses an InstallApp of anything but StagingPath.
	errNotStagingPath = errors.New("is not the staging path")
)

// wrongCPUError refuses an executable built for another CPU than the speaker's.
type wrongCPUError struct {
	class     elf.Class
	machine   elf.Machine
	wantClass elf.Class
	wantMach  elf.Machine
}

func (e wrongCPUError) Error() string {
	return fmt.Sprintf("built for %v %v, this speaker runs %v %v executables",
		e.class, e.machine, e.wantClass, e.wantMach)
}

// wrongMainError refuses a Go binary built from another main package than soundtapd's.
type wrongMainError struct{ path, want string }

func (e wrongMainError) Error() string { return fmt.Sprintf("built from %s, not %s", e.path, e.want) }

// coreLevelError refuses an app that needs a newer SoundTap Core than the speaker has.
type coreLevelError struct{ need, have int }

func (e coreLevelError) Error() string {
	return fmt.Sprintf("this app needs SoundTap Core level %d, the speaker has level %d: "+
		"install a newer SoundTap Core first", e.need, e.have)
}

// coreLevelAnswerError refuses an app whose answer to -os-api is not a level.
type coreLevelAnswerError struct{ answer string }

func (e coreLevelAnswerError) Error() string {
	return fmt.Sprintf("the app answered %q for its SoundTap Core level", e.answer)
}

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
	update := s.soundtapd
	if update == nil {
		apiError(w, http.StatusNotImplemented, "this build cannot replace soundtapd")

		return
	}
	// An older app only with the downgrade confirmation, as for an STU: it can bring
	// back a bug a newer release fixed (ADR 0048).
	want, code, err := s.soundtapdConfirm(r, update.version)
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

	tmp := update.run + ".new"

	sum, err := receiveSoundTapd(r.Body, tmp)
	if err != nil {
		_ = os.Remove(tmp)

		apiError(w, http.StatusBadRequest, err.Error())

		return
	}
	// Before it is executed, even for the -os-api probe in check.
	digest, _ := hex.DecodeString(sum) // receiveSoundTapd's own hex.EncodeToString: always decodes

	err = s.checkSignature(r, release.KindApp, digest)
	if err != nil {
		_ = os.Remove(tmp)

		apiErrorCode(w, http.StatusForbidden, apierror.SignatureInvalid, err.Error())

		return
	}

	// The probe outlives a browser that stops waiting: the upload is already in.
	err = update.install(context.WithoutCancel(r.Context()), tmp)
	if err != nil {
		var (
			refused refusedError
			failed  installError
		)
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

	s.writeJSON(w, map[string]any{"installed": true, "sha256": sum, "restarting": true})

	unlock = false

	update.restartSoon()
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
func (u *soundtapdUpdate) install(ctx context.Context, tmp string) error {
	defer os.Remove(tmp)

	err := u.check(ctx, tmp)
	if err != nil {
		return refusedError{err}
	}
	// Stored before it runs: a speaker that loses power in between boots the firmware's
	// own copy rather than one that cannot come back.
	// store cleans up after itself and keeps the override already there when it fails
	// before replacing it, so nothing is cleared here.
	err = u.store(tmp)
	if err != nil {
		return installError{"storing the new soundtapd failed", err}
	}

	err = os.Rename(tmp, u.run)
	if err != nil {
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
	update := s.soundtapd
	if path != s.StagingPath() {
		_ = os.Remove(path)

		return fmt.Errorf("%s %w", path, errNotStagingPath)
	}

	err := update.install(context.Background(), path)
	if err != nil {
		return err
	}

	update.restartSoon()

	return nil
}

// deleteSoundTapd goes back to the firmware's own soundtapd.
func (s *Handler) deleteSoundTapd(w http.ResponseWriter, r *http.Request) {
	update := s.soundtapd
	if update == nil {
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

	update.clear()

	err := copyFile(update.base, update.run)
	if err != nil {
		s.firmware.mu.Unlock()
		s.fail(w, "restoring the firmware's soundtapd failed; it returns at the next boot", err)

		return
	}

	s.writeJSON(w, map[string]any{"installed": false, "restarting": true})
	safego.Go("soundtapd.restart", func() {
		time.Sleep(time.Second)
		update.restart()
	})
}

func receiveSoundTapd(body io.Reader, path string) (string, error) {
	// Executable: it is probed with -os-api and then runs as the new app.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, executableMode)
	if err != nil {
		// The 400's text as it has always been worded: the path and what the system said.
		return "", err //nolint:wrapcheck // the text is the API's answer, unchanged
	}

	hasher := sha256.New()

	written, err := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(body, soundtapdMaxSize+1))
	if cerr := file.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}

	if written > soundtapdMaxSize {
		return "", fmt.Errorf("%w %d bytes", errUploadTooLarge, soundtapdMaxSize)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// check refuses anything but a soundtapd for this speaker's CPU: an executable that
// cannot start here leaves the speaker without its API until the rollback kicks in.
func (u *soundtapdUpdate) check(ctx context.Context, path string) error {
	upload, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("not an ELF executable: %w", err)
	}
	defer upload.Close()

	base, err := elf.Open(u.base)
	if err != nil {
		return fmt.Errorf("reading the firmware's soundtapd: %w", err)
	}
	defer base.Close()

	if upload.Class != base.Class || upload.Machine != base.Machine || upload.Type != elf.ET_EXEC {
		return wrongCPUError{
			class: upload.Class, machine: upload.Machine, wantClass: base.Class, wantMach: base.Machine,
		}
	}

	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("not a Go binary: %w", err)
	}

	if info.Path != u.main {
		return wrongMainError{path: info.Path, want: u.main}
	}

	need, err := u.needs(ctx, path)
	if err != nil {
		return err
	}

	if have := osapi.Have(u.osAPI); need > have {
		return coreLevelError{need: need, have: have}
	}

	return nil
}

// needs asks the upload which SoundTap Core level it was built for (ADR 0035). Only a build
// from before the contract, which Go's flag package turns away with exit status 2 and
// this message, needs nothing. Any other failure refuses the upload: a crash or a hang
// must not pass as "no requirement".
func (*soundtapdUpdate) needs(ctx context.Context, path string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, coreLevelTimeout)
	defer cancel()

	var stderr strings.Builder

	cmd := exec.CommandContext(ctx, path, "-os-api")
	cmd.Stderr = &stderr
	out, err := cmd.Output()

	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil && exit.ExitCode() == flagExitCode &&
		strings.Contains(stderr.String(), "flag provided but not defined: -os-api") {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("asking the app which SoundTap Core level it needs: %w", err)
	}

	level, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || level < 0 {
		return 0, coreLevelAnswerError{answer: strings.TrimSpace(string(out))}
	}

	return level, nil
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

	upload, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening the upload: %w", err)
	}
	defer upload.Close()
	// Default, not best: 32 KB larger on 12 MB, and far shorter on the ST10 core, where the
	// upload already sits in RAM next to the running copy.
	err = atomicjson.WriteStream(u.override+".gz", storedMode, func(w io.Writer) error {
		compressor, zerr := gzip.NewWriterLevel(w, gzip.DefaultCompression)
		if zerr != nil {
			return fmt.Errorf("starting the compression: %w", zerr)
		}

		_, zerr = io.Copy(compressor, upload)
		if zerr != nil {
			return fmt.Errorf("compressing the upload: %w", zerr)
		}

		return compressor.Close()
	})
	if err != nil {
		return fmt.Errorf("storing the compressed upload: %w", err)
	}
	// The old override is gone from here on: a failure leaves none, not a half one.
	// .trial is written by the boot that runs it too, but this process restarts it now,
	// and a hang from here on must also be undone by the next boot.
	err = os.WriteFile(u.override+".trial", nil, storedMode)
	if err == nil {
		err = atomicjson.WriteFile(u.override+".base", []byte(baseSum), storedMode)
	}

	if err != nil {
		u.clear()

		return fmt.Errorf("marking the stored upload: %w", err)
	}

	return nil
}

func (u *soundtapdUpdate) clear() {
	for _, suffix := range []string{".base", ".gz", ".trial"} {
		_ = os.Remove(u.override + suffix)
	}
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", path, err)
	}
	defer file.Close()

	hasher := sha256.New()

	_, err = io.Copy(hasher, file)
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", path, err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// copyFile replaces dst with a copy of src, executable, atomically (atomicjson.WriteStream).
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer source.Close()

	err = atomicjson.WriteStream(dst, executableMode, func(w io.Writer) error {
		_, cerr := io.Copy(w, source)
		if cerr != nil {
			return fmt.Errorf("copying: %w", cerr)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}

	return nil
}
