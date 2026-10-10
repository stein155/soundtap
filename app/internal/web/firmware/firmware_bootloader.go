// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package firmware

import (
	"fmt"
	"net/http"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/stu"
)

// installBootloaders writes the package's MLO and U-Boot to mtd0-mtd4 when they differ
// from what the board runs, or read back right only after many ECC corrections. It answers w and returns false when that fails.
func (s *Handler) installBootloaders(w http.ResponseWriter, j *installJob, pkg *stu.Package) bool {
	mlo, uboot := j.files.mlo, j.files.uboot
	// Both staged bootloaders must be on disk before anything is written from them;
	// the error used to be scoped to the if and lost.
	err := mlo.Sync()
	if err == nil {
		err = uboot.Sync()
	}
	if err == nil {
		err = install.VerifyBootloaders(pkg, j.req.model, mlo.Name(), uboot.Name())
	}
	if err != nil {
		s.fail(w, "the package was not installed", err)
		return false
	}
	// An ordinary update carries the chain the board already runs, and then nothing
	// at mtd0-mtd4 is touched.
	changing, err := install.BootloadersChanging(j.read, mlo.Name(), uboot.Name())
	if err != nil {
		s.fail(w, "the package was not installed", err)
		return false
	}
	if len(changing) > 0 {
		return s.replaceBootloaders(w, j, pkg)
	}
	// The board already runs this chain; bring its recipes in line with it too. The
	// install is done by the build before this one, so a chain that changed its recipes
	// (ADR 0040) gets them only from the next install. Nothing is written when they match.
	if pkg.Native {
		if err := j.inst.InstallMainlineRecipes(j.read, install.BootVariant(j.req.model)); err != nil {
			s.fail(w, "the package was not installed", err)
			return false
		}
	}
	return true
}

// replaceBootloaders writes the staged boot chain, with the environment recipes the
// new chain runs. It answers w and returns false when that fails.
func (s *Handler) replaceBootloaders(w http.ResponseWriter, j *installJob, pkg *stu.Package) bool {
	// Our U-Boot cannot run Bose's slot recipes (nboot.e), so ours go in first.
	// They boot under Bose's U-Boot as well, so every moment of the bootloader
	// write below still has recipes the chain on NAND can run.
	if pkg.Native {
		if err := j.inst.InstallMainlineRecipes(j.read, install.BootVariant(j.req.model)); err != nil {
			s.fail(w, "the package was not installed", err)
			return false
		}
	}
	// Bose's U-Boot reads one 8 KiB copy, and its first write below makes it the one
	// that boots: the environment goes back to that format first (ADR 0010).
	if !pkg.Native {
		if err := j.inst.UseLegacyEnv(j.read); err != nil {
			s.fail(w, "the package was not installed", err)
			return false
		}
	}
	if err := install.WriteBootloaders(j.run, j.read, j.files.mlo.Name(), j.files.uboot.Name(), pkg.Native, s.log); err != nil {
		s.fail(w, "writing bootloaders failed", err)
		return false
	}
	// Back on Bose's bootloader: the stock recipes, now that it can run them.
	if !pkg.Native {
		if err := j.inst.InstallStockRecipes(j.read); err != nil {
			s.fail(w, "the stock bootloaders were written but the environment still holds the SoundTap boot recipes", err)
			return false
		}
	}
	return true
}

// selectInstalledSlot points the boot environment at the slot just written and
// answers w with the report.
//
// On this board the environment is the slot selector -- bootcmd runs nand_boot
// or nand_boot2 -- so an install that does not rewrite it is never booted. This
// endpoint used to warn about that instead of doing it, which is why a PONG
// install appeared to do nothing. Last, and only once the slot is bootable: if
// it fails, the speaker still boots what it boots now.
//
// Every model, not only the Wireless Link. An ST10 used to pick its slot from the
// name in the kernel header, so writing a slot was enough; that stopped being true
// the moment a SoundTap install rewrote its environment, and bootcmd has decided
// ever since. Measured on the speaker on 2026-09-20: an install reported PING
// written, the speaker rebooted, and /proc/cmdline still said ubi.mtd=9 -- PONG,
// the slot it was already running. SelectSlot refuses to point bootcmd at an entry
// the environment does not have, so a board that never saw our installer keeps
// booting what it boots now rather than dropping into a U-Boot prompt.
func (s *Handler) selectInstalledSlot(w http.ResponseWriter, j *installJob, pkg *stu.Package) {
	target := j.req.target
	rep := s.describe(pkg)
	var markers []string
	if pkg.Native {
		markers = flash.SoundTapMarkers(target.PersistentName)
	}
	// Settle a pending fallback first, so it cannot later put back the slot this selects.
	if err := s.settleBootGuard(); err != nil && s.log != nil {
		s.log.Warn("boot guard", "err", err)
	}
	if err := j.inst.SelectBootSlot(j.read, target.Alternate, markers); err != nil {
		s.fail(w, fmt.Sprintf("%s was written but could not be selected; the speaker still boots %s",
			target.Alternate.Name, target.Running.Name), err)
		return
	}
	rep.Reason = fmt.Sprintf("Installed into %s and selected it. Reboot to run it; %s still holds the firmware you are running now.",
		target.Alternate.Name, target.Running.Name)
	if err := s.persistFirmwarePending(target.Alternate.Name, rep); err != nil {
		if s.log != nil {
			s.log.Error("firmware recovery state", "err", err)
		}
		rep.Reason += " Recovery status was not saved: " + err.Error()
	}
	rep.Installed = true
	rep.Slot, rep.RunningSlot = target.Alternate.Name, target.Running.Name
	writeJSON(w, http.StatusOK, rep)
}
