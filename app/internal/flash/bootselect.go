// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stein155/soundtap/app/internal/ubootenv"
)

// Which slot this board boots is decided by one environment entry, not by the
// uImage signature bytes that select a slot on an ST10. Read off this board's own
// mtd5: bootcmd is literally "run nand_boot;" or "run nand_boot2;", and those run
// nand_args / nand_args2, which carry ubi.mtd=8 and ubi.mtd=9.
//
// The web install path used to write a slot and then tell the user that a PONG
// install "needs a bootloader that selects PONG". It does not: it needs this.
const (
	bootcmdKey  = "bootcmd"
	bootcmdPing = "run nand_boot;"
	bootcmdPong = "run nand_boot2;"

	// U-Boot's own rollback (CONFIG_BOOTCOUNT_LIMIT, ADR 0010): past bootlimit boots
	// without a confirm it runs altbootcmd instead of bootcmd.
	altKey   = "altbootcmd"
	limitKey = "bootlimit"

	// While upgrade_available is 1 our U-Boot keeps the boot count in the environment
	// too (uboot-2026.07-bootcount-env.patch), as bootcount: RTC SCRATCH2 does not survive
	// a cold reset or a power cut. An install sets it with the slot it selects; the boot
	// confirm clears both.
	pendingKey = "upgrade_available"
	countKey   = "bootcount"
)

// The refusals of an environment write. A message with values reads them around its
// sentinel, as it always has.
var (
	errNoFallbackEntry  = errors.New("entry for the fallback")
	errNotNANDSlot      = errors.New("is not a NAND slot")
	errRefuseSelect     = errors.New("entry; refusing to select")
	errBadEnvBlock      = errors.New("sits on a bad erase block: Bose's U-Boot would find no environment there")
	errShortRead        = errors.New("short read of")
	errEnvReadback      = errors.New("environment readback verification failed")
	errNotErased        = errors.New("does not read back erased")
	errNoSlotSelectable = errors.New("so no slot can be selected")
	errTooSmallFor      = errors.New("is too small for a")
	errReadbackFailed   = errors.New("readback verification failed")
)

// markUnconfirmed sets upgrade_available and a count starting over for a selection
// soundtapd has yet to confirm. It reports a change, as it always is one.
func markUnconfirmed(env *ubootenv.Env) bool {
	env.Delete(countKey)
	env.Set(pendingKey, "1")

	return true
}

// clearUnconfirmed clears upgrade_available and the count, and reports whether either was
// there. Stock (no markers) gets neither key.
func clearUnconfirmed(env *ubootenv.Env) bool {
	_, hadPending := env.Get(pendingKey)
	_, hadCount := env.Get(countKey)
	env.Delete(countKey)
	env.Delete(pendingKey)

	return hadPending || hadCount
}

// BootLimit is how many unconfirmed boots of a slot U-Boot tries before it boots the
// other one. Four (owner, 2026-11-20): an unconfirmed selection keeps its count in the
// environment, where a power cut does not clear it, so three power cuts during trial
// boots, each before that boot's confirm, still leave a fourth boot of the new slot to
// confirm it; the fifth boot, after four unconfirmed ones, falls back (ADR 0010).
const BootLimit = 4

// armRollback points altbootcmd at the slot that is not being selected. Stock
// (no markers) gets neither key: Bose's environment never had them.
func armRollback(env *ubootenv.Env, slot Slot, markers []string) error {
	if len(markers) == 0 {
		env.Delete(altKey)
		env.Delete(limitKey)

		return nil
	}

	other := ping
	if slot.Name == ping.Name {
		other = pong
	}

	fallback, err := bootFor(other)
	if err != nil {
		return err
	}

	if _, ok := env.Get(fallback.cmdKey); !ok {
		return fmt.Errorf("the environment has no %s %w", fallback.cmdKey, errNoFallbackEntry)
	}

	env.Set(altKey, fallback.bootcmd)
	env.Set(limitKey, strconv.Itoa(BootLimit))

	return nil
}

