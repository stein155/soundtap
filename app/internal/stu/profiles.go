// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package stu

import (
	"errors"
	"fmt"
	"strings"

	"github.com/stein155/soundtap/app/internal/modeltable"
)

// Names the registry and the scanner both compare against.
const (
	magicBose  = "BOSE"
	modelWLA   = "wla"
	modelST10  = "st10"
	modelST20  = "st20"
	modelBinky = "binky"

	modRootfs      = "soundtap-rootfs"
	modUBI         = "ubi.img"
	modLinux       = "linuxPatched"
	modKernel      = "soundtap-kernel"
	modUbootUpdate = "UbootUpdate"
	modUboot       = "u-boot.img"
	modInstaller   = "soundtap-installer"
	modSWInstaller = "SoftwareUpdateInstaller"
	modMLO         = "MLO"
	modARMUpdate   = "ARMUpdate"
)

// The module chains, in order. Any other chain is not a package this speaker knows how to
// install, whatever else it may be. Other packages' tests build packages from them.
//
//nolint:gochecknoglobals // exported chains the firmware tests use; functions would change that API.
var (
	// Expected is the stock chain of the ST10 and the Wireless Link.
	Expected = []string{
		modSWInstaller, modARMUpdate, modUbootUpdate, "SCMReset_Update",
		modLinux, modUBI, modMLO, modUboot,
	}

	// ExpectedST20 is the ST20's stock chain.
	ExpectedST20 = []string{
		modSWInstaller, modARMUpdate, "CC2540_Update", "SMSC_Update",
		modUbootUpdate, "SCMReset_Update", modLinux, modUBI,
		"SCMBLC_CC2540_6.0.18.738.bin",
		"Bootloader_CE2_Release_8MB_3.x-3_8_0-942",
		"App_BD_DMP 3.x-3_8_0-9423_ER.bcd", modMLO, modUboot,
	}

	// ExpectedWLANative is the repo-native RTCH chain.
	ExpectedWLANative = []string{
		modKernel, modRootfs, "soundtap-spl", "soundtap-uboot",
	}

	// ExpectedSoundTap is the universal chain: our installer first, because stock executes module
	// 0 by position, then the four payloads under their own names. It wears BOSE magic because
	// that is the one thing stock authenticates -- measured 2026-09-18 -- and nothing inside it is Bose's.
	ExpectedSoundTap = []string{
		modInstaller, modKernel, modRootfs, "soundtap-spl", "soundtap-uboot",
	}

	// ExpectedSoundTapDuo is the same universal package with the kernel split per model and
	// the sm2 bootloaders marked binky; the ST10 is the same sm2 module and takes them too.
	// The suffix is how Bose's own installer picks a payload, and ours follows it:
	// ModuleForModel decides what this speaker takes and what belongs to another one.
	ExpectedSoundTapDuo = []string{
		modInstaller, "soundtap-kernel.rhino", "soundtap-kernel.binky",
		modRootfs, "soundtap-spl.binky", "soundtap-uboot.binky",
	}

	// ExpectedSoundTapTri is the duo chain with the ST20's kernel and SPL added. What the
	// builder emits when an ST20 kernel is built alongside; ST10 and Wireless Link take it exactly
	// as they take the duo, and the spotty payloads are discarded as another model's.
	ExpectedSoundTapTri = []string{
		modInstaller, "soundtap-kernel.rhino", "soundtap-kernel.binky", "soundtap-kernel.spotty",
		modRootfs, "soundtap-spl.binky", "soundtap-uboot.binky",
		"soundtap-spl.spotty",
	}

	// ExpectedST300 is the ST300's stock chain.
	ExpectedST300 = []string{
		modSWInstaller, modARMUpdate, modUbootUpdate, "STP_Update",
		modLinux, modUBI, modMLO, modUboot, "ginger_1.16.7.5043495.bos",
	}
)

// BaseModuleName strips a model suffix, whoever the module is for. Use it where the
// question is what kind of payload this is; ModuleForModel is for whose it is.
func BaseModuleName(name string) string {
	dot := strings.LastIndex(name, ".")
	if dot < 0 {
		return name
	}

	if modeltable.IsModuleSuffix(name[dot+1:]) {
		return name[:dot]
	}

	return name
}

// ModuleForModel returns the name a module carries once its model suffix is stripped, and
// false when the suffix names a different speaker. An unsuffixed module is for every model.
// The suffixes are the model table's (rhino, spotty, binky).
func ModuleForModel(name, variant string) (string, bool) {
	dot := strings.LastIndex(name, ".")
	if dot < 0 {
		return name, true
	}

	if !modeltable.IsModuleSuffix(name[dot+1:]) {
		return name, true
	}

	if name[dot+1:] != variant {
		return "", false
	}

	return name[:dot], true
}

type profile struct {
	modules   []string
	maxRootfs int64
	target    string
	magic     string
	native    bool // one of ours, whatever label it wears for stock
}

