// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stein155/soundtap/app/internal/atomicjson"
	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/oplock"
	"github.com/stein155/soundtap/app/internal/safego"
	"github.com/stein155/soundtap/app/internal/stu"
	"github.com/stein155/soundtap/app/internal/web/apierror"
)

// Firmware upload and install. The speaker cannot store a ~96 MB package
// anywhere — 128 MB of RAM, a read-only rootfs and a 38 MB persistent partition
// — so the upload is never staged whole: it is validated as it streams, the way
// the stock installer consumes one, and the two payloads that have to reach NAND
// are written out module by module as they go past.
//
// GET-shaped default is "tell me what this is": POST without ?install=1 reports
// the package and writes nothing. ?install=1 writes the slot the speaker is not
// running from. See internal/flash for what the stock ARMUpdate does and why the
// order here is the order it is.
const firmwareUploadPath = "/api/firmware"

// firmwareState serialises uploads. Two 96 MB streams at once on this box is not
// a race to reason about, it is an out-of-memory — and two installs at once is
// two writers on one flash partition.
// mu is the speaker-wide operations lock (ADR 0052), shared with the app updater,
// restore and factory reset through its lock file. Uploads that only ask what a package is
// take no lock: a probe is open to the LAN and may trickle in for its whole read deadline,
// so a lock would let one stranger hold off the install and the UI's check.
// A probe is scanned as it streams and never stored, so any number stay small.
type firmwareState struct {
	mu oplock.Lock
}

type firmwareModule struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type Report struct {
	Size        int64            `json:"size"`
	Modules     []firmwareModule `json:"modules"`
	Kernel      string           `json:"kernel"` // "mainline" | "stock"
	Models      []string         `json:"models"`
	Installable bool             `json:"installable"`
	Installed   bool             `json:"installed,omitempty"`
	Slot        string           `json:"slot,omitempty"`        // slot written
	RunningSlot string           `json:"runningSlot,omitempty"` // slot still booting
	Reason      string           `json:"reason,omitempty"`
	// Version is what the signature says the package is ("stock" for Bose's firmware), and
	// Confirm the X-SoundTap-Confirm an install of it needs here.
	Version string `json:"version,omitempty"`
	Confirm string `json:"confirm,omitempty"`
}

func (s *Handler) persistFirmwarePending(slot string, rep Report) error {
	if s.homeDir == "" {
		return errors.New("firmware recovery state has no home")
	}
	p := install.Pending{TargetSlot: slot}
	p.Package.Size, p.Package.Kernel, p.Package.Models = rep.Size, rep.Kernel, rep.Models
	return atomicjson.Write(filepath.Join(s.homeDir, install.PendingPath), p, 0o600)
}

// flashPlan is what the running speaker can tell us about its own NAND. It is a
// field so a test can install one; off-device it is nil and installs are refused.
type flashPlan func() (flash.Target, error)

// flashRun runs one mtd-utils command, using the binaries already in the image.
func (s *Handler) flashRun(stdin io.Reader, name string, args ...string) error {
	if s.rootRun != nil {
		// soundtapd's own user cannot write NAND: soundtap-root runs the command (ADR 0066),
		// holding the operations lock as the command here would.
		if err := s.rootRun(s.firmware.mu.File(), stdin, name, args...); err != nil {
			return err
		}
		if s.log != nil {
			s.log.Info("flash", "cmd", name, "args", args, "via", "soundtap-root")
		}
		return nil
	}
	// Long, but not unbounded: erasing and rewriting a 96 MiB NAND partition is
	// minutes of work, and a wedged writer must not hold the speaker forever.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	// The writer carries the operations lock: if soundtapd stops halfway, a restarted one
	// finds the lock taken until this command is done with the NAND.
	if f := s.firmware.mu.File(); f != nil {
		cmd.ExtraFiles = []*os.File{f}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, out)
	}
	if s.log != nil {
		s.log.Info("flash", "cmd", name, "args", args, "out", string(out))
	}
	return nil
}

func (s *Handler) flashRead(name string, args ...string) ([]byte, error) {
	return install.ReadNAND(name, args...)
}

// postFirmware validates an uploaded .stu and, with ?install=1, installs it. The
// body is the raw package; no multipart, because parsing a multipart envelope
// around a 96 MB stream buys nothing here.
func (s *Handler) postFirmware(w http.ResponseWriter, r *http.Request) {
	switch r.Header.Get("X-SoundTap-Confirm") {
	case confirmInstall, confirmDowngrade, confirmStock:
	default:
		if r.URL.Query().Get("install") == "1" {
			apiError(w, http.StatusBadRequest, "firmware install confirmation required")
			return
		}
	}
	if r.URL.Query().Get("install") != "1" {
		pkg, err := stu.Scan(r.Body, nil)
		if err != nil {
			s.rejectPackage(w, err)
			return
		}
		rep := s.describe(pkg)
		if version, confirm, err := s.packageVersion(r); err == nil {
			rep.Version, rep.Confirm = version, confirm
		}
		// Bose's own package without our signature: it installs only as the way back.
		if r.Header.Get(signatureHeader) == "" && boseFirmware(pkg) {
			rep.Version, rep.Confirm = stockVersion, confirmStock
		}
		writeJSON(w, http.StatusOK, rep)
		return
	}
	if !s.firmware.mu.TryLock() {
		apiErrorCode(w, http.StatusConflict, apierror.Busy, "another firmware operation is in progress")
		return
	}
	defer s.firmware.mu.Unlock()
	s.installFirmware(w, r)
}