// slotBoot is the environment entries a slot boots through: the command bootcmd must run,
// the command entry itself, and the bootargs entry that command runs in turn.
type slotBoot struct {
	bootcmd, cmdKey, argsKey string
}

// bootFor returns the entries slot boots through.
func bootFor(slot Slot) (slotBoot, error) {
	switch slot.Name {
	case ping.Name:
		return slotBoot{bootcmd: bootcmdPing, cmdKey: "nand_boot", argsKey: "nand_args"}, nil
	case pong.Name:
		return slotBoot{bootcmd: bootcmdPong, cmdKey: "nand_boot2", argsKey: "nand_args2"}, nil
	}

	return slotBoot{bootcmd: "", cmdKey: "", argsKey: ""}, fmt.Errorf("%q %w", slot.Name, errNotNANDSlot)
}

// SoundTapMarkers are the kernel command-line additions a SoundTap install wants on
// the bootargs entry of the slot it boots.
//
// The kernel we ship takes the bootloader's line: os/board/image/linux.fragment
// leaves CONFIG_CMDLINE empty and CONFIG_CMDLINE_FORCE unset. Our U-Boot's slot_boot
// sets bootargs from soundtap_args, which carries the same markers, so there this
// entry is not the line the kernel gets. They are written because they are the
// correct line for a recipe that does boot through this entry, and because an
// environment that contradicts the kernel it boots is a trap for whoever reads mtd5
// next.
//
// persistent names the persistent-storage partition; stock misspells it
// "Persistant" on some images, so the caller passes what /proc/mtd actually says.
func SoundTapMarkers(persistent string) []string {
	if persistent == "" {
		persistent = "NAND.Persistent-Storage"
	}

	return []string{"soundtap.nandroot=1", "ubi.mtd=" + persistent + ",2048"}
}

// isSoundTapMarker reports whether one bootargs field was put there by a SoundTap
// install. ubi.mtd= is the entry's own business — ubi.mtd=8,2048 is stock's and
// must survive — so only a named partition counts as ours.
func isSoundTapMarker(field string) bool {
	return field == "soundtap.nandroot=1" || strings.HasPrefix(field, "ubi.mtd=NAND.")
}

// SelectSlot points the environment at slot and brings its bootargs entry in line
// with markers: every marker present exactly once, and every SoundTap marker that
// is not in the list removed. Passing no markers is how an install of stock
// firmware hands the environment back the way Bose would have left it.
//
// It changes nothing else. Reinstalling is idempotent, which matters because every
// rewrite erases a copy of the environment: under Bose's U-Boot the only one.
func SelectSlot(env *ubootenv.Env, slot Slot, markers []string) error {
	boot, err := bootFor(slot)
	if err != nil {
		return err
	}

	bootcmd, cmdKey, argsKey := boot.bootcmd, boot.cmdKey, boot.argsKey
	// Refuse to point bootcmd at an entry that is not there. U-Boot would run an
	// empty command and drop to its prompt with no console attached, which on this
	// board is indistinguishable from a brick.
	for _, key := range []string{cmdKey, argsKey} {
		if _, ok := env.Get(key); !ok {
			return fmt.Errorf("the environment has no %s %w %s", key, errRefuseSelect, slot.Name)
		}
	}

	env.Set(bootcmdKey, bootcmd)
	// Both entries, not just the one being selected: markers left on the other slot
	// would describe a boot that is no longer the one this environment performs.
	for _, key := range []string{"nand_args", "nand_args2"} {
		value, ok := env.Get(key)
		if !ok {
			continue
		}

		want := markers
		if key != argsKey {
			want = nil
		}

		env.Set(key, applyMarkers(value, want))
	}

	return nil
}

