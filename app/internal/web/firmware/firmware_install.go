// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"net/http"
	"os"
	"runtime/debug"

	"github.com/stein155/soundtap/app/internal/counters"
	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/release"
	"github.com/stein155/soundtap/app/internal/stu"
	"github.com/stein155/soundtap/app/internal/web/apierror"
)

// modelST10 is the ST10's lower-case model name.
const modelST10 = "st10"

// installMemLimit is soundtapd's soft Go memory limit during an install (normally 40 MiB).
const installMemLimit = 28 << 20

const (
	// maxInstallPct is where an install's percentage stops while the package streams: the
	// kernel and the bootloaders are written after the last byte has arrived.
	maxInstallPct = 99
	// percent turns a fraction into a percentage.
	percent = 100
	// stagingDirMode is the home directory's mode when the install creates it.
	stagingDirMode = 0o755
	// splMTD and ubootMTD are the partitions whose sizes bound the staged SPL and U-Boot.
	splMTD   = 0
	ubootMTD = 4
	// defaultCodecMarker is written at boot when the other slot holds this speaker's only
	// lasting copy of its codec script (ADR 0012). Bose's package carries the script, ours
	// does not.
	defaultCodecMarker = "/run/soundtap/codec-only-in-other-slot"
)

var (
	// errWLAGeometry refuses an install on a board whose bootloader partitions are not the
	// Wireless Link's.
	errWLAGeometry = errors.New("WLA bootloader partition geometry is not recognized")
	// errCodecOnlyInOtherSlot refuses a SoundTap rootfs over the slot that still holds the
	// codec script the speaker could not keep.
	errCodecOnlyInOtherSlot = errors.New("the other slot holds this speaker's only copy of its sound settings, " +
		"which could not be saved to its storage; an update would leave it without sound. " +
		"Restart the speaker and try again; if this stays, go back to Bose's software and install again")
)

// tooBigError refuses a module larger than the partition it is for.
type tooBigError struct {
	what      string // the module, as the refusal names it
	size      int64
	partition string
	capacity  int64
}

func (e tooBigError) Error() string {
	return fmt.Sprintf("%s is %d bytes and %s holds %d", e.what, e.size, e.partition, e.capacity)
}

// installFirmware writes the package into the slot the speaker is not running.
//
// Nothing is staged whole: the rootfs is piped into ubiformat as it arrives off
// the network, exactly as the stock installer streams its payload through a FIFO
// into the NAND writer, because there is nowhere on this box to put 86 MB. Only
// the 3 MB kernel is held back — it is the write that makes the slot bootable,
// so it must not happen until the whole package has proven itself.
func (s *Handler) installFirmware(w http.ResponseWriter, r *http.Request) {
	s.install(w, r, false)
}

// install is installFirmware. With restart the report says the speaker restarts into the
// new slot on its own; install returns true only then, once the slot is written and
// selected and the report saved, and false on every failure.
func (s *Handler) install(w http.ResponseWriter, r *http.Request, restart bool) bool {
	req, admitted := s.admitInstall(w, r)
	if !admitted {
		return false
	}

	req.restart = restart
	// A speaker that has been up a while has about 20 MB available and an install takes about
	// that much, and an OOM here is a panic and a reboot partway through (vm.panic_on_oom).
	defer debug.SetMemoryLimit(debug.SetMemoryLimit(installMemLimit))

	debug.FreeOSMemory()
	// Hashed as it streams: the signature covers the whole file (ADR 0048).
	digest := sha256.New()

	progress := &installProgress{total: r.ContentLength, read: 0, last: 0, report: s.reportProgress(r.ContentLength)}
	defer s.installPct.Store(0)

	progress.say(0)
	body := bufio.NewReader(io.TeeReader(r.Body, io.MultiWriter(digest, progress)))

	// Runs after the active flag is cleared below, so the failure follows the progress screen.
	rec := &statusRecorder{ResponseWriter: w, code: 0}

	w = rec
	defer s.countFailure(rec)
	// Signal the LED loop that flash is in progress; clear it on the way out
	// regardless of success or failure so the panel does not stay "updating" forever.
	if s.firmwareActiveFn != nil {
		s.firmwareActiveFn(true)
		defer s.firmwareActiveFn(false)
	}

	files, staged := s.stageInstall(w)
	if !staged {
		return false
	}
	defer files.remove()

	job := s.newInstallJob(&req, files)
	pkg, scanErr := stu.Scan(body, job.writerFor)

	writeErr := job.finishRootfs(scanErr)
	if s.refuseScan(w, job, pkg, scanErr, writeErr) {
		// A refusal after the rootfs began, a stream cut in a later module say, must not
		// leave a native rootfs in the spare slot either.
		if job.rootfs != nil {
			s.discardRootfs(job, pkg)
		}

		return false
	}
	// A stock rootfs carries no /boot; its kernel is the raw partition, read back below.
	if pkg.Native {
		err := job.inst.VerifyRootfs(job.read, install.BootVariant(req.model))
		if err != nil {
			// Refused before checkStream held it to its signature: a native rootfs boots
			// under a lost environment's built-in bootcmd, so it goes, as any refused one.
			s.discardRootfs(job, pkg)
			s.fail(w, "writing the root filesystem failed", err)

			return false
		}
	}

	if !s.checkStream(w, r, job, pkg, body, digest) {
		return false
	}

	if !s.installBootloaders(w, job, pkg) {
		return false
	}
	// The activation step. Until now the target slot has no kernel at all.
	err := job.inst.WriteKernel(job.read, files.kernel.Name())
	if err != nil {
		s.fail(w, "writing the kernel failed", err)

		return false
	}

	return s.selectInstalledSlot(w, job, pkg)
}