// rejectPackage answers a package that is not one. The reason is the whole value
// of this endpoint: "rejected" without saying which module failed sends people
// back to re-downloading at random.
func (s *Handler) rejectPackage(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, stu.ErrNotSTU) {
		code = http.StatusUnsupportedMediaType
	}
	apiErrorCode(w, code, apierror.PackageRejected, err.Error())
}

func (s *Handler) describe(pkg *stu.Package) Report {
	rep := Report{
		Size: pkg.Size, Kernel: "stock", Models: make([]string, len(pkg.Targets)),
	}
	for i, model := range pkg.Targets {
		rep.Models[i] = strings.ToUpper(model)
	}
	if pkg.Mainline {
		rep.Kernel, rep.Models = "mainline", []string{strings.ToUpper(pkg.KernelTarget)}
		// A universal package's kernels say which speakers it is for by their suffix;
		// the uImage name only says which model the build was driven from.
		if models := install.SuffixedKernelModels(pkg); len(models) > 0 {
			rep.Models = models
		}
	}
	for _, m := range pkg.Modules {
		rep.Modules = append(rep.Modules, firmwareModule{Name: m.Name, Size: m.Payload()})
	}
	rep.Installable = s.flashPlan != nil
	if rep.Installable {
		model, err := s.hardwareFirmwareModel()
		if err == nil {
			err = install.PackageRefusal(model, pkg)
		}
		if err == nil {
			err = install.ToolsRefusal(s.missingFlashTools())
		}
		if err == nil {
			// Ask the speaker's own NAND the question the install will ask, so a board
			// that cannot say which slot it is running says so here rather than after
			// a 99 MB upload. That is not hypothetical: two ubi.mtd= parameters on the
			// command line made every install fail this way.
			_, err = s.flashPlan()
		}
		if err != nil {
			rep.Installable = false
			rep.Reason = err.Error()
		}
	}
	if !rep.Installable {
		if rep.Reason == "" {
			rep.Reason = "This build cannot write NAND. Install with a USB stick, as the install guide describes."
		}
	}
	return rep
}

func (s *Handler) hardwareFirmwareModel() (string, error) {
	if s.firmwareModel != nil {
		return s.firmwareModel()
	}
	return install.LiveModel()
}

