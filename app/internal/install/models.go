// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package install holds what an STU install decides and writes outside the HTTP
// handler: which model this speaker is and what a package may do to it, the boot chain
// checks and NAND writes for mtd0-mtd4, the staging helpers and the install record
// support reads after the reboot. internal/web streams the upload and calls these.
package install

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/modeltable"
	"github.com/stein155/soundtap/app/internal/stu"
)

// modelWLA is the Wireless Link's model name.
const modelWLA = "wla"

// PackageModule finds a module the way this speaker would take it: by its name with the
// model suffix stripped, and only when the suffix is this speaker's own or absent. A
// universal package calls the Wireless Link's SPL soundtap-spl.binky.
func PackageModule(pkg *stu.Package, name, model string) (stu.Module, bool) {
	return moduleWithSuffix(pkg, name, VariantForModel(model))
}

// moduleWithSuffix finds the module called name that a speaker taking suffix takes: the
// unsuffixed one or the one carrying that suffix.
func moduleWithSuffix(pkg *stu.Package, name, suffix string) (stu.Module, bool) {
	for _, m := range pkg.Modules {
		if base, mine := stu.ModuleForModel(m.Name, suffix); mine && base == name {
			return m, true
		}
	}
	return stu.Module{}, false
}

// SuffixedKernelModels lists the speakers a universal package carries a kernel for: every
// model whose modules carry that kernel's suffix, so the ST300 with the Wireless Link's.
func SuffixedKernelModels(pkg *stu.Package) []string {
	var models []string
	for _, m := range pkg.Modules {
		if stu.BaseModuleName(m.Name) != "soundtap-kernel" || m.Name == "soundtap-kernel" {
			continue
		}
		suffix := m.Name[strings.LastIndex(m.Name, ".")+1:]
		for _, model := range modeltable.Models {
			if model.ModuleSuffix == suffix {
				models = append(models, strings.ToUpper(model.Name))
			}
		}
	}
	return models
}

// BootloaderModules names the SPL and U-Boot modules in whichever chain this
// package uses: SoundTap's own RTCH chain, or the stock BOSE one it goes back to.
// The model table (os/models.json) says whose chain each model takes: the ST10 and the ST300
// take the Wireless Link's whole sm2 chain and the ST20 its U-Boot, so they look those
// modules up by that suffix. The ST20's SPL is its own build: its ROM boots NAND before USB,
// so its SPL asks USB first.
func BootloaderModules(pkg *stu.Package, model string) (spl, uboot stu.Module, ok bool) {
	m, _ := modeltable.ModelNamed(model)
	for _, names := range [][2]string{{"soundtap-spl", "soundtap-uboot"}, {"MLO", "u-boot.img"}} {
		spl, ok = moduleWithSuffix(pkg, names[0], m.SPL)
		if !ok {
			continue
		}
		uboot, ok = moduleWithSuffix(pkg, names[1], m.UBoot)
		if ok {
			return spl, uboot, true
		}
	}
	return stu.Module{}, stu.Module{}, false
}

// KnownModel refuses every model an install is not proven on: any the model table does not
// name.
func KnownModel(variant string) error {
	if _, ok := modeltable.ModelNamed(variant); !ok {
		return fmt.Errorf("firmware installation is not supported on %s", variant)
	}
	return nil
}

// ModelRefusal judges one module against the hardware it would be written to.
// No model keeps Bose's updater once it runs our boot chain, so refusing a stock
// package here would leave a speaker that can be flashed to SoundTap but never back.
// What keeps an install safe is that flash.Plan writes only the slot the speaker is
// not running from, and refuses outright when it cannot tell which that is.
func ModelRefusal(variant string, m stu.Module) error {
	if err := KnownModel(variant); err != nil {
		return err
	}
	// Bose's ST300 package also carries STP_Update and the .bos for the soundbar
	// processor. This installer writes only SPL, U-Boot, kernel and rootfs by name and
	// skips the rest, so going back to Bose leaves the processor as it is, as SoundTap
	// itself does.
	if !m.Installable(variant) {
		target := m.Target
		if target == "" && len(m.Targets) > 0 {
			target = strings.Join(m.Targets, "/")
		}
		return fmt.Errorf("%s firmware cannot be installed on %s", target, variant)
	}
	// A rootfs bigger than anything Bose has streamed to this exact model is not
	// something the timed installer has ever been shown to survive.
	if (m.Name == "ubi.img" || m.Name == "soundtap-rootfs") && m.Size > stu.RootfsLimit(variant) {
		return fmt.Errorf("ubi.img is %d bytes, larger than any %s package Bose has streamed",
			m.Size, variant)
	}
	return nil
}