func applyMarkers(value string, markers []string) string {
	kept := []string{}

	for field := range strings.FieldsSeq(value) {
		if isSoundTapMarker(field) && !slicesHas(markers, field) {
			continue
		}

		kept = append(kept, field)
	}

	for _, marker := range markers {
		if !slicesHas(kept, marker) {
			kept = append(kept, marker)
		}
	}

	return strings.Join(kept, " ")
}

func slicesHas(values []string, want string) bool {
	return slices.Contains(values, want)
}

// Reader is how the installer reads a partition back. nanddump on the speaker;
// tests supply their own.
type Reader func(name string, args ...string) ([]byte, error)

// retiredOwnerKey is where builds before 0.1.0 kept an owner's own signing key (ADR 0048
// no longer has one). Nothing reads it any more; an install's slot selection, a write that
// happens anyway, removes it, and the next write (the boot confirmation) leaves it out of
// the other copy too.
const retiredOwnerKey = "soundtap_owner_key"

// SelectBootSlot rewrites NAND.U-Boot-Env so the board boots slot.
//
// It reads the environment first and refuses one no copy of which checks out. That is
// deliberate: a corrupt environment still leaves U-Boot falling back to a working
// built-in default, and rebuilding on top of the damage would take that away; RepairEnv
// rebuilds it from what a running system knows instead.
func (in *Installer) SelectBootSlot(read Reader, slot Slot, markers []string) error {
	return in.rewriteEnv(read, func(env *ubootenv.Env) (bool, error) {
		err := SelectSlot(env, slot, markers)
		if err != nil {
			return false, err
		}

		env.Delete(retiredOwnerKey)

		if in.Target.BootCount {
			// The fallback and the count that drives it go in the same write as bootcmd.
			if len(markers) > 0 {
				markUnconfirmed(env)
			} else {
				clearUnconfirmed(env)
			}

			return true, armRollback(env, slot, markers)
		}

		return true, nil
	})
}

// SettleRunningSlot selects the running slot after U-Boot fell back to it, in one
// environment write. The fallback names the running slot as well: the other slot just
// failed, and a later run of warm boots past bootlimit must not lead back into it. The
// next install arms the fallback at this slot again when it selects the rewritten one.
func (in *Installer) SettleRunningSlot(read Reader, markers []string) error {
	running := in.Target.Running

	return in.rewriteEnv(read, func(env *ubootenv.Env) (bool, error) {
		err := SelectSlot(env, running, markers)
		if err != nil {
			return false, err
		}

		if !in.Target.BootCount {
			return true, nil
		}

		clearUnconfirmed(env) // the running slot just proved it boots

		err = armRollback(env, running, markers)
		if err != nil {
			return false, err
		}

		if len(markers) > 0 {
			runBoot, _ := bootFor(running)
			env.Set(altKey, runBoot.bootcmd)
		}

		return true, nil
	})
}

// RepairEnv leaves NAND with an environment the U-Boot on the board boots from, and
// reports whether it wrote. After a torn write U-Boot boots its built-in default while
// libubootenv refuses every later install and confirm, so this runs before both.
//
// Under a U-Boot that reads the redundant environment (EnvRedundant):
//   - a copy that checks out is left alone: libubootenv reads the good one and writes the
//     other, so one torn copy repairs itself on the next write;
//   - the single 8 KiB copy (Bose's, or ours from before the redundant environment) is
//     converted with every entry kept: the U-Boot reads it until then
//     (CONFIG_ENV_REDUNDANT_UPGRADE), but fw_printenv does not;
//   - anything else is rebuilt from what this boot knows: our slot recipes for variant,
//     and bootcmd on the running slot, which just proved it boots.
//
// Both copies are written then, the redundant one first (FreshRedundantFlag): until the
// primary is rewritten it is not a valid copy, so the redundant one boots from the moment
// it is whole. Each is read back.
//
// Under a U-Boot that reads the single copy only a bad CRC is rebuilt, in that format, and
// a valid redundant copy is erased: this U-Boot does not keep it, and the next U-Boot that
// reads it would boot it in place of the one this U-Boot does. Without bootlimit and
// altbootcmd the next install's selection adds those.
func (in *Installer) RepairEnv(read Reader, variant string) (bool, error) {
	if in.Target.EnvMTD < 0 {
		return false, nil
	}

	if !in.Target.EnvRedundant {
		return in.repairLegacyEnv(read, variant)
	}

	copies, lost, err := in.readCopies(read)
	if err != nil {
		return false, err
	}

	primary := copies[ubootenv.Primary]

	err = needsRepair(ubootenv.Current(primary, copies[ubootenv.Redundant]))
	if err != nil {
		return false, nilIfHealthy(err)
	}

	env, err := ubootenv.ParseLegacy(primary)
	if err != nil {
		env, err = in.rebuiltEnv(variant)
		if err != nil {
			return false, err
		}
	}

	return true, in.writeFreshEnv(read, env, lost)
}

