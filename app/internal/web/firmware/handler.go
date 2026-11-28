// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package firmware is the install lane of the speaker's HTTP API (ADR 0091): STU
// uploads and NAND installs (/api/firmware), app updates (/api/soundtapd), reboot and
// boot confirmation. internal/web owns the router and hands these routes to a Handler.
package firmware

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/oplock"
	"github.com/stein155/soundtap/app/internal/web/apierror"
)

// Paths the router needs for its body limits and auth decisions.
const (
	UploadPath    = firmwareUploadPath
	AppUpdatePath = soundtapdUpdatePath
	AppMaxSize    = soundtapdMaxSize
)

// Handler serves the install lane. The zero value refuses every install: the
// speaker-only hooks are wired by soundtapd through the setters.
type Handler struct {
	log     *slog.Logger
	homeDir string

	// rebootFn reboots into the installed slot.
	rebootFn func() error
	// bootGuardFn confirms this boot to the boot guard.
	bootGuardFn      func(envCount uint32, settle func(fallback bool) error) error
	firmwareActiveFn func(bool) // tells the LED loop a flash started or ended
	firmwareFailedFn func()     // an install that started has failed

	firmwareProgressFn func(int) // hears an install's percentage
	// installPct is an install's percentage plus one while one runs with a known size, and
	// 0 otherwise (Progress).
	installPct atomic.Int64

	firmware  firmwareState    // serialises .stu uploads
	soundtapd *soundtapdUpdate // nil: POST /api/soundtapd refuses

	// bootConfirmed is set once ConfirmBoot succeeded in this process (GET /api/version).
	bootConfirmed atomic.Bool

	// flashPlan reads this speaker's own NAND layout. nil off-device, where an
	// install is refused rather than simulated.
	flashPlan flashPlan
	// firmwareModel resolves immutable hardware identity independently of writable
	// settings. nil uses /proc/variant and the exact variant= kernel argument.
	firmwareModel func() (string, error)
	// flashTools reports which of the mtd-utils commands internal/flash needs are
	// absent from this image. nil means "ask the running system".
	flashTools func() []string
	// flashRunner overrides how flash commands are run. nil means exec; only
	// tests set it, because the alternative is a test that erases a partition.
	flashRunner flash.Command
	// flashReader reads programmed NAND back; tests replace it with fake NAND.
	flashReader func(string, ...string) ([]byte, error)
	// flashOpen streams a written UBI volume back; nil is os.Open, tests read fake NAND.
	flashOpen func(string) (io.ReadCloser, error)
	// rootRun runs the NAND commands through soundtap-root when soundtapd is not root
	// (ADR 0066); nil runs them here.
	rootRun RootRun

	// bootChain is the boot chain on NAND as BootChain first read it.
	bootChainOnce sync.Once
	bootChain     install.BootChain

	// releaseKeys are the keys a package's .sig must verify against; nil is
	// release.PublicKeys. Tests sign with their own pair.
	releaseKeys []string
	// osVersionFile holds the running SoundTap version; empty is defaultOSVersionFile.
	osVersionFile string
	// codecMarker is the boot's codec-only-in-other-slot marker; empty is
	// defaultCodecMarker.
	codecMarker string
	// restartDelay lets an install's answer reach the browser before the speaker goes
	// away; 0 is defaultRestartDelay, and tests set a nanosecond so they need not wait.
	restartDelay time.Duration
}

// RootRun runs one flash command (flash.Command's shape) elsewhere, passing lock, the
// operations lock, along: rootd.Client.Run.
type RootRun func(lock *os.File, stdin io.Reader, name string, args ...string) error

// SetRootRun sends the lane's NAND writes to soundtap-root.
func (s *Handler) SetRootRun(fn RootRun) { s.rootRun = fn }

// New returns the install lane for a speaker whose persistent state lives in homeDir.
func New(log *slog.Logger, homeDir string) *Handler {
	return &Handler{
		log:                log,
		homeDir:            homeDir,
		rebootFn:           nil,
		bootGuardFn:        nil,
		firmwareActiveFn:   nil,
		firmwareFailedFn:   nil,
		firmwareProgressFn: nil,
		installPct:         atomic.Int64{},
		firmware:           firmwareState{mu: oplock.Lock{}},
		soundtapd:          nil,
		bootConfirmed:      atomic.Bool{},
		flashPlan:          nil,
		firmwareModel:      nil,
		flashTools:         nil,
		flashRunner:        nil,
		flashReader:        nil,
		flashOpen:          nil,
		rootRun:            nil,
		bootChainOnce:      sync.Once{},
		bootChain:          install.BootChain{SPL: "", UBoot: "", UBootSpare: ""},
		releaseKeys:        nil,
		osVersionFile:      "",
		codecMarker:        "",
		restartDelay:       0,
	}
}

