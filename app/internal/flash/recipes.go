// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stein155/soundtap/app/internal/modeltable"
	"github.com/stein155/soundtap/app/internal/ubootenv"
)

// mainlineTemplates are the slot boot recipes our U-Boot runs, as
// os/installer/module/installer.c carries them in mainline_env_templates and
// os/support/uboot_recipes.py writes them. That Python file explains every byte
// and is the copy to edit first; os/tests/installer/test_wla_env_recipes.py fails the build
// when the three drift apart. Index 2 takes the device tree and kernel, 3 the variant
// and module type, 4 the variant.
//
// Bose's own nand_boot/nand_boot2 use nboot.e, which mainline U-Boot does not have, so
// an install that puts our bootloader in front of Bose's recipes leaves a board that
// does not boot. These run under Bose's U-Boot too, which is why they are written before
// the bootloader is: every moment in between still boots.
func mainlineTemplates() []string {
	return []string{
		"nand_boot=setenv slot Ping; setenv rmtd 8; setenv kaddr 0x280000; run slot_boot",
		"nand_boot2=setenv slot Pong; setenv rmtd 9; setenv kaddr 0x780000; run slot_boot",
		"slot_boot=if ubi part NAND.File-System-${slot} 2048; then if ubifsmount ubi0:rootfs; " +
			"then if ubifsload 0x83000000 /boot/%s; then mw.l 0x82000000 0 1; ubifsload " +
			"0x82000000 /boot/%s; if itest.l *0x82000000 == 0x56190527; then run " +
			"soundtap_args; bootm 0x82000000 - 0x83000000; fi; run stock_args; if nand read 0x80007fc0 " +
			"${kaddr} 0x500000; then if fixsig 0x80007fc0; then bootm 0x80007fc0 - 0x83000000; fi; " +
			"fi; fi; fi; fi; reset",
		"soundtap_args=setenv bootargs console=ttyGS0,115200n8 root=ubi0:rootfs ro " +
			"rootfstype=ubifs ubi.mtd=${rmtd},2048 rootwait init=/init mem=128M cma=8M panic=10 " +
			"omap_wdt.early_enable=1 variant=%s module_type=%s country=GB region=GB " +
			"soundtap.nandroot=1 ubi.mtd=NAND.Persistent-Storage,2048",
		"stock_args=setenv bootargs console=ttyO3,115200n8 quiet root=ubi0:rootfs ro " +
			"ubi.mtd=${rmtd},2048 noinitrd rootfstype=ubifs rootwait=1 ip=off variant=%s " +
			"variant_raw=${variant_raw} variant_mode=normal oled_type= country=GB region=GB",
		"mtdids=nand0=nand0",
		//nolint:lll // one U-Boot value, kept whole and byte-identical to installer.c and uboot_recipes.py.
		"mtdparts=mtdparts=nand0:0x20000@0x0(NAND.SPL),0x20000@0x20000(NAND.SPL.backup1),0x20000@0x40000(NAND.SPL.backup2),0x20000@0x60000(NAND.SPL.backup3),0x1E0000@0x80000(NAND.U-Boot),0x20000@0x260000(NAND.U-Boot-Env),0x500000@0x280000(NAND.Kernel-Ping),0x500000@0x780000(NAND.Kernel-Pong),0x6000000@0xC80000(NAND.File-System-Ping),0x6000000@0x6C80000(NAND.File-System-Pong),0x2600000@0xCC80000(NAND.Persistent-Storage),0xB80000@0xF280000(NAND.Update-Staging),0x40000@0xFE00000(NAND.Manufacturing-Data)",
	}
}

// recipeModels is the model table's boot files per variant (os/models.json, as installer.c's
// model_boots carries them): device tree, kernel, module type. A name only stock reports
// has none.
func recipeModels() map[string][3]string {
	out := map[string][3]string{}

	for _, v := range modeltable.Variants {
		if !v.StockOnly {
			out[v.Name] = [3]string{v.DTB, v.Kernel, v.ModuleType}
		}
	}

	return out
}

// MainlineRecipes fills the templates for one variant, as installer.c does.
func MainlineRecipes(variant string) ([]string, error) {
	files, ok := recipeModels()[variant]
	if !ok {
		//nolint:err113 // recipes.go stays literal-identical to HEAD; this message is one of its literals.
		return nil, fmt.Errorf("no boot recipes for variant %q", variant)
	}

	templates := mainlineTemplates()

	out := make([]string, len(templates))
	for index, template := range templates {
		switch index {
		case 2: //nolint:mnd // template index 2, as the doc comment names it; recipes.go stays literal-identical.
			out[index] = fmt.Sprintf(template, files[0], files[1])
		case 3: //nolint:mnd // template index 3, as above.
			out[index] = fmt.Sprintf(template, variant, files[2])
		case 4: //nolint:mnd // template index 4, as above.
			out[index] = fmt.Sprintf(template, variant)
		default:
			out[index] = template
		}
	}

	return out, nil
}