// countFailure counts an install that answered with an error and lets the speaker say so.
// It runs after the active flag is cleared, so the failure follows the progress screen.
func (s *Handler) countFailure(rec *statusRecorder) {
	if rec.code < http.StatusBadRequest {
		return
	}

	counters.InstallFailures.Add(1) // /api/health (ADR 0127)

	if s.firmwareFailedFn != nil {
		s.firmwareFailedFn()
	}
}

// checkStream reads the package to its end and holds it to its signature. Nothing written
// so far is selected. The signature over the named digest passed before the first write;
// check that the file hashes to it before the bootloaders, the kernel or the environment
// change. A native root filesystem carries its kernel and boots under a lost
// environment's built-in bootcmd, so one that fails is erased. It answers w and returns
// false when the package fails.
func (s *Handler) checkStream(w http.ResponseWriter, r *http.Request, job *installJob, pkg *stu.Package,
	body io.Reader, digest hash.Hash,
) bool {
	_, err := io.Copy(io.Discard, body)
	if err == nil && !job.req.unsignedStock {
		err = s.checkSignature(r, release.KindCore, digest.Sum(nil))
	}
	// ADR 0048: the SHA-256 of every unsigned package installed is logged; Bose's own file
	// on the way back does not pass checkSignature, which logs the others.
	if err == nil && job.req.unsignedStock && s.log != nil {
		s.log.Warn("installing an unsigned stock package", "sha256", hex.EncodeToString(digest.Sum(nil)))
	}

	if err != nil {
		s.discardRootfs(job, pkg)
		s.fail(w, "the package was not installed", err)

		return false
	}

	return true
}

// discardRootfs erases a native root filesystem the install wrote and then refused. pkg is
// nil when the scan itself failed.
func (s *Handler) discardRootfs(job *installJob, pkg *stu.Package) {
	if !job.nativeRootfs && (pkg == nil || !pkg.Native) {
		return
	}

	err := job.inst.EraseAlternateRootfs()
	if err != nil && s.log != nil {
		s.log.Warn("erasing the refused root filesystem", "err", err)
	}
}

// reportProgress hears an install's percentage: the panel always, Progress only when the
// size (total) is known, since without it the percentage stays 0 and says nothing.
func (s *Handler) reportProgress(total int64) func(int) {
	return func(pct int) {
		if total > 0 {
			s.installPct.Store(int64(pct) + 1)
		}

		if s.firmwareProgressFn != nil {
			s.firmwareProgressFn(pct)
		}
	}
}

// installProgress turns the bytes read off the request into a percentage. It stops at 99:
// the kernel and the bootloaders are written after the last byte has arrived.
type installProgress struct {
	total, read int64
	last        int
	report      func(int)
}

func (p *installProgress) Write(chunk []byte) (int, error) {
	p.read += int64(len(chunk))
	if p.total > 0 {
		pct := int(min(p.read*percent/p.total, maxInstallPct))
		if pct != p.last {
			p.say(pct)
		}
	}

	return len(chunk), nil
}