// repairLegacyEnv is RepairEnv for a U-Boot that reads the single 8 KiB copy.
func (in *Installer) repairLegacyEnv(read Reader, variant string) (bool, error) {
	wrote, err := in.dropStaleRedundantEnv(read)
	if err != nil {
		return wrote, err
	}

	raw, err := in.dumpEnv(read, in.Target.EnvMTD, 0, ubootenv.LegacySize)
	if err != nil {
		return wrote, err
	}

	_, err = ubootenv.ParseLegacy(raw)
	if !errors.Is(err, ubootenv.ErrBadCRC) {
		if err != nil {
			return wrote, fmt.Errorf("reading the single copy: %w", err)
		}

		return wrote, nil // the copy checks out: nothing to repair
	}

	env, err := in.rebuiltEnv(variant)
	if err != nil {
		return wrote, err
	}

	image, err := env.EncodeLegacy()
	if err != nil {
		return wrote, fmt.Errorf("encoding the single copy: %w", err)
	}

	return true, in.writeEnvCopy(read, in.Target.EnvMTD, 0, image)
}

// dropStaleRedundantEnv erases a redundant copy that checks out while the U-Boot on the
// board reads the single copy. It is left from a U-Boot that read it (an install of an
// older SoundTap), and a newer U-Boot would boot it in place of the copy this one keeps.
func (in *Installer) dropStaleRedundantEnv(read Reader) (bool, error) {
	if _, ok := in.Target.BootloaderSizes[ubootMTD]; !ok {
		return false, nil // no NAND.U-Boot in the table, so no copy there to find
	}

	redundant, err := in.dumpEnv(read, ubootMTD, ubootenv.RedundantOffset, ubootenv.Size)
	if errors.Is(err, errBadBlock) {
		return false, nil // a bad block holds no copy for any U-Boot to boot
	}

	if err != nil {
		return false, err
	}

	_, _, err = ubootenv.ParseCopy(redundant)

	checksOut := err == nil
	if !checksOut {
		return false, nil // a copy that does not check out boots nothing: leave it
	}

	return true, in.eraseEnvCopy(read, ubootMTD, ubootenv.RedundantOffset)
}

// errHealthy stands for an environment that checks out, which needs no repair.
var errHealthy = errors.New("the environment checks out")

// needsRepair turns what ubootenv.Current found into nil when no copy checks out, which is
// what RepairEnv repairs, errHealthy when one does, and the error otherwise.
func needsRepair(_ *ubootenv.Env, _ int, _ byte, err error) error {
	switch {
	case errors.Is(err, ubootenv.ErrBadCRC):
		return nil
	case err == nil:
		return errHealthy
	}

	return fmt.Errorf("reading the environment: %w", err)
}

// nilIfHealthy is the result RepairEnv returns for what needsRepair refused.
func nilIfHealthy(err error) error {
	if errors.Is(err, errHealthy) {
		return nil
	}

	return err
}

