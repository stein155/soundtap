// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package modeltable

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The generated table is os/models.json: a hand edit of table_gen.go, or a table edit without
// `go generate`, fails here as well as in os/tests/build/test_models_table.py.
func TestGeneratedTableIsModelsJSON(t *testing.T) {
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
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatal(err)
	}

	str := func(s *string) string {
		if s == nil {
			return ""
		}

		return *s
	}

	if len(Models) != len(table.Models) || len(Variants) != len(table.Variants) {
		t.Fatalf("table_gen.go has %d models and %d variants, os/models.json %d and %d",
			len(Models), len(Variants), len(table.Models), len(table.Variants))
	}

	for _, m := range Models {
		j := table.Models[m.Name]

		want := Model{
			Name: m.Name, Variant: j.Variant, ModuleSuffix: j.ModuleSuffix, SPL: j.SPL,
			UBoot: j.UBoot, KernelName: j.KernelName, StockRootfsLimit: j.StockRootfsLimit,
		}
		if !reflect.DeepEqual(m, want) {
			t.Errorf("model %s: table_gen.go %+v, os/models.json %+v", m.Name, m, want)
		}
	}

	for _, v := range Variants {
		j := table.Variants[v.Name]

		want := Variant{
			Name: v.Name, Model: j.Model, Product: j.Product, ModuleType: j.ModuleType,
			DTB: str(j.DTB), Kernel: str(j.Kernel), CodecScript: str(j.CodecScript), StockOnly: j.StockOnly,
		}
		if v != want {
			t.Errorf("variant %s: table_gen.go %+v, os/models.json %+v", v.Name, v, want)
		}
	}

	limits := map[string]int64{
		"st10": StockRootfsLimitST10, "st20": StockRootfsLimitST20,
		"wla": StockRootfsLimitWLA, "st300": StockRootfsLimitST300,
	}
	for name, limit := range limits {
		if table.Models[name].StockRootfsLimit != limit {
			t.Errorf("StockRootfsLimit%s is %d, os/models.json %d", name, limit, table.Models[name].StockRootfsLimit)
		}
	}
}

func TestLookups(t *testing.T) {
	if m, ok := ModelNamed("st300"); !ok || m.ModuleSuffix != "binky" {
		t.Errorf("ModelNamed(st300) = %+v, %v", m, ok)
	}

	if _, ok := ModelNamed("st30"); ok {
		t.Error("ModelNamed(st30): the SoundTouch 30 runs as an ST20, not as a model of its own")
	}

	if v, ok := VariantNamed("mojo3"); !ok || v.Model != "st10" {
		t.Errorf("VariantNamed(mojo3) = %+v, %v", v, ok)
	}

	if _, ok := VariantNamed("Rhino"); ok {
		t.Error("VariantNamed matches exactly")
	}

	for suffix, want := range map[string]bool{"rhino": true, "binky": true, "spotty": true, "ginger": false, "mojo": false, "img": false} {
		if IsModuleSuffix(suffix) != want {
			t.Errorf("IsModuleSuffix(%q) = %v", suffix, !want)
		}
	}
}