func (p *installProgress) say(pct int) {
	p.last = pct
	if p.report != nil {
		p.report(pct)
	}
}

// installRequest is what an install has settled before it reads a byte of the package.
type installRequest struct {
	model  string
	target flash.Target
	// unsignedStock is an upload without a signature confirmed as Bose's firmware: the way
	// back to stock. Only Bose's modules may reach NAND, and the press on the
	// speaker that /api/firmware?install=1 waits for is the only check of where it came from.
	unsignedStock bool
	// unsigned is any other upload without a signature: an own build, installed on the
	// press alone (ADR 0048). Bose's firmware among them still needs the stock confirmation.
	unsigned bool
	// restart is a Core release the speaker fetched itself (POST /api/core-update): it
	// restarts into the new slot once that is selected (InstallStream).
	restart bool
}

// admitInstall refuses an install this speaker cannot or may not do, before the
// upload. It answers w and returns false when it refuses.
func (s *Handler) admitInstall(w http.ResponseWriter, r *http.Request) (installRequest, bool) {
	var req installRequest

	if s.flashPlan == nil {
		apiError(w, http.StatusNotImplemented, "this build cannot write NAND")

		return req, false
	}
	// Before the upload, not after: a 96 MB stream that ends in "flash_erase: not
	// found" costs minutes and tells the user nothing useful.
	err := install.ToolsRefusal(s.missingFlashTools())
	if err != nil {
		apiError(w, http.StatusNotImplemented, err.Error())

		return req, false
	}

	req.unsignedStock = unsignedStock(r)

	req.unsigned = !req.unsignedStock && r.Header.Get(signatureHeader) == ""
	if !req.unsignedStock {
		err = s.signatureBeforeWrite(r)
		if err != nil {
			apiErrorCode(w, http.StatusForbidden, apierror.SignatureInvalid, err.Error())

			return req, false
		}
	}
	// An older release, or Bose's firmware, only with the confirmation that names it:
	// either can bring back a bug a newer release fixed.
	version, confirm, err := s.packageVersion(r)
	if err != nil {
		apiErrorCode(w, http.StatusForbidden, apierror.SignatureInvalid, err.Error())

		return req, false
	}

	if req.unsignedStock {
		version, confirm = stockVersion, confirmStock
	}

	if r.Header.Get("X-SoundTap-Confirm") != confirm {
		apiError(w, http.StatusPreconditionRequired, versionRefusal(version, s.runningVersion(), confirm).Error())

		return req, false
	}

	model, err := s.hardwareFirmwareModel()
	if err == nil {
		err = install.KnownModel(model)
	}

	if err != nil {
		apiError(w, http.StatusPreconditionFailed, err.Error())

		return req, false
	}

	req.model = model

	req.target, err = s.flashPlan()
	if err != nil {
		// Refusing is the safe answer: an install that guesses its slot writes the
		// one the speaker is running from.
		apiError(w, http.StatusPreconditionFailed, err.Error())

		return req, false
	}

	if !req.target.WLABootloaderGeometry() {
		s.fail(w, "the package was not installed", errWLAGeometry)

		return req, false
	}
	// A stock BOSE chain is a supported install here, and on every model it is the
	// only way back: none keeps Bose's updater once it runs our chain. What makes it
	// safe is that the stock package carries MLO and u-boot.img, so the boot chain is
	// restored as a whole rather than left half SoundTap and half stock, and that
	// this endpoint writes the slot the speaker is not running from.
	return req, true
}

// stagedFiles hold the payloads that wait until the package has proven itself: the
// kernel always, MLO and U-Boot on a board that takes its boot chain from it.
type stagedFiles struct{ kernel, mlo, uboot *os.File }