// rebuiltEnv is the environment RepairEnv writes when nothing on NAND checks out.
func (in *Installer) rebuiltEnv(variant string) (*ubootenv.Env, error) {
	recipes, err := MainlineRecipes(variant)
	if err != nil {
		return nil, err
	}

	runBoot, err := bootFor(in.Target.Running)
	if err != nil {
		return nil, err
	}

	runCmd := runBoot.bootcmd

	env := ubootenv.NewEnv()

	for _, r := range recipes {
		key, value, _ := strings.Cut(r, "=")
		env.Set(key, value)
	}
	// Only Bose's nand_boot runs these; selecting a slot needs them to exist.
	env.Set("nand_args", "setenv bootargs ${bootargs} ubi.mtd=8,2048")
	env.Set("nand_args2", "setenv bootargs ${bootargs} ubi.mtd=9,2048")
	env.Set(bootcmdKey, runCmd)

	return env, nil
}

// ubootMTD is NAND.U-Boot, whose last erase block holds the redundant environment copy.
// plan holds the live table to this number.
const ubootMTD = 4

// writeFreshEnv writes env as a whole redundant environment: the redundant copy, then the
// primary, each read back. The copy lost to a bad block (lost, -1 for none) is not
// written.
func (in *Installer) writeFreshEnv(read Reader, env *ubootenv.Env, lost int) error {
	redundant, err := env.EncodeCopy(ubootenv.FreshRedundantFlag)
	if err != nil {
		return fmt.Errorf("encoding the redundant copy: %w", err)
	}

	primary, err := env.EncodeCopy(ubootenv.FreshPrimaryFlag)
	if err != nil {
		return fmt.Errorf("encoding the primary copy: %w", err)
	}

	if lost != ubootenv.Redundant {
		err := in.writeEnvCopy(read, ubootMTD, ubootenv.RedundantOffset, redundant)
		if err != nil {
			return err
		}
	}

	if lost == ubootenv.Primary {
		return nil
	}

	return in.writeEnvCopy(read, in.Target.EnvMTD, 0, primary)
}

// UseLegacyEnv puts the environment back into the single 8 KiB copy Bose's U-Boot reads,
// and erases the redundant copy. The way back to stock runs it before it writes Bose's
// bootloaders: from the first write of Bose's U-Boot on, that is the copy the board boots
// from. It keeps every entry, and refuses an environment that no longer fits 8 KiB before
// anything is written. Every moment still boots: while the redundant copy stands our
// U-Boot reads it, and once it is erased our U-Boot reads the single copy as well
// (CONFIG_ENV_REDUNDANT_UPGRADE). Later edits go to the single copy.
func (in *Installer) UseLegacyEnv(read Reader) error {
	if !in.Target.EnvRedundant {
		return nil
	}

	lost, err := in.lostCopy(read)
	if err != nil {
		return err
	}

	if lost == ubootenv.Primary {
		return fmt.Errorf("%s %w", envPart, errBadEnvBlock)
	}

	env, err := in.Env(read)
	if err != nil {
		return err
	}

	image, err := env.EncodeLegacy()
	if err != nil {
		return fmt.Errorf("the environment does not fit the 8 KiB Bose's U-Boot reads: %w", err)
	}

	err = in.writeEnvCopy(read, in.Target.EnvMTD, 0, image)
	if err != nil {
		return err
	}

	if lost != ubootenv.Redundant {
		err := in.eraseEnvCopy(read, ubootMTD, ubootenv.RedundantOffset)
		if err != nil {
			return err
		}
	}

	in.Target.EnvRedundant = false

	return nil
}

