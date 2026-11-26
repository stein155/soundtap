// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package model

import (
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/flash"
	"github.com/stein155/soundtap/app/internal/install"
	"github.com/stein155/soundtap/app/internal/modeltable"
	"github.com/stein155/soundtap/app/internal/stu"
)

// TestModelTable holds soundtapd's per-model code to the model table (os/models.json, as
// internal/modeltable carries it): the profile each variant gets, the model and package
// suffix the install path reads, the boot recipes and the rootfs ceiling.
// os/tests/build/test_models_table.py runs it with the table's other copies.
func TestModelTable(t *testing.T) {
	t.Parallel()

	for _, variant := range modeltable.Variants {
		t.Run(variant.Name, func(t *testing.T) {
			t.Parallel()

			profile, err := forVariants([]string{variant.Name})
			if err != nil {
				t.Fatal(err)
			}

			if profile.Name != variant.Model || profile.Type != variant.Product ||
				profile.AICConfig != variant.CodecScript {
				t.Errorf("profile is %s %q codec %q, the table says %s %q codec %q",
					profile.Name, profile.Type, profile.AICConfig, variant.Model, variant.Product, variant.CodecScript)
			}

			if got := install.ModelForVariant(variant.Name); got != variant.Model {
				t.Errorf("install.ModelForVariant = %q, want %q", got, variant.Model)
			}

			recipes, err := flash.MainlineRecipes(variant.Name)
			if variant.StockOnly {
				if err == nil {
					t.Error("a name only stock reports has boot recipes")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			want := []struct {
				index int
				text  string
			}{
				{2, "ubifsload 0x83000000 /boot/" + variant.DTB + ";"},
				{2, "ubifsload 0x82000000 /boot/" + variant.Kernel + ";"},
				{3, " variant=" + variant.Name + " module_type=" + variant.ModuleType + " "},
				{4, " variant=" + variant.Name + " "},
			}
			for _, w := range want {
				if !strings.Contains(recipes[w.index], w.text) {
					t.Errorf("recipe %d lacks %q: %s", w.index, w.text, recipes[w.index])
				}
			}
		})
	}

	for _, entry := range modeltable.Models {
		t.Run(entry.Name, func(t *testing.T) {
			t.Parallel()

			own, _ := modeltable.VariantNamed(entry.Variant)
			if got := Named(entry.Name); got.Name != entry.Name || got.Type != own.Product {
				t.Errorf("Named(%s) is %s %q, want %q", entry.Name, got.Name, got.Type, own.Product)
			}

			if got := install.VariantForModel(entry.Name); got != entry.ModuleSuffix {
				t.Errorf("install.VariantForModel = %q, want %q", got, entry.ModuleSuffix)
			}

			err := install.KnownModel(entry.Name)
			if err != nil {
				t.Error(err)
			}

			if got := stu.RootfsLimit(entry.Name); got != entry.StockRootfsLimit {
				t.Errorf("stu.RootfsLimit = %d, want %d", got, entry.StockRootfsLimit)
			}
			// The universal package: this model takes its own kernel and its own boot chain.
			pkg := &stu.Package{
				Size: 0, Modules: nil, Target: "", Targets: nil, Mainline: false, KernelTarget: "", Native: false,
			}

			for _, s := range []string{"rhino", "binky", "spotty"} {
				for _, base := range []string{"soundtap-kernel", "soundtap-spl", "soundtap-uboot"} {
					pkg.Modules = append(pkg.Modules, stu.Module{
						Name: base + "." + s, Size: 0, Target: "", Targets: nil, Native: false,
					})
				}
			}

			kernel, _ := install.PackageModule(pkg, "soundtap-kernel", entry.Name)

			spl, uboot, ok := install.BootloaderModules(pkg, entry.Name)
			if !ok || kernel.Name != "soundtap-kernel."+entry.ModuleSuffix ||
				spl.Name != "soundtap-spl."+entry.SPL || uboot.Name != "soundtap-uboot."+entry.UBoot {
				t.Errorf("takes %q, %q and %q (ok %v); the table says kernel %s, SPL %s, U-Boot %s",
					kernel.Name, spl.Name, uboot.Name, ok, entry.ModuleSuffix, entry.SPL, entry.UBoot)
			}
		})
	}

	err := install.KnownModel("st30")
	if err == nil {
		t.Error("install.KnownModel takes a model the table does not name")
	}
}