// lockSoon waits a moment for the lock: an install answers the client before its handler
// lets go, and the reboot that follows at once was refused.
func (s *Handler) lockSoon() bool {
	for range 20 {
		if s.firmware.mu.TryLock() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// reboot restarts the speaker. It exists because an install is only half a
// firmware update: the new slot does not run until U-Boot picks it, and telling
// someone "now pull the plug" after a successful flash is how a good install
// turns into a bad story. Confirm-gated like the factory reset, and only wired
// up on a flashed speaker.
func (s *Handler) reboot(w http.ResponseWriter, r *http.Request) {
	if s.rebootFn == nil {
		apiError(w, http.StatusNotImplemented, "this build cannot reboot the speaker")
		return
	}
	if !s.lockSoon() {
		apiErrorCode(w, http.StatusConflict, apierror.Busy, "firmware operation is in progress")
		return
	}
	unlock := true
	defer func() {
		if unlock {
			s.firmware.mu.Unlock()
		}
	}()
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		s.badRequest(w, "bad body", err)
		return
	}
	if !body.Confirm {
		s.badRequest(w, "confirm must be true", nil)
		return
	}
	// A deliberate reboot is not a failed attempt: without this, one before the boot
	// confirm would count towards the fallback to the other slot.
	if err := s.settleBootGuard(); err != nil && s.log != nil {
		s.log.Warn("boot guard", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"rebooting": true})
	// After the response, not before: the browser should hear "yes" rather than
	// lose the connection and have to guess whether the request arrived.
	unlock = false
	safego.Go("firmware.reboot", func() {
		time.Sleep(time.Second)
		if err := s.rebootFn(); err != nil {
			if s.log != nil {
				s.log.Error("reboot", "err", err)
			}
			s.firmware.mu.Unlock()
		}
	})
}

// flashToolNames are the mtd-utils commands internal/flash shells out to. An image
// built without the mtd package has none of them, and the WLA NAND image was built
// that way: /api/firmware answered "installable" for a board that could not have
// written a single page, and the first flash_erase would have failed instead.
// Promising an install the image cannot perform is worse than refusing it.
var flashToolNames = []string{"flash_erase", "nandwrite", "ubiformat"}

func (s *Handler) missingFlashTools() []string {
	if s.flashTools != nil {
		return s.flashTools()
	}
	var missing []string
	for _, tool := range flashToolNames {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	return missing
}

// ConfirmBoot runs once this boot has proven itself: it repairs an environment no copy of
// which checks out, or converts the single copy to the redundant environment the U-Boot
// reads, and then resets U-Boot's boot count. The repair goes first: libubootenv refuses
// an environment it cannot read, and settling a fallback writes through it.
func (s *Handler) ConfirmBoot() error {
	// Each environment copy is one erase block: an install selecting its slot at the same
	// moment would lose one of the two writes, or double-write a block without an erase.
	s.firmware.mu.Lock()
	defer s.firmware.mu.Unlock()
	target, err := s.livePlan()
	if err != nil {
		return err
	}
	run, read := s.flashIO()
	inst := &flash.Installer{Target: target, Run: run}
	if model, err := s.hardwareFirmwareModel(); err == nil {
		if wrote, err := inst.RepairEnv(read, install.BootVariant(model)); err != nil {
			return err
		} else if wrote && s.log != nil {
			s.log.Warn("rewrote the U-Boot environment", "slot", target.Running.Name,
				"redundant", target.EnvRedundant)
		}
	}
	if err := s.settleBootGuard(); err != nil {
		return err
	}
	s.bootConfirmed.Store(true)
	return nil
}

// BootConfirmed reports whether this process confirmed the boot: ConfirmBoot succeeded.
// A soundtapd restarted within one boot confirms it again a minute after it starts.
func (s *Handler) BootConfirmed() bool { return s.bootConfirmed.Load() }

// livePlan is this speaker's NAND layout: the install's plan where one is wired (tests
// wire a fake), the live one otherwise.
func (s *Handler) livePlan() (flash.Target, error) {
	if s.flashPlan != nil {
		return s.flashPlan()
	}
	return install.LivePlan()
}

// settleBootGuard resets U-Boot's boot count, in the RTC and, for a selection not yet
// confirmed, in the environment. When U-Boot had to fall back to the other slot to get
// here, the slot that failed is still the selected one, so the running slot is selected
// in its place: it just proved it boots.
func (s *Handler) settleBootGuard() error {
	if s.bootGuardFn == nil {
		return nil
	}
	plan := s.flashPlan
	if plan == nil {
		plan = install.LivePlan
	}
	target, planErr := plan()
	run, read := s.flashIO()
	inst := &flash.Installer{Target: target, Run: run}
	// An environment soundtapd cannot read, after RepairEnv, is one U-Boot cannot read
	// either, so it holds no count to take into account or to clear: the RTC's decides.
	var envCount uint32
	envErr := planErr
	if envErr == nil {
		envCount, envErr = inst.PendingBootCount(read)
	}
	if envErr != nil && s.log != nil {
		s.log.Warn("boot count in the U-Boot environment unreadable", "err", envErr)
	}
	// The guard clears its record of the fallback only after this saved the choice.
	return s.bootGuardFn(envCount, func(fallback bool) error {
		if planErr != nil && fallback {
			return planErr
		}
		if !fallback {
			if envErr != nil {
				return nil
			}
			return inst.ConfirmPendingBoot(read)
		}
		if s.log != nil {
			s.log.Warn("booted by fallback; selecting the running slot", "slot", target.Running.Name)
		}
		return inst.SettleRunningSlot(read, flash.SoundTapMarkers(target.PersistentName))
	})
}

// ErrInstallBusy means another install, restore or reset holds the install lock.
var ErrInstallBusy = errors.New("another firmware operation is in progress")

// InstallStream installs a package that does not arrive as the request body: a Core
// release the speaker downloads itself (ADR 0048). open runs once the install lock is
// held and returns the package as a stream, its size (-1 if unknown) and its .sig. The
// install is the one an upload with ?install=1 gets, with the plain confirmation, so a
// package signed as older or as Bose's firmware is refused there. An error from open,
// or ErrInstallBusy, is returned with nothing written to w; once open succeeded, w
// carries the install's answer and InstallStream returns nil.
func (s *Handler) InstallStream(w http.ResponseWriter, r *http.Request,
	open func() (body io.ReadCloser, size int64, signature string, err error)) error {
	if !s.firmware.mu.TryLock() {
		return ErrInstallBusy
	}
	defer s.firmware.mu.Unlock()
	body, size, signature, err := open()
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()
	in := r.Clone(r.Context())
	in.Header.Set(signatureHeader, signature)
	in.Header.Set("X-SoundTap-Confirm", confirmInstall)
	in.Body, in.ContentLength = body, size
	s.installFirmware(w, in)
	return nil
}