// dumpEnv reads size bytes at offset of mtd.
func (in *Installer) dumpEnv(read Reader, mtd, offset, size int) ([]byte, error) {
	args := []string{"--omitoob", "--bb=skipbad", "-l", strconv.Itoa(size)}
	if offset > 0 {
		args = append(args, "-s", strconv.Itoa(offset))
	}

	raw, err := read("nanddump", append(args, in.dev(mtd))...)
	if err == nil && len(raw) == 0 {
		err = fmt.Errorf("mtd%d+%#x: %w", mtd, offset, errBadBlock)
	} else if err == nil && len(raw) < size {
		err = fmt.Errorf("mtd%d+%#x: %w %d bytes", mtd, offset, errShortRead, len(raw))
	}

	return raw, err
}

// writeEnvCopy erases the erase block at offset of mtd, writes image at its start and
// reads it back.
func (in *Installer) writeEnvCopy(read Reader, mtd, offset int, image []byte) error {
	staged, err := stage(image)
	if err != nil {
		return err
	}
	defer os.Remove(staged)

	dev := in.dev(mtd)

	err = in.Run(nil, "flash_erase", dev, strconv.Itoa(offset), "1")
	if err != nil {
		return err
	}

	args := []string{"-p"}
	if offset > 0 {
		args = append(args, "-s", strconv.Itoa(offset))
	}

	err = in.Run(nil, "nandwrite", append(args, dev, staged)...)
	if err != nil {
		return err
	}

	back, err := in.dumpEnv(read, mtd, offset, len(image))
	if err != nil {
		return fmt.Errorf("reading mtd%d+%#x back: %w", mtd, offset, err)
	}

	if !bytes.Equal(back[:len(image)], image) {
		return fmt.Errorf("mtd%d+%#x %w", mtd, offset, errEnvReadback)
	}

	return nil
}

// eraseEnvCopy erases the erase block at offset of mtd and checks it reads back erased.
func (in *Installer) eraseEnvCopy(read Reader, mtd, offset int) error {
	err := in.Run(nil, "flash_erase", in.dev(mtd), strconv.Itoa(offset), "1")
	if err != nil {
		return err
	}

	back, err := in.dumpEnv(read, mtd, offset, ubootenv.Size)
	if err != nil {
		return fmt.Errorf("reading mtd%d+%#x back: %w", mtd, offset, err)
	}

	if !bytes.Equal(back[:ubootenv.Size], bytes.Repeat([]byte{0xff}, ubootenv.Size)) {
		return fmt.Errorf("mtd%d+%#x %w", mtd, offset, errNotErased)
	}

	return nil
}

// HoldRunningSlot points the environment back at the running slot when it selects the
// alternate one -- an earlier install that was never rebooted into -- so the alternate
// can be erased without the next boot landing in a half-written slot. Under our U-Boot
// the kernel comes out of the slot's root filesystem, so erasing the kernel partition
// first gates nothing; only the selection does. It touches bootcmd and altbootcmd
// only: a fallback that names the alternate is pointed at the running slot
// too (ADR 0010), so a boot that fails while the alternate is half-written retries the
// running slot instead of rolling into it. It writes nothing when neither the selection
// nor the fallback names the alternate.
func (in *Installer) HoldRunningSlot(read Reader) error {
	if in.Target.EnvMTD < 0 {
		return nil // no environment: the kernel signature selects, and that is erased first
	}

	running, alt := in.Target.Running, in.Target.Alternate

	runBoot, err := bootFor(running)
	if err != nil {
		return err
	}

	altBoot, err := bootFor(alt)
	if err != nil {
		return err
	}

	runCmd, altCmd := runBoot.bootcmd, altBoot.bootcmd

	return in.rewriteEnv(read, func(env *ubootenv.Env) (bool, error) {
		changed := false

		if v, ok := env.Get(bootcmdKey); ok && sameCommand(v, altCmd) {
			env.Set(bootcmdKey, runCmd)
			// The running slot is confirmed: its boots count in the RTC alone again.
			clearUnconfirmed(env)

			changed = true
		}
		// The fallback must not lead into the slot about to be erased either.
		if v, ok := env.Get(altKey); ok && sameCommand(v, altCmd) {
			env.Set(altKey, runCmd)

			changed = true
		}

		return changed, nil
	})
}