// The Wireless Link ships the same eight module names as the ST10 — the chain
// says which family a package belongs to, not which model. Only the rootfs
// ceiling differs, and a scan that cannot yet tell the two apart holds the
// package to the loosest of them; the exact one is enforced against the hardware
// by RootfsLimit, where the model is actually known. A new map on every call, so no
// caller can change the table another one reads.
func profiles() map[string]profile {
	return map[string]profile{
		modelST10: stockProfile(Expected, stockST10UBIModuleSize, modelST10),
		modelST20: stockProfile(ExpectedST20, stockST20UBIModuleSize, modelST20),
		modelWLA: {
			modules: ExpectedWLANative, maxRootfs: stockWLAUBIModuleSize, target: modelWLA,
			magic: "RTCH", native: true,
		},
		"wla-stock": stockProfile(Expected, stockWLAUBIModuleSize, modelWLA),
		"soundtap":  nativeProfile(ExpectedSoundTap, stockWLAUBIModuleSize, modelWLA),
		// The same universal chain on an ST10. One package installs on either speaker,
		// so its chain matches both models and the mainline kernel name picks between
		// them, exactly as the shared eight-module stock chain does.
		"soundtap-st10": nativeProfile(ExpectedSoundTap, stockST10UBIModuleSize, modelST10),
		// The duo chain, once per model it installs on, for the same reason as above.
		"soundtap-duo":       nativeProfile(ExpectedSoundTapDuo, stockWLAUBIModuleSize, modelWLA),
		"soundtap-duo-st10":  nativeProfile(ExpectedSoundTapDuo, stockST10UBIModuleSize, modelST10),
		"soundtap-tri":       nativeProfile(ExpectedSoundTapTri, stockWLAUBIModuleSize, modelWLA),
		"soundtap-tri-st10":  nativeProfile(ExpectedSoundTapTri, stockST10UBIModuleSize, modelST10),
		"soundtap-tri-st20":  nativeProfile(ExpectedSoundTapTri, stockST20UBIModuleSize, modelST20),
		"soundtap-tri-st300": nativeProfile(ExpectedSoundTapTri, stockST300UBIModuleSize, modelST300),
		// Bose's own, the ST300's way back. STP_Update and the .bos are never written.
		modelST300: stockProfile(ExpectedST300, stockST300UBIModuleSize, modelST300),
	}
}

// stockProfile is a chain Bose's updater runs, under BOSE magic.
func stockProfile(modules []string, maxRootfs int64, target string) profile {
	return profile{modules: modules, maxRootfs: maxRootfs, target: target, magic: magicBose, native: false}
}

// nativeProfile is one of our chains wearing BOSE magic for stock's benefit.
func nativeProfile(modules []string, maxRootfs int64, target string) profile {
	return profile{modules: modules, maxRootfs: maxRootfs, target: target, magic: magicBose, native: true}
}

// RootfsLimit is the largest ubi.img Bose's own installer has streamed to this
// model. Zero means the model is not one this speaker installs to at all.
func RootfsLimit(model string) int64 {
	return profiles()[model].maxRootfs
}

// The ways a profile table is refused; each message reads the names around its sentinel.
var (
	errInvalidProfile = errors.New("invalid STU profile")
	errDiverge        = errors.New("diverge after ubi.img")
	errPrefixOf       = errors.New("is a prefix of")
)

// magicSize is the four-byte magic at the start of a package.
const magicSize = 4

// validateProfileRegistry holds the table to what the scanner relies on: every chain has a
// rootfs, two chains part before it, and no chain is a prefix of another.
func validateProfileRegistry(registry map[string]profile) error { //nolint:gocognit,cyclop // moved unchanged
	for name, candidate := range registry {
		ubi := -1

		for i, module := range candidate.modules {
			if module == modUBI || module == modRootfs {
				ubi = i

				break
			}
		}

		if name == "" || candidate.target == "" || len(candidate.modules) == 0 ||
			candidate.maxRootfs <= overlap || ubi < 0 || len(candidate.magic) != magicSize {
			return fmt.Errorf("%w %q", errInvalidProfile, name)
		}

		for otherName, other := range registry {
			if name == otherName {
				continue
			}

			common := min(len(other.modules), len(candidate.modules))

			diverged := false

			for i := range common {
				if candidate.modules[i] != other.modules[i] {
					diverged = true

					if i > ubi {
						return fmt.Errorf("STU profiles %q and %q %w", name, otherName, errDiverge)
					}

					break
				}
			}
			// Two models may share a chain exactly — the ST10 and the Wireless
			// Link do. What must never happen is one chain being a prefix of a
			// longer one, because then a truncated package of the longer model
			// would read as a complete package of the shorter.
			if !diverged && len(candidate.modules) != len(other.modules) {
				return fmt.Errorf("STU profile %q %w %q", name, errPrefixOf, otherName)
			}
		}
	}

	return nil
}
