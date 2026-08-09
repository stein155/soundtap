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
	for _, v := range modeltable.Variants {
		t.Run(v.Name, func(t *testing.T) {
			m, err := forVariants([]string{v.Name})
			if err != nil {
				t.Fatal(err)
			}
			if m.Name != v.Model || m.Type != v.Product || m.AICConfig != v.CodecScript {
				t.Errorf("profile is %s %q codec %q, the table says %s %q codec %q",
					m.Name, m.Type, m.AICConfig, v.Model, v.Product, v.CodecScript)
			}
			if got := install.ModelForVariant(v.Name); got != v.Model {
				t.Errorf("install.ModelForVariant = %q, want %q", got, v.Model)
			}
			recipes, err := flash.MainlineRecipes(v.Name)
			if v.StockOnly {
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
				{2, "ubifsload 0x83000000 /boot/" + v.DTB + ";"},
				{2, "ubifsload 0x82000000 /boot/" + v.Kernel + ";"},
				{3, " variant=" + v.Name + " module_type=" + v.ModuleType + " "},
				{4, " variant=" + v.Name + " "},
			}
			for _, w := range want {
				if !strings.Contains(recipes[w.index], w.text) {
					t.Errorf("recipe %d lacks %q: %s", w.index, w.text, recipes[w.index])
				}
			}
		})
	}
	for _, m := range modeltable.Models {
		t.Run(m.Name, func(t *testing.T) {
			own, _ := modeltable.VariantNamed(m.Variant)
			if got := Named(m.Name); got.Name != m.Name || got.Type != own.Product {
				t.Errorf("Named(%s) is %s %q, want %q", m.Name, got.Name, got.Type, own.Product)
			}
			if got := install.VariantForModel(m.Name); got != m.ModuleSuffix {
				t.Errorf("install.VariantForModel = %q, want %q", got, m.ModuleSuffix)
			}
			if err := install.KnownModel(m.Name); err != nil {
				t.Error(err)
			}
			if got := stu.RootfsLimit(m.Name); got != m.StockRootfsLimit {
				t.Errorf("stu.RootfsLimit = %d, want %d", got, m.StockRootfsLimit)
			}
			// The universal package: this model takes its own kernel and its own boot chain.
			pkg := &stu.Package{}
			for _, s := range []string{"rhino", "binky", "spotty"} {
				for _, base := range []string{"soundtap-kernel", "soundtap-spl", "soundtap-uboot"} {
					pkg.Modules = append(pkg.Modules, stu.Module{Name: base + "." + s})
				}
			}
			kernel, _ := install.PackageModule(pkg, "soundtap-kernel", m.Name)
			spl, uboot, ok := install.BootloaderModules(pkg, m.Name)
			if !ok || kernel.Name != "soundtap-kernel."+m.ModuleSuffix ||
				spl.Name != "soundtap-spl."+m.SPL || uboot.Name != "soundtap-uboot."+m.UBoot {
				t.Errorf("takes %q, %q and %q (ok %v); the table says kernel %s, SPL %s, U-Boot %s",
					kernel.Name, spl.Name, uboot.Name, ok, m.ModuleSuffix, m.SPL, m.UBoot)
			}
		})
	}
	if err := install.KnownModel("st30"); err == nil {
		t.Error("install.KnownModel takes a model the table does not name")
	}
}