// PendingBootCount is U-Boot's boot count as the environment keeps it for a selection
// that is not confirmed yet (upgrade_available=1), and 0 when there is none.
func (in *Installer) PendingBootCount(read Reader) (uint32, error) {
	env, err := in.Env(read)
	if err != nil {
		return 0, err
	}

	if v, _ := env.Get(pendingKey); v != "1" {
		return 0, nil
	}

	value, _ := env.Get(countKey)
	count, parseErr := strconv.ParseUint(value, 10, 16)

	counted := parseErr == nil
	if !counted {
		return 0, nil // none counted yet
	}

	return uint32(count), nil
}

// ConfirmPendingBoot clears upgrade_available and the count with it once this boot has
// proven itself. In the same write it brings an armed bootlimit to BootLimit: the
// soundtapd that installed this slot wrote its own, and a speaker that is not
// reinstalled keeps it otherwise. It writes nothing when there is nothing to change, so
// a confirmed speaker writes once for a new limit and never again.
//
// Only a selection of the running slot is this boot's to confirm. One of the other slot
// is an install's that has not booted yet: POST /api/reboot settles the boot guard right
// after that install, and a soundtapd restarted within the install's boot confirms again.
// Clearing it there sent the new slot into its trial boots counting in SCRATCH2 alone,
// which a cold reset or a power cut clears (ADR 0010).
func (in *Installer) ConfirmPendingBoot(read Reader) error {
	return in.rewriteEnv(read, func(env *ubootenv.Env) (bool, error) {
		changed := false
		if selected := SelectedSlot(env); selected == "" || selected == in.Target.Running.Name {
			changed = clearUnconfirmed(env)
		}

		if v, ok := env.Get(limitKey); ok && v != strconv.Itoa(BootLimit) {
			env.Set(limitKey, strconv.Itoa(BootLimit))

			changed = true
		}

		return changed, nil
	})
}

// ClearPendingBootCount removes the boot count the environment keeps for a selection on
// trial, and leaves the trial itself: a deliberate reboot before the confirm is not a
// failed attempt, nor a proof. It writes nothing when there is no count.
func (in *Installer) ClearPendingBootCount(read Reader) error {
	return in.rewriteEnv(read, func(env *ubootenv.Env) (bool, error) {
		if _, ok := env.Get(countKey); !ok {
			return false, nil
		}

		env.Delete(countKey)

		return true, nil
	})
}

// SelectedSlot is the slot an environment's bootcmd boots, "" when it names neither.
func SelectedSlot(env *ubootenv.Env) string {
	v, _ := env.Get(bootcmdKey)
	switch {
	case sameCommand(v, bootcmdPing):
		return ping.Name
	case sameCommand(v, bootcmdPong):
		return pong.Name
	}

	return ""
}

// Env reads NAND.U-Boot-Env through fw_printenv, which refuses one whose CRC does
// not check out: the image ships no default environment for it to fall back on.
func (in *Installer) Env(read Reader) (*ubootenv.Env, error) {
	cfg, err := in.envConfig()
	if err != nil {
		return nil, err
	}
	defer os.Remove(cfg)

	env, err := in.readEnv(read, cfg)
	if lost := in.lostBehind(read, err); lost >= 0 {
		env, _, err = in.remainingEnv(read, lost)
	}

	return env, err
}

func (*Installer) readEnv(read Reader, cfg string) (*ubootenv.Env, error) {
	out, err := read("fw_printenv", "-c", cfg)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", envPart, err)
	}

	env, err := ubootenv.ParseText(out)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", envPart, err)
	}

	return env, nil
}

