// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Package modeltable is soundtapd's copy of os/models.json, the one table of the speakers
// SoundTap knows (ADR 0008): which model each hardware variant runs as, the STU modules and
// boot chain each model takes, and what each variant's slot boots from /boot. The data is in
// table_gen.go, generated from that file; os/tests/build/test_models_table.py holds it and the
// other copies (init script, installer.c, STU builder, U-Boot) to the same table.
package modeltable

//go:generate python3 ../../../os/support/models_gen.py

// Model is one speaker model, by the name MODEL selects it by.
type Model struct {
	Name string
	// Variant is the board's own hardware variant.
	Variant string
	// ModuleSuffix names the STU modules this model takes (soundtap-kernel.<suffix>).
	ModuleSuffix string
	// SPL and UBoot name the boot chain modules it takes (soundtap-spl.<suffix>).
	SPL, UBoot string
	// KernelName is the model the uImage header names after "Linux-6.12 SoundTap ".
	KernelName string
	// StockRootfsLimit is the largest ubi.img Bose's own installer streamed to this model.
	StockRootfsLimit int64
}

// Variant is one hardware variant, as /proc/variant and variant= on the kernel command line
// name it.
type Variant struct {
	Name, Model string
	// Product is the name /api/info reports for this board.
	Product string
	// ModuleType is the Shelby module: "sm2" or "scm".
	ModuleType string
	// DTB and Kernel are what the slot recipes load from /boot; empty for a stock-only name.
	DTB, Kernel string
	// CodecScript is the speaker's own codec script; empty on a board without a codec.
	CodecScript string
	// StockOnly marks a name only stock firmware reports: no recipe of ours writes it.
	StockOnly bool
}

// ModelNamed is the model MODEL calls name.
func ModelNamed(name string) (Model, bool) {
	for _, m := range Models {
		if m.Name == name {
			return m, true
		}
	}

	var none Model

	return none, false
}

// VariantNamed is the variant a board calls name, matched exactly.
func VariantNamed(name string) (Variant, bool) {
	for _, v := range Variants {
		if v.Name == name {
			return v, true
		}
	}

	var none Variant

	return none, false
}

// IsModuleSuffix reports whether suffix names a model's modules or boot chain, which makes
// a module carrying it another model's unless it is this one's.
func IsModuleSuffix(suffix string) bool {
	for _, m := range Models {
		if suffix == m.ModuleSuffix || suffix == m.SPL || suffix == m.UBoot {
			return true
		}
	}

	return false
}