func (f stagedFiles) remove() {
	for _, file := range []*os.File{f.kernel, f.mlo, f.uboot} {
		if file != nil {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}
}

// stageInstall creates the staging files. It answers w and returns false when
// there is no room for them.
func (s *Handler) stageInstall(w http.ResponseWriter) (stagedFiles, bool) {
	var files stagedFiles
	// Stage where there is room. os.CreateTemp("") is /tmp, which on this speaker is an
	// 8 MiB tmpfs that also holds the log -- and the kernel is 4.7 MB, so an install
	// failed with "no space left on device" 35 seconds in, twice, once because the
	// previous attempt's staged kernel was still lying there. The home directory is on
	// the persistent volume with tens of megabytes free.
	stage := s.stagingDir()
	install.RemoveStaleStaging(stage)

	var err error

	files.kernel, err = os.CreateTemp(stage, "soundtap-kernel-")
	if err != nil {
		s.fail(w, "no room to stage the kernel", err)

		return files, false
	}

	files.mlo, err = os.CreateTemp(stage, "soundtap-mlo-")
	if err == nil {
		files.uboot, err = os.CreateTemp(stage, "soundtap-uboot-")
	}

	if err != nil {
		files.remove()
		s.fail(w, "no room to stage bootloaders", err)

		return files, false
	}

	return files, true
}

// stagingDir is where the payloads that cannot be streamed straight to flash are held:
// the home directory when there is one, which is the persistent volume, and the system
// default otherwise (tests, and any build with no home).
func (s *Handler) stagingDir() string {
	if s.homeDir == "" {
		return ""
	}

	err := os.MkdirAll(s.homeDir, stagingDirMode)
	if err != nil {
		return ""
	}

	return s.homeDir
}

// flashIO is how NAND is written and read back: exec on a speaker, fakes in tests.
func (s *Handler) flashIO() (flash.Command, func(string, ...string) ([]byte, error)) {
	run, read := s.flashRunner, s.flashReader
	if run == nil {
		run = s.flashRun
	}

	if read == nil {
		read = s.flashRead
	}

	return run, read
}

// installJob routes the package's modules as they stream past. refusal is the first
// reason the package cannot be installed; once it is set nothing more is staged or
// written, while the scan still reads and checks every module's CRC.
type installJob struct {
	req   installRequest
	files stagedFiles
	inst  *flash.Installer
	run   flash.Command
	read  func(string, ...string) ([]byte, error)

	// codecMarker is the Handler's codecMarker, resolved.
	codecMarker string

	refusal error
	// A model-suffixed kernel has already said which speaker it is for, in the one place
	// stock's installer reads too. Its uImage name then only has to prove it is a SoundTap
	// mainline image: one kernel serves rhino and binky, so the name inside it names
	// whichever model the build happened to be driven from.
	kernelWasSuffixed bool
	// bootloaders names the boot chain copies this install rewrote, for the report.
	bootloaders []string
	// The rootfs writer, started when its module begins, because ubiformat has to be
	// told the image size up front -- and the size is in the module header, which
	// arrives immediately before the payload.
	rootfs     io.WriteCloser
	rootfsWait func() error
	// nativeRootfs is set when the rootfs written came from a native module: known before
	// the package is, so a scan that fails after it still erases it.
	nativeRootfs bool
}

func (s *Handler) newInstallJob(req *installRequest, files stagedFiles) *installJob {
	run, read := s.flashIO()

	codecMarker := s.codecMarker
	if codecMarker == "" {
		codecMarker = defaultCodecMarker
	}

	return &installJob{
		req: *req, files: files, run: run, read: read,
		inst:        &flash.Installer{Target: req.target, Run: run, Open: s.flashOpen},
		codecMarker: codecMarker,
		refusal:     nil, kernelWasSuffixed: false, bootloaders: nil, rootfs: nil, rootfsWait: nil,
		nativeRootfs: false,
	}
}

// screen refuses a module this upload may not bring, unless an earlier one already was.
func (j *installJob) screen(module stu.Module) {
	if j.req.unsignedStock && j.refusal == nil && !boseModule(module) {
		j.refusal = errNotStock
	}
	// An unsigned package under "install" is a SoundTap build: a module of Bose's is the way
	// back, which takes its own confirmation, and is refused before its payload is written.
	if j.req.unsigned && j.refusal == nil && boseModule(module) {
		j.refusal = errNeedsStock
	}
}

// writerFor is the stu.Scan sink: where module's payload goes, or nil to skip it.
func (j *installJob) writerFor(module stu.Module) io.Writer {
	j.screen(module)

	name, mine := j.moduleName(module)
	if !mine || j.refusal != nil {
		return nil
	}

	var sink io.Writer

	switch name {
	case "soundtap-rootfs":
		j.refusal = codecOnlyInOtherSlot(j.codecMarker)
		if j.refusal == nil {
			sink, j.refusal = j.rootfsWriter(module)
			j.nativeRootfs = j.nativeRootfs || module.Native
		}
	case "ubi.img":
		sink, j.refusal = j.rootfsWriter(module)
		j.nativeRootfs = j.nativeRootfs || module.Native
	case "linuxPatched", "soundtap-kernel":
		j.kernelWasSuffixed = name != module.Name
		sink, j.refusal = j.kernelWriter(module)
	case "soundtap-spl", "MLO":
		sink, j.refusal = j.bootloaderWriter(module, "the SPL", splMTD, j.files.mlo)
	case "soundtap-uboot", "u-boot.img":
		sink, j.refusal = j.bootloaderWriter(module, "u-boot.img", ubootMTD, j.files.uboot)
	default:
		// Any other module of this speaker's is read past: its CRC is still checked.
	}

	return sink
}

// codecOnlyInOtherSlot refuses a SoundTap rootfs over the slot that still holds the
// codec script the speaker could not keep, as the update would leave it mute for good.
// marker is the file the boot writes when that is so.
func codecOnlyInOtherSlot(marker string) error {
	_, err := os.Stat(marker)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	return errCodecOnlyInOtherSlot
}

// moduleName is what module is called on this speaker, and whether it is this
// speaker's at all.
//
// A universal package names its kernels per model -- soundtap-kernel.rhino,
// soundtap-kernel.binky -- because one file installs on either speaker and only
// one of those kernels is this speaker's. Skipping another model's module skips
// the payload without skipping its CRC: the scanner still reads and checks every
// byte, so a corrupt package is still refused.
func (j *installJob) moduleName(module stu.Module) (string, bool) {
	model := j.req.model

	name, mine := stu.ModuleForModel(module.Name, install.VariantForModel(model))
	if mine || (model != modelST10 && model != "st20") {
		return name, mine
	}
	// The boot chain is the Wireless Link's by name: the ST10 takes all of it, the
	// ST20 its U-Boot.
	if base, shared := stu.ModuleForModel(module.Name, "binky"); shared &&
		(base == "soundtap-uboot" || base == "soundtap-spl" && model == modelST10) {
		return base, true
	}

	return name, false
}

// rootfsWriter prepares the alternate slot and starts writing the root filesystem into it.
//
// The alternate kernel is erased here rather than up front, and the difference
// matters: a slot with no kernel cannot be selected, so erasing before the
// rootfs is touched still means every later failure leaves the speaker booting
// what it boots now — but a package that never gets as far as its rootfs (a
// truncated download, an HTML error page, a mainline image on an ST20) now
// leaves the fallback slot intact and bootable.
func (j *installJob) rootfsWriter(module stu.Module) (io.Writer, error) {
	model, alt := j.req.model, j.req.target.Alternate

	err := install.ModelRefusal(model, module)
	if err != nil {
		return nil, fmt.Errorf("the root filesystem module: %w", err)
	}

	err = install.CheckKernelForTarget(j.files.kernel.Name(), model, j.kernelWasSuffixed)
	if err != nil {
		return nil, fmt.Errorf("the staged kernel: %w", err)
	}
	// Against the live table, not a constant: a payload that does not fit is
	// a write that dies partway through, and there is no reason to find that
	// out with the target slot already erased.
	// module.Size, not module.Payload(): the overlapping tail is part of the image, and
	// a rootfs is a whole number of eraseblocks only when it is counted in.
	if !flash.Fits(module.Size, alt.RootfsSize) {
		return nil, tooBigError{
			what: "the root filesystem", size: module.Size, partition: alt.RootfsName, capacity: alt.RootfsSize,
		}
	}
	// How much of the module is really the UBI image. Both answers have
	// been measured, and neither is always right:
	//
	//   stock's approved package   ubi.img 89,653,248 = 684 eraseblocks
	//                              including the overlap
	//   ours, 2026-09-17           soundtap-rootfs 51,118,100, and it is
	//                              51,118,080 = 390 eraseblocks without it
	//
	// ubiformat takes a whole number of eraseblocks or nothing, and says so
	// in two different ways: "not multiple of eraseblock size" for the long
	// one and "error 0 (No error information)" straight after its scan for
	// the short one. So pick the length that is a whole number rather than
	// believing either package shape.
	body := install.UBIBodySize(module.Size, alt.RootfsEraseSize)
	// An earlier install that was never rebooted into left the environment
	// selecting the slot about to be erased; point it back at the running one
	// first, or a failure from here on boots a half-written slot.
	// A torn earlier write leaves a bad CRC that libubootenv refuses outright.
	_, err = j.inst.RepairEnv(j.read, install.BootVariant(model))
	if err != nil {
		return nil, fmt.Errorf("repairing the U-Boot environment: %w", err)
	}

	err = j.inst.HoldRunningSlot(j.read)
	if err != nil {
		return nil, fmt.Errorf("selecting the running slot: %w", err)
	}

	err = j.inst.EraseAlternateKernel()
	if err != nil {
		return nil, fmt.Errorf("erasing the other slot's kernel: %w", err)
	}

	j.rootfs, j.rootfsWait = j.inst.StartRootfs(body)
	// The scanner hands over the whole module; anything past the image is
	// the next header's first bytes and must not reach ubiformat.
	return &install.TruncatingWriter{W: j.rootfs, Left: body}, nil
}

func (j *installJob) kernelWriter(module stu.Module) (io.Writer, error) {
	err := install.ModelRefusal(j.req.model, module)
	if err != nil {
		return nil, fmt.Errorf("the kernel module: %w", err)
	}

	alt := j.req.target.Alternate
	if !flash.Fits(module.Size, alt.KernelSize) {
		return nil, tooBigError{
			what: "the kernel", size: module.Size, partition: fmt.Sprintf("mtd%d", alt.KernelMTD),
			capacity: alt.KernelSize,
		}
	}

	return j.files.kernel, nil
}

// bootloaderWriter stages a boot chain module for mtd, on a board that takes one.
// It is held to the partition before a byte is staged: staging is on /mnt/nv, which
// an oversize module would fill long before the scan ends.
func (j *installJob) bootloaderWriter(module stu.Module, label string, mtd int, staged *os.File) (io.Writer, error) {
	if size := j.req.target.BootloaderSizes[mtd]; !flash.Fits(module.Size, size) {
		return nil, tooBigError{what: label, size: module.Size, partition: fmt.Sprintf("mtd%d", mtd), capacity: size}
	}

	return staged, nil
}

// finishRootfs ends the rootfs write, if one started, and returns how it went.
// Closing with the scan's error makes ubiformat's read fail rather than let it
// finish a truncated image and call it a success.
func (j *installJob) finishRootfs(scanErr error) error {
	if j.rootfs == nil {
		return nil
	}

	if pw, ok := j.rootfs.(interface{ CloseWithError(err error) error }); ok {
		_ = pw.CloseWithError(scanErr)
	} else {
		_ = j.rootfs.Close()
	}

	return j.rootfsWait()
}

// refuseScan answers w when the scan, the package or the rootfs write failed, and
// reports whether it did.
func (s *Handler) refuseScan(w http.ResponseWriter, job *installJob, pkg *stu.Package, scanErr, writeErr error) bool {
	if errors.Is(scanErr, stu.ErrSink) {
		// The package was fine; the write was not. Saying "bad package" here sends
		// someone off to re-download a file that was never the problem.
		//
		// Report writeErr when there is one: a writer that dies closes the pipe, so
		// the scan's own error is always the same "read/write on closed pipe" and
		// says nothing, while writeErr carries what the command actually printed.
		// Throwing that away cost an hour of guessing at a failed stock install.
		cause := scanErr
		if writeErr != nil {
			cause = writeErr
		}

		s.fail(w, "writing to flash failed", cause)

		return true
	}

	if scanErr != nil {
		s.rejectPackage(w, scanErr)

		return true
	}

	err := install.PackageRefusal(job.req.model, pkg)
	if err != nil {
		s.fail(w, "the package was not installed", err)

		return true
	}
	// A SoundTap kernel or chain under Bose's module names is not Bose's firmware either.
	if job.req.unsignedStock && (pkg.Mainline || pkg.Native) {
		s.fail(w, "the package was not installed", errNotStock)

		return true
	}
	// The way back to Bose is never a side effect of "install", signed or not.
	if job.req.unsigned && boseFirmware(pkg) {
		apiError(w, http.StatusPreconditionRequired, errNeedsStock.Error())

		return true
	}

	if job.refusal != nil {
		s.fail(w, "the package was not installed", job.refusal)

		return true
	}

	if writeErr != nil {
		s.fail(w, "writing the root filesystem failed", writeErr)

		return true
	}

	return false
}