// envConfig writes the fw_env.config that points libubootenv at this board's
// environment, in the format the U-Boot on the board reads: two lines for the redundant
// environment (the primary at the start of NAND.U-Boot-Env, the other copy in the last
// erase block of NAND.U-Boot), or one for the single 8 KiB copy. Each copy owns one erase
// block. The partition number differs per model, so it is written per call.
func (in *Installer) envConfig() (string, error) {
	if in.Target.EnvMTD < 0 {
		return "", fmt.Errorf("the partition table has no %s, %w", envPart, errNoSlotSelectable)
	}

	size := ubootenv.LegacySize
	if in.Target.EnvRedundant {
		size = ubootenv.Size
	}

	if in.Target.EnvSize < int64(size) || in.Target.EnvEraseSize < int64(size) {
		return "", fmt.Errorf("%s %w %d byte environment", envPart, errTooSmallFor, size)
	}

	lines := fmt.Sprintf("%s 0x0 %#x %#x 1\n", in.dev(in.Target.EnvMTD), size, in.Target.EnvEraseSize)
	if in.Target.EnvRedundant {
		lines += fmt.Sprintf("%s %#x %#x %#x 1\n",
			in.dev(ubootMTD), ubootenv.RedundantOffset, size, in.Target.EnvEraseSize)
	}

	config, err := os.CreateTemp("", "soundtap-fw-env-")
	if err != nil {
		return "", fmt.Errorf("writing fw_env.config: %w", err)
	}
	defer config.Close()

	_, err = config.WriteString(lines)
	if err != nil {
		_ = os.Remove(config.Name())

		return "", fmt.Errorf("writing fw_env.config: %w", err)
	}

	return config.Name(), nil
}

// stage writes data to a temporary file, synced, and returns its path; the caller removes
// it.
func stage(data []byte) (string, error) {
	staged, err := os.CreateTemp("", "soundtap-env-")
	if err != nil {
		return "", fmt.Errorf("staging: %w", err)
	}
	defer staged.Close()

	_, err = staged.Write(data)
	if err == nil {
		err = staged.Sync()
	}

	if err != nil {
		_ = os.Remove(staged.Name())

		return "", fmt.Errorf("staging: %w", err)
	}

	return staged.Name(), nil
}

// sameCommand compares two bootcmd values the way U-Boot runs them: a trailing ';'
// and surrounding space change nothing.
func sameCommand(a, b string) bool {
	trim := func(s string) string { return strings.TrimSuffix(strings.TrimSpace(s), ";") }

	return trim(a) == trim(b)
}

// rewriteEnv applies edit to NAND.U-Boot-Env through fw_setenv and reads the result
// back. edit reports whether it changed anything.
func (in *Installer) rewriteEnv(read Reader, edit func(*ubootenv.Env) (bool, error)) error {
	cfg, err := in.envConfig()
	if err != nil {
		return err
	}
	defer os.Remove(cfg)

	current, err := in.readEnv(read, cfg)
	if lost := in.lostBehind(read, err); lost >= 0 {
		return in.rewriteRemaining(read, lost, edit)
	}

	if err != nil {
		return err
	}

	env := current.Clone()

	changed, err := edit(env)
	if err != nil || !changed {
		return err
	}

	script, err := ubootenv.Script(current, env)
	if err != nil {
		return fmt.Errorf("the fw_setenv script: %w", err)
	}

	if len(script) == 0 {
		// Nothing changed. Not erasing is the whole point: this is one erase block
		// and under Bose's U-Boot it is the only copy of the environment.
		return nil
	}

	staged, err := stage(script)
	if err != nil {
		return err
	}
	defer os.Remove(staged)

	err = in.Run(nil, "fw_setenv", "-c", cfg, "-s", staged)
	if err != nil {
		return err
	}

	back, err := in.readEnv(read, cfg)
	if err != nil {
		return fmt.Errorf("reading %s back: %w", envPart, err)
	}

	if !back.Equal(env) {
		return fmt.Errorf("%s %w", envPart, errReadbackFailed)
	}

	return nil
}