// PackageRefusal judges a whole package against the hardware variant.
func PackageRefusal(variant string, pkg *stu.Package) error {
	ubi, ok := pkg.Module("ubi.img")
	if !ok {
		ubi, ok = pkg.Module("soundtap-rootfs")
	}
	if !ok {
		return errors.New("package has no rootfs module")
	}
	if err := ModelRefusal(variant, ubi); err != nil {
		return err
	}
	// One kernel serves every model in a universal package, so the name inside it names
	// whichever model the build was driven from. A kernel module suffixed for this
	// speaker is the package saying it is for this speaker; the install path already
	// trusts that suffix (kernelWasSuffixed), and this gate refused what it would take.
	if _, suffixed := pkg.Module("soundtap-kernel." + VariantForModel(variant)); pkg.Mainline &&
		pkg.KernelTarget != variant && !suffixed {
		return fmt.Errorf("%s kernel cannot be installed on %s", pkg.KernelTarget, variant)
	}
	// Going back to stock is a supported install on every model: all three run our own
	// boot chain, so none has Bose's updater left to do it (ADR 0009), and this endpoint
	// is the way back. The package has to carry the whole boot chain, though. Writing
	// only kernel and rootfs leaves a board running our SPL against Bose's kernel.
	// A Wireless Link package needs it in either direction: its SoundTap packages carry
	// the chain too, and nothing else on it can bring one.
	if !pkg.Mainline || variant == "wla" {
		if _, _, ok := BootloaderModules(pkg, variant); !ok {
			return fmt.Errorf("a %s package must carry SPL and U-Boot: a half-restored boot chain has no way back", strings.ToUpper(variant))
		}
	}
	return nil
}

// LiveModel reads this speaker's model from /proc/variant and the kernel command line.
func LiveModel() (string, error) {
	return ModelFromVariants(LiveVariants()...)
}

// LiveVariants is this speaker's immutable hardware identity: /proc/variant and
// every variant= on the kernel command line. Empty off a speaker.
func LiveVariants() []string {
	variants := []string{}
	if value, err := os.ReadFile("/proc/variant"); err == nil {
		if variant := strings.TrimSpace(string(value)); variant != "" {
			variants = append(variants, variant)
		}
	}
	if value, err := os.ReadFile("/proc/cmdline"); err == nil {
		for _, field := range strings.Fields(string(value)) {
			if variant, ok := strings.CutPrefix(field, "variant="); ok {
				variants = append(variants, variant)
			}
		}
	}
	return variants
}

// ModelFromVariants accepts the variants only when they all name one known model.
func ModelFromVariants(variants ...string) (string, error) {
	models := map[string]bool{}
	for _, variant := range variants {
		model := ModelForVariant(variant)
		if model == "" {
			return "", fmt.Errorf("unknown immutable hardware variant %q", variant)
		}
		models[model] = true
	}
	if len(models) != 1 {
		return "", errors.New("cannot establish one immutable hardware model for firmware install")
	}
	for model := range models {
		return model, nil
	}
	panic("unreachable")
}

// VariantForModel is the suffix a package's modules use for this model (the model table's
// module_suffix). The ST300's module is the Wireless Link's: it takes binky's kernel and
// boot chain, with its own device tree from the rootfs (installer.c's ginger).
func VariantForModel(model string) string {
	m, _ := modeltable.ModelNamed(model)
	return m.ModuleSuffix
}

// BootVariant names the boot recipes for model: this speaker's own variant when it is that
// model and has recipes of its own (mojo and the Series III share another model's modules
// but boot their own device tree and codec script), otherwise the model's.
func BootVariant(model string) string {
	for _, v := range LiveVariants() {
		if ModelForVariant(v) == model {
			if _, err := flash.MainlineRecipes(v); err == nil {
				return v
			}
		}
	}
	// The model's own variant. The ST300's modules are the Wireless Link's; its device
	// tree (ginger) is not.
	m, _ := modeltable.ModelNamed(model)
	return m.Variant
}

// ModelForVariant maps a hardware variant (rhino, spotty, binky, ...) to its model name, as
// the model table (os/models.json) has it: the ST20 and ST30 Series III (spotty3, mojo3) run
// as the ST10, whose module they carry, and the SoundTouch 30 Series I (mojo) and stock's
// lisa as the ST20. "" for a variant the table does not name.
func ModelForVariant(variant string) string {
	v, _ := modeltable.VariantNamed(strings.ToLower(strings.TrimSpace(variant)))
	return v.Model
}
