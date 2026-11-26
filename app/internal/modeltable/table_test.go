// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package modeltable_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/stein155/soundtap/app/internal/modeltable"
)

// The generated table is os/models.json: a hand edit of table_gen.go, or a table edit without
// `go generate`, fails here as well as in os/tests/build/test_models_table.py.
func TestGeneratedTableIsModelsJSON(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../os/models.json")
	if err != nil {
		t.Fatal(err)
	}

	type model struct {
		Variant          string `json:"variant"`
		ModuleSuffix     string `json:"module_suffix"`
		SPL              string `json:"spl"`
		UBoot            string `json:"uboot"`
		KernelName       string `json:"kernel_name"`
		StockRootfsLimit int64  `json:"stock_rootfs_limit"`
	}

	type variant struct {
		Model       string  `json:"model"`
		Product     string  `json:"product"`
		ModuleType  string  `json:"module_type"`
		DTB         *string `json:"dtb"`
		Kernel      *string `json:"kernel"`
		CodecScript *string `json:"codec_script"`
		StockOnly   bool    `json:"stock_only"`
	}

	var table struct {
		Models   map[string]model   `json:"models"`
		Variants map[string]variant `json:"variants"`
	}

	err = json.Unmarshal(raw, &table)
	if err != nil {
		t.Fatal(err)
	}

	str := func(s *string) string {
		if s == nil {
			return ""
		}

		return *s
	}

	if len(modeltable.Models) != len(table.Models) || len(modeltable.Variants) != len(table.Variants) {
		t.Fatalf("table_gen.go has %d models and %d variants, os/models.json %d and %d",
			len(modeltable.Models), len(modeltable.Variants), len(table.Models), len(table.Variants))
	}

	for _, model := range modeltable.Models {
		j := table.Models[model.Name]

		want := modeltable.Model{
			Name: model.Name, Variant: j.Variant, ModuleSuffix: j.ModuleSuffix, SPL: j.SPL,
			UBoot: j.UBoot, KernelName: j.KernelName, StockRootfsLimit: j.StockRootfsLimit,
		}
		if !reflect.DeepEqual(model, want) {
			t.Errorf("model %s: table_gen.go %+v, os/models.json %+v", model.Name, model, want)
		}
	}

	for _, variant := range modeltable.Variants {
		j := table.Variants[variant.Name]

		want := modeltable.Variant{
			Name: variant.Name, Model: j.Model, Product: j.Product, ModuleType: j.ModuleType,
			DTB: str(j.DTB), Kernel: str(j.Kernel), CodecScript: str(j.CodecScript), StockOnly: j.StockOnly,
		}
		if variant != want {
			t.Errorf("variant %s: table_gen.go %+v, os/models.json %+v", variant.Name, variant, want)
		}
	}

	limits := map[string]int64{
		"st10": modeltable.StockRootfsLimitST10, "st20": modeltable.StockRootfsLimitST20,
		"wla": modeltable.StockRootfsLimitWLA, "st300": modeltable.StockRootfsLimitST300,
	}
	for name, limit := range limits {
		if table.Models[name].StockRootfsLimit != limit {
			t.Errorf("StockRootfsLimit%s is %d, os/models.json %d", name, limit, table.Models[name].StockRootfsLimit)
		}
	}
}

func TestLookups(t *testing.T) {
	t.Parallel()

	if m, ok := modeltable.ModelNamed("st300"); !ok || m.ModuleSuffix != "binky" {
		t.Errorf("ModelNamed(st300) = %+v, %v", m, ok)
	}

	if _, ok := modeltable.ModelNamed("st30"); ok {
		t.Error("ModelNamed(st30): the SoundTouch 30 runs as an ST20, not as a model of its own")
	}

	if v, ok := modeltable.VariantNamed("mojo3"); !ok || v.Model != "st10" {
		t.Errorf("VariantNamed(mojo3) = %+v, %v", v, ok)
	}

	if _, ok := modeltable.VariantNamed("Rhino"); ok {
		t.Error("VariantNamed matches exactly")
	}

	suffixes := map[string]bool{
		"rhino": true, "binky": true, "spotty": true, "ginger": false, "mojo": false, "img": false,
	}
	for suffix, want := range suffixes {
		if modeltable.IsModuleSuffix(suffix) != want {
			t.Errorf("IsModuleSuffix(%q) = %v", suffix, !want)
		}
	}
}