// InstallMainlineRecipes writes the slot recipes for variant into NAND.U-Boot-Env,
// replacing Bose's. It refuses an environment that has no nand_boot and nand_boot2 (not
// one of ours to rewrite) and one the recipes would not fit (Encode fails closed), and
// writes nothing when they are already there.
func (in *Installer) InstallMainlineRecipes(read Reader, variant string) error {
	recipes, err := MainlineRecipes(variant)
	if err != nil {
		return err
	}
	// Our U-Boot's console is UART0, which on the ST300 is the soundbar processor's link:
	// a byte from it during bootdelay would stop autoboot. -2 never looks at the console.
	// Same entry as the on-box installer writes (os/installer/module/installer.c).
	if variant == "ginger" {
		recipes = append(recipes, "bootdelay=-2")
	}

	return in.rewriteEnv(read, func(env *ubootenv.Env) (bool, error) {
		return applyRecipes(env, recipes)
	})
}

func applyRecipes(env *ubootenv.Env, recipes []string) (bool, error) {
	for _, key := range []string{"nand_boot", "nand_boot2"} {
		if _, ok := env.Get(key); !ok {
			//nolint:err113 // recipes.go stays literal-identical to HEAD; this message is one of its literals.
			return false, fmt.Errorf("the environment has no %s; refusing to install boot recipes", key)
		}
	}

	changed := false

	for _, entry := range recipes {
		key, value, _ := strings.Cut(entry, "=")
		if v, ok := env.Get(key); !ok || v != value {
			env.Set(key, value)

			changed = true
		}
	}

	return changed, nil
}

// stockSlotRecipe is what Bose's U-Boot has to do to boot one slot, written by us: set the
// slot's kernel command line, attach the slot's UBI partition, load this board's device
// tree from /boot, read the kernel from the slot's kernel partition with nboot.e, let
// fixsig fix its header, and bootm it with that device tree. The args entry goes first
// because it only reads variables none of the later steps set. %[1]s is the partition
// suffix, %[2]s the slot's args entry, %[3]s the variable holding its kernel offset; every
// model keeps those variables under the same names, so one recipe serves all of them.
const stockSlotRecipe = "run %[2]s; mtdparts default; ubi part NAND.File-System-%[1]s 2048; ubifsmount ubi0; " +
	"ubifsload ${loadaddr} /boot/${variant}_${module_type}.dtb; fdt addr ${loadaddr}; " +
	"nboot.e ${kloadaddr} 0 ${%[3]s}; fixsig; bootm ${kloadaddr} - ${loadaddr}"

// stockRecipes are the two slot recipes the way back to stock writes.
func stockRecipes() []string {
	return []string{
		"nand_boot=" + fmt.Sprintf(stockSlotRecipe, "Ping", "nand_args", "kernel0_nand_address"),
		"nand_boot2=" + fmt.Sprintf(stockSlotRecipe, "Pong", "nand_args2", "kernel1_nand_address"),
	}
}

// EnvKeys are the environment entries this package changes through fw_setenv: the slot
// selection and its rollback, the boot recipes, the markers' args entries and the retired
// owner key, which a selection removes. soundtap-root refuses a fw_setenv script that
// touches any other entry (ADR 0066); a new entry here is a new entry there. Whole-copy
// writes (RepairEnv, UseLegacyEnv) go through nandwrite and are held to the environment's
// place in NAND instead.
func EnvKeys() []string {
	keys := []string{bootcmdKey, altKey, limitKey, pendingKey, countKey, "bootdelay", retiredOwnerKey}

	for _, slot := range []Slot{ping, pong} {
		boot, err := bootFor(slot)
		if err == nil {
			keys = append(keys, boot.cmdKey, boot.argsKey) // the markers go into the args entry
		}
	}

	for _, entry := range append(mainlineTemplates(), stockRecipes()...) {
		key, _, _ := strings.Cut(entry, "=")
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return slices.Compact(keys)
}

// InstallStockRecipes is the way back: slot recipes Bose's U-Boot boots in place of ours,
// and the entries only our U-Boot reads removed, so a board back on Bose's bootloader has
// an environment that bootloader understands. Run it once Bose's bootloader is on NAND:
// the stock recipes use nboot.e, which our U-Boot does not have.
func (in *Installer) InstallStockRecipes(read Reader) error {
	return in.rewriteEnv(read, func(env *ubootenv.Env) (bool, error) {
		changed, err := applyRecipes(env, stockRecipes())
		if err != nil {
			return false, err
		}
		// Ours on the ST300 only (InstallMainlineRecipes); Bose's environment has 1.
		if v, ok := env.Get("bootdelay"); ok && v == "-2" {
			env.Set("bootdelay", "1")

			changed = true
		}

		for _, entry := range mainlineTemplates()[2:] {
			key, _, _ := strings.Cut(entry, "=")
			if _, ok := env.Get(key); ok {
				env.Delete(key)

				changed = true
			}
		}

		return changed, nil
	})
}