// Mux is where Register adds routes: an *http.ServeMux, or the web server's own mux.
type Mux interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// Register adds the install lane's routes to mux.
func (s *Handler) Register(mux Mux) {
	mux.HandleFunc("POST "+firmwareUploadPath, s.postFirmware)
	mux.HandleFunc("POST "+soundtapdUpdatePath, s.postSoundTapd)
	mux.HandleFunc("DELETE "+soundtapdUpdatePath, s.deleteSoundTapd)
	mux.HandleFunc("POST /api/reboot", s.reboot)
}

// EnableInstall lets POST /api/firmware?install=1 write NAND with plan.
func (s *Handler) EnableInstall(plan func() (flash.Target, error)) { s.flashPlan = plan }

// SetReboot wires POST /api/reboot.
func (s *Handler) SetReboot(fn func() error) { s.rebootFn = fn }

// SetBootGuard wires boot confirmation to the boot guard.
func (s *Handler) SetBootGuard(fn func(envCount uint32, settle func(fallback bool) error) error) {
	s.bootGuardFn = fn
}

// SetActiveFn is called with true when a flash starts and false when it ends.
func (s *Handler) SetActiveFn(fn func(bool)) { s.firmwareActiveFn = fn }

// SetFailedFn is called after an install that started ends with an error, so the speaker
// itself can say so: the error otherwise reaches only the browser that sent the package.
func (s *Handler) SetFailedFn(fn func()) { s.firmwareFailedFn = fn }

// statusRecorder remembers the status an install answered with.
type statusRecorder struct {
	http.ResponseWriter

	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// Progress is how far the install under way is, in percent (0-99), and false when none
// runs or its size is unknown: GET /api/version shows it to the page that follows an update.
func (s *Handler) Progress() (int, bool) {
	v := s.installPct.Load()

	return int(v - 1), v > 0
}

// SetProgressFn is called with an install's percentage each time it moves.
func (s *Handler) SetProgressFn(fn func(int)) { s.firmwareProgressFn = fn }

// TryLock takes the install lock without waiting. Anything that rewrites what an
// install reads or writes (a backup restore, a factory reset) holds it.
func (s *Handler) TryLock() bool { return s.firmware.mu.TryLock() }

// Unlock releases what TryLock took.
func (s *Handler) Unlock() { s.firmware.mu.Unlock() }

// apiError is the API's one error shape (ADR 0091), as internal/web writes it.
func apiError(w http.ResponseWriter, status int, msg string) {
	apierror.Write(w, status, apierror.ForStatus(status), msg)
}

// apiErrorCode is apiError with a code more precise than the status implies.
func apiErrorCode(w http.ResponseWriter, status int, code apierror.Code, msg string) {
	apierror.Write(w, status, code, msg)
}

// Model is this speaker's hardware model as the installer resolves it.
func (s *Handler) Model() (string, error) { return s.hardwareFirmwareModel() }

// SetModel overrides how Model resolves the hardware model; tests use it.
func (s *Handler) SetModel(fn func() (string, error)) { s.firmwareModel = fn }

// fail reports a failure on the speaker's side, as internal/web's fail does.
func (s *Handler) fail(w http.ResponseWriter, msg string, err error) {
	code := http.StatusInternalServerError

	if err != nil {
		s.logger().Warn(msg, "err", err.Error())

		if errors.Is(err, context.DeadlineExceeded) {
			code = http.StatusGatewayTimeout
		}

		if errors.Is(err, syscall.ENOSPC) {
			code = http.StatusInsufficientStorage
			msg += ": the speaker's storage is full"
		}
	}

	apiError(w, code, msg)
}

func (s *Handler) badRequest(w http.ResponseWriter, msg string, err error) {
	if err != nil {
		s.logger().Warn(msg, "err", err.Error())
	}
	// A body over the route's limit is a 413, not a 400.
	apierror.RefuseBody(w, msg, err)
}

// logger tolerates the zero Handler the tests build.
func (s *Handler) logger() *slog.Logger {
	if s.log == nil {
		return slog.New(slog.DiscardHandler)
	}

	return s.log
}

// writeJSON answers w with v and 200 OK. An encoding that fails halfway has already
// sent the status, so all that is left to do is to log it.
func (s *Handler) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	err := json.NewEncoder(w).Encode(v)
	if err != nil {
		s.logger().Warn("writing the answer", "err", err)
	}
}
