// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package flash

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/ubootenv"
	"github.com/stein155/soundtap/app/internal/ubootenv/ubootenvtest"
)

// envNAND is the environment's two blocks, mtd5 and the last of mtd4, holding our
// redundant environment: what fw_setenv put there, what fw_printenv reads back.
type envNAND struct {
	nand   map[string][]byte
	writes int
}

func newEnvNAND(t *testing.T, edit func(*ubootenv.Env)) *envNAND {
	t.Helper()
	env := factoryEnv(t)
	edit(env)
	n := &envNAND{nand: map[string][]byte{}}
	if err := ubootenvtest.Image(n.nand, "/dev/mtd5", "/dev/mtd4", env); err != nil {
		t.Fatal(err)
	}
	return n
}

func (n *envNAND) installer(running, alt Slot) *Installer {
	return &Installer{
		Target: Target{Running: running, Alternate: alt, EnvMTD: 5, EnvSize: 0x20000, EnvEraseSize: 0x20000,
			EnvRedundant: true},
		Run: func(_ io.Reader, name string, args ...string) error {
			if name == "fw_setenv" {
				if err := ubootenvtest.SetEnv(n.nand, args); err != nil {
					return err
				}
				n.writes++
			}
			return nil
		},
	}
}

func (n *envNAND) read(_ string, args ...string) ([]byte, error) {
	return ubootenvtest.PrintEnv(n.nand, args)
}

func (n *envNAND) env(t *testing.T) *ubootenv.Env {
	t.Helper()
	env, err := ubootenvtest.Load(n.nand, "/dev/mtd5", "/dev/mtd4")
	if err != nil {
		t.Fatal(err)
	}
	return env
}

// A first install selected PONG and was never rebooted into. A second one is about to
// erase PONG again: the environment must boot PING, the running slot, before it does.
func TestHoldRunningSlotUndoesAnUnbootedSelection(t *testing.T) {
	nand := newEnvNAND(t, func(env *ubootenv.Env) {
		if err := SelectSlot(env, pong, SoundTapMarkers("")); err != nil {
			t.Fatal(err)
		}
	})
	in := nand.installer(ping, pong)
	if err := in.HoldRunningSlot(nand.read); err != nil {
		t.Fatal(err)
	}
	env := nand.env(t)
	if got := SelectedSlot(env); got != "PING" {
		t.Fatalf("selected %q after the hold, want PING", got)
	}
	if got, _ := env.Get("bootcmd"); got != bootcmdPing {
		t.Errorf("bootcmd = %q", got)
	}
	// Already booting the running slot: no erase of the only copy.
	if err := in.HoldRunningSlot(nand.read); err != nil || nand.writes != 1 {
		t.Fatalf("second hold: err %v, %d writes, want 1", err, nand.writes)
	}
}

func TestSelectedSlotReadsBootcmd(t *testing.T) {
	env := factoryEnv(t)
	env.Set("bootcmd", "run rt_boot")
	if got := SelectedSlot(env); got != "" {
		t.Fatalf("SelectedSlot = %q for a bootcmd naming neither slot", got)
	}
	env.Set("bootcmd", "run nand_boot2")
	if got := SelectedSlot(env); got != "PONG" {
		t.Fatalf("SelectedSlot without the ';' = %q, want PONG", got)
	}
}

// Bose's recipes run nboot.e, which our U-Boot does not have: replacing the chain must
// bring ours, and only once.
func TestInstallMainlineRecipesReplacesBoseRecipes(t *testing.T) {
	nand := newEnvNAND(t, func(*ubootenv.Env) {})
	if boot, _ := nand.env(t).Get("nand_boot"); !strings.Contains(boot, "nboot.e") {
		t.Fatalf("factory nand_boot = %q, expected Bose's nboot.e recipe", boot)
	}
	in := nand.installer(ping, pong)
	if err := in.InstallMainlineRecipes(nand.read, "rhino"); err != nil {
		t.Fatal(err)
	}
	env := nand.env(t)
	want, _ := MainlineRecipes("rhino")
	for _, entry := range want {
		key, value, _ := strings.Cut(entry, "=")
		if got, _ := env.Get(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if args, _ := env.Get("soundtap_args"); !strings.Contains(args, "variant=rhino module_type=sm2") {
		t.Errorf("soundtap_args = %q", args)
	}
	if err := in.InstallMainlineRecipes(nand.read, "rhino"); err != nil || nand.writes != 1 {
		t.Fatalf("reinstall: err %v, %d writes, want 1", err, nand.writes)
	}
	if err := in.InstallMainlineRecipes(nand.read, "nobody"); err == nil {
		t.Fatal("an unknown variant got recipes")
	}
}

// On the ST300 our U-Boot's console UART is the soundbar processor's link, so autoboot must
// not wait for a key there; every other model keeps the factory delay.
func TestInstallMainlineRecipesDropsTheBootDelayOnlyOnGinger(t *testing.T) {
	for variant, want := range map[string]string{"ginger": "-2", "binky": "1", "rhino": "1"} {
		nand := newEnvNAND(t, func(env *ubootenv.Env) { env.Set("bootdelay", "1") })
		if err := nand.installer(ping, pong).InstallMainlineRecipes(nand.read, variant); err != nil {
			t.Fatal(variant, err)
		}
		if got, _ := nand.env(t).Get("bootdelay"); got != want {
			t.Errorf("%s: bootdelay = %q, want %q", variant, got, want)
		}
	}
}

func TestInstallMainlineRecipesRefusesAForeignEnvironment(t *testing.T) {
	nand := newEnvNAND(t, func(env *ubootenv.Env) { env.Delete("nand_boot2") })
	if err := nand.installer(ping, pong).InstallMainlineRecipes(nand.read, "binky"); err == nil || nand.writes != 0 {
		t.Fatalf("err %v, %d writes; want a refusal and no write", err, nand.writes)
	}
}

// The way back to stock puts the stock recipes back as the factory fixture holds them and
// drops what only our U-Boot reads.
func TestInstallStockRecipesRestoresTheFactoryEntries(t *testing.T) {
	factory := factoryEnv(t)
	nand := newEnvNAND(t, func(env *ubootenv.Env) {
		if _, err := applyRecipes(env, mustRecipes(t, "binky")); err != nil {
			t.Fatal(err)
		}
	})
	if err := nand.installer(ping, pong).InstallStockRecipes(nand.read); err != nil {
		t.Fatal(err)
	}
	env := nand.env(t)
	for _, key := range []string{"nand_boot", "nand_boot2"} {
		want, _ := factory.Get(key)
		if got, _ := env.Get(key); got != want {
			t.Errorf("%s = %q, want the factory %q", key, got, want)
		}
	}
	// The fixture's recipes are stockRecipes, so the two cannot drift apart.
	for _, entry := range stockRecipes {
		key, value, _ := strings.Cut(entry, "=")
		if got, _ := factory.Get(key); got != value {
			t.Errorf("fixture %s = %q, want stockRecipes' %q", key, got, value)
		}
	}
	for _, key := range []string{"slot_boot", "soundtap_args", "stock_args", "mtdids", "mtdparts"} {
		if _, ok := env.Get(key); ok {
			t.Errorf("%s survived the way back to stock", key)
		}
	}
	if got, want := len(env.Keys()), len(factory.Keys()); got != want {
		t.Errorf("%d keys, want the factory's %d", got, want)
	}
}

// On the ST300 our recipes hold autoboot with bootdelay=-2; Bose's U-Boot gets the
// factory 1 back.
func TestInstallStockRecipesRestoresBootdelay(t *testing.T) {
	nand := newEnvNAND(t, func(env *ubootenv.Env) {
		if _, err := applyRecipes(env, append(mustRecipes(t, "ginger"), "bootdelay=-2")); err != nil {
			t.Fatal(err)
		}
	})
	if err := nand.installer(ping, pong).InstallStockRecipes(nand.read); err != nil {
		t.Fatal(err)
	}
	if got, _ := nand.env(t).Get("bootdelay"); got != "1" {
		t.Errorf("bootdelay = %q, want the factory 1", got)
	}
}

// What Bose's U-Boot must do to boot a slot: build that slot's kernel command line, attach
// the slot's rootfs partition, load the board's device tree to loadaddr, read the slot's
// kernel partition to kloadaddr, fix its header and boot it with the device tree. Running
// each recipe step by step, with variables expanded when the step runs as U-Boot does,
// must give exactly that, for both slots.
func TestStockRecipesBootTheSlotStockUBootExpects(t *testing.T) {
	slots := map[string]struct{ part, kernel, mtd string }{
		"nand_boot":  {"Ping", "0x280000", "ubi.mtd=8,2048"},
		"nand_boot2": {"Pong", "0x780000", "ubi.mtd=9,2048"},
	}
	for _, entry := range stockRecipes {
		key, _, _ := strings.Cut(entry, "=")
		slot := slots[key]
		env := factoryEnv(t)
		env.Set("variant", "binky")
		env.Set("module_type", "sm2")
		if _, err := applyRecipes(env, stockRecipes); err != nil {
			t.Fatal(err)
		}
		var steps []string
		bootargs := ""
		runStockEntry(t, env, key, func(cmd string) {
			if strings.HasPrefix(cmd, "bootm ") {
				bootargs, _ = env.Get("bootargs")
			}
			steps = append(steps, cmd)
		})
		want := []string{
			"mtdparts default",
			"ubi part NAND.File-System-" + slot.part + " 2048",
			"ubifsmount ubi0",
			"ubifsload 0x82000000 /boot/binky_sm2.dtb",
			"fdt addr 0x82000000",
			"nboot.e 0x80007fc0 0 " + slot.kernel,
			"fixsig",
			"bootm 0x80007fc0 - 0x82000000",
		}
		if strings.Join(steps, "; ") != strings.Join(want, "; ") {
			t.Errorf("%s runs\n  %q\nwant\n  %q", key, steps, want)
		}
		if !strings.Contains(bootargs, slot.mtd) || !strings.Contains(bootargs, "variant=binky module_type=sm2") {
			t.Errorf("%s boots with bootargs %q, want %s and the board's variant", key, bootargs, slot.mtd)
		}
	}
}

// runStockEntry runs one environment entry the way U-Boot's shell does for these recipes:
// steps split on ';', ${var} expanded as each step runs, run and setenv carried out, every
// other command handed to do.
func runStockEntry(t *testing.T, env *ubootenv.Env, key string, do func(string)) {
	t.Helper()
	script, ok := env.Get(key)
	if !ok {
		t.Fatalf("no %s to run", key)
	}
	for _, step := range strings.Split(script, ";") {
		cmd := strings.Join(strings.Fields(os.Expand(step, func(v string) string { s, _ := env.Get(v); return s })), " ")
		name, rest, _ := strings.Cut(cmd, " ")
		switch name {
		case "":
		case "run":
			runStockEntry(t, env, rest, do)
		case "setenv":
			k, v, _ := strings.Cut(rest, " ")
			env.Set(k, v)
		default:
			do(cmd)
		}
	}
}

func mustRecipes(t *testing.T, variant string) []string {
	t.Helper()
	r, err := MainlineRecipes(variant)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// ADR 0010: on an sm2 board the selection also arms U-Boot's rollback to the other
// slot, a stock install takes it away again, and a hold never leaves the fallback
// pointing at the slot about to be erased.
func TestBootCountRollback(t *testing.T) {
	nand := newEnvNAND(t, func(*ubootenv.Env) {})
	in := nand.installer(ping, pong)
	in.Target.BootCount = true
	if err := in.SelectBootSlot(nand.read, pong, SoundTapMarkers("")); err != nil {
		t.Fatal(err)
	}
	env := nand.env(t)
	if alt, _ := env.Get("altbootcmd"); alt != bootcmdPing {
		t.Errorf("altbootcmd = %q, want the running slot", alt)
	}
	if limit, _ := env.Get("bootlimit"); limit != "4" {
		t.Errorf("bootlimit = %q, want 4", limit)
	}

	// A second install from PING must not fall back into PONG while it rewrites it.
	if err := in.HoldRunningSlot(nand.read); err != nil {
		t.Fatal(err)
	}
	env = nand.env(t)
	if got, _ := env.Get("bootcmd"); got != bootcmdPing {
		t.Errorf("bootcmd after the hold = %q, want PING's", got)
	}
	if alt, _ := env.Get("altbootcmd"); alt != bootcmdPing {
		t.Errorf("altbootcmd after the hold = %q, want PING's", alt)
	}

	// PONG fails its boots and U-Boot falls back to PING: settling there keeps the
	// fallback on PING, so warm boots past bootlimit never lead back into PONG.
	if err := in.SettleRunningSlot(nand.read, SoundTapMarkers("")); err != nil {
		t.Fatal(err)
	}
	env = nand.env(t)
	if got, _ := env.Get("bootcmd"); got != bootcmdPing {
		t.Errorf("bootcmd after the fallback = %q, want PING's", got)
	}
	if alt, _ := env.Get("altbootcmd"); alt != bootcmdPing {
		t.Errorf("altbootcmd after the fallback = %q, want PING's, not the failed slot", alt)
	}

	if err := in.SelectBootSlot(nand.read, pong, nil); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"altbootcmd", "bootlimit"} {
		if v, ok := nand.env(t).Get(key); ok {
			t.Errorf("stock install left %s=%q", key, v)
		}
	}

	// The ST20 reads neither key, so it gets neither.
	st20 := newEnvNAND(t, func(*ubootenv.Env) {})
	if err := st20.installer(ping, pong).SelectBootSlot(st20.read, pong, SoundTapMarkers("")); err != nil {
		t.Fatal(err)
	}
	if _, ok := st20.env(t).Get("altbootcmd"); ok {
		t.Error("altbootcmd written without BootCount")
	}
}

// A confirmed boot of PING whose fallback is PONG, the state after an install from
// PONG to PING: the selection already boots PING, but a hold before erasing PONG must
// still take the fallback off it.
func TestHoldMovesFallbackOffAlternate(t *testing.T) {
	nand := newEnvNAND(t, func(env *ubootenv.Env) {
		env.Set("bootcmd", bootcmdPing)
		env.Set("altbootcmd", bootcmdPong)
		env.Set("bootlimit", "4")
	})
	in := nand.installer(ping, pong)
	in.Target.BootCount = true
	if err := in.HoldRunningSlot(nand.read); err != nil {
		t.Fatal(err)
	}
	env := nand.env(t)
	if got, _ := env.Get("bootcmd"); got != bootcmdPing {
		t.Errorf("bootcmd = %q, want PING's", got)
	}
	if alt, _ := env.Get("altbootcmd"); alt != bootcmdPing {
		t.Errorf("altbootcmd = %q, want PING's, not the slot about to be erased", alt)
	}
}

func TestBootCountDecode(t *testing.T) {
	for v, want := range map[uint32]struct {
		count uint32
		ok    bool
	}{
		0x050001B0: {5, true}, // read off the WLA, 2026-10-08
		0x010001B0: {1, true}, // the ST10 after a power cycle
		0x00000000: {0, false},
	} {
		if count, ok := bootCount(v); count != want.count || ok != want.ok {
			t.Errorf("bootCount(%#x) = %d %v, want %d %v", v, count, ok, want.count, want.ok)
		}
	}
}

// The fallback is the only record that the selected slot failed; it is cleared only once
// the running slot is saved in its place.
func TestBootGuardClearsOnlyAfterTheSave(t *testing.T) {
	saveErr := errors.New("env write failed")
	const fallback, plain = 0x050001B0, 0x010001B0 // counts 5 and 1 around BootLimit
	v := uint32(fallback)
	var seen uint32
	err := confirmBootCount(&v, 0, func(fell bool) error {
		if !fell {
			t.Error("count 5 not taken for a fallback")
		}
		seen = v
		return saveErr
	})
	if !errors.Is(err, saveErr) || v != fallback {
		t.Errorf("failed save = %v, SCRATCH2 %#x, want the error and %#x kept", err, v, fallback)
	}
	if seen != fallback {
		t.Errorf("SCRATCH2 was %#x during the save, want %#x", seen, fallback)
	}
	if err := confirmBootCount(&v, 0, func(bool) error { return nil }); err != nil || v != 0 {
		t.Errorf("saved = %v, SCRATCH2 %#x, want cleared", err, v)
	}
	v = plain
	if err := confirmBootCount(&v, 0, func(fell bool) error {
		if fell {
			t.Error("a fallback on an ordinary boot")
		}
		return nil
	}); err != nil || v != 0 {
		t.Errorf("ordinary boot = %v, SCRATCH2 %#x, want cleared", err, v)
	}
}

// A cold reset clears SCRATCH2, so after power cuts the environment's count is the one
// that says U-Boot ran altbootcmd: the larger of the two decides.
func TestBootGuardTakesTheEnvironmentCount(t *testing.T) {
	for _, tc := range []struct {
		scratch2, env uint32
		fallback      bool
	}{
		{0, 5, true},           // cold resets only: SCRATCH2 holds no count
		{0x010001B0, 5, true},  // a cold reset after the fallback, then count 1 in the RTC
		{0x050001B0, 0, true},  // warm resets of a confirmed slot
		{0x020001B0, 1, false}, // still trying the selection
		{0, 4, false},          // three power cuts before the confirm: the fourth boot is still the selection
		{0, 0, false},
	} {
		v := tc.scratch2
		var fell bool
		if err := confirmBootCount(&v, tc.env, func(f bool) error { fell = f; return nil }); err != nil || v != 0 {
			t.Fatalf("%+v: err %v, SCRATCH2 %#x", tc, err, v)
		}
		if fell != tc.fallback {
			t.Errorf("%+v: fallback %v", tc, fell)
		}
	}
}

// The selection an install makes is unconfirmed: upgrade_available and a count starting
// over go in with bootcmd, and the confirm, a settle after a fallback and a hold take
// them out again. Stock gets neither.
func TestUnconfirmedSelectionCountsInTheEnvironment(t *testing.T) {
	nand := newEnvNAND(t, func(env *ubootenv.Env) { env.Set("bootcount", "6") })
	in := nand.installer(ping, pong)
	in.Target.BootCount = true
	writes := nand.writes
	if err := in.SelectBootSlot(nand.read, pong, SoundTapMarkers("")); err != nil {
		t.Fatal(err)
	}
	env := nand.env(t)
	if v, _ := env.Get("upgrade_available"); v != "1" || nand.writes != writes+1 {
		t.Fatalf("upgrade_available = %q in %d writes, want 1 in the selection's own", v, nand.writes-writes)
	}
	if v, ok := env.Get("bootcount"); ok {
		t.Errorf("an old count %q survived the selection", v)
	}
	if n, err := in.PendingBootCount(nand.read); err != nil || n != 0 {
		t.Errorf("pending count %d, %v", n, err)
	}
	// U-Boot counted two boots in the environment.
	if err := in.rewriteEnv(nand.read, func(env *ubootenv.Env) (bool, error) {
		env.Set("bootcount", "2")
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := in.PendingBootCount(nand.read); err != nil || n != 2 {
		t.Errorf("pending count %d, %v, want 2", n, err)
	}
	// The selection has not booted: a confirm from the slot that made it (a reboot, a
	// restarted soundtapd) leaves its count alone.
	writes = nand.writes
	if err := in.ConfirmPendingBoot(nand.read); err != nil || nand.writes != writes {
		t.Fatalf("a confirm from the installing slot: %v, %d writes", err, nand.writes-writes)
	}
	if n, err := in.PendingBootCount(nand.read); err != nil || n != 2 {
		t.Errorf("pending count %d, %v after a confirm from the installing slot, want 2", n, err)
	}
	booted := nand.installer(pong, ping) // the new slot, booted
	booted.Target.BootCount = true
	for name, clear := range map[string]func() error{
		"confirm": func() error { return booted.ConfirmPendingBoot(nand.read) },
		"settle":  func() error { return in.SettleRunningSlot(nand.read, SoundTapMarkers("")) },
		"hold":    func() error { return in.HoldRunningSlot(nand.read) },
	} {
		if err := in.SelectBootSlot(nand.read, pong, SoundTapMarkers("")); err != nil {
			t.Fatal(err)
		}
		if err := clear(); err != nil {
			t.Fatal(err)
		}
		env := nand.env(t)
		for _, key := range []string{"upgrade_available", "bootcount"} {
			if v, ok := env.Get(key); ok {
				t.Errorf("%s left %s=%q", name, key, v)
			}
		}
	}
	writes = nand.writes
	if err := in.ConfirmPendingBoot(nand.read); err != nil || nand.writes != writes {
		t.Errorf("a confirm with nothing pending: %v, %d writes", err, nand.writes-writes)
	}
	if err := in.SelectBootSlot(nand.read, pong, nil); err != nil {
		t.Fatal(err)
	}
	if v, ok := nand.env(t).Get("upgrade_available"); ok {
		t.Errorf("a stock selection set upgrade_available=%q", v)
	}
}

func TestPlanBootCountOnEveryModel(t *testing.T) {
	mtd := map[string]Partition{}
	for name, idx := range map[string]int{kernelPing: 6, kernelPong: 7, rootfsPing: 8, rootfsPong: 9} {
		mtd[name] = Partition{Index: idx, Size: 0x500000, EraseSize: 0x20000}
	}
	for cmdline, want := range map[string]bool{
		"ubi.mtd=9,2048 variant=rhino module_type=sm2":  true,
		"ubi.mtd=9,2048 variant=spotty module_type=scm": true,
	} {
		target, err := Plan(cmdline, mtd)
		if err != nil {
			t.Fatal(err)
		}
		if target.BootCount != want {
			t.Errorf("%q: BootCount = %v", cmdline, target.BootCount)
		}
	}
}

// A RAM recovery install writes the slot the environment is not booting and selects it.
// The fallback must then name the slot it replaced, or the next boot that fails U-Boot's
// bootlimit "falls back" into the slot that is failing.
func TestRecoveryInstallArmsRollbackToThePreviousSlot(t *testing.T) {
	table := map[string]Partition{}
	for name, idx := range map[string]int{kernelPing: 6, kernelPong: 7, rootfsPing: 8, rootfsPong: 9} {
		table[name] = Partition{Index: idx, Size: 0x500000, EraseSize: 0x20000}
	}
	// After an ordinary install from PING: PONG is selected, PING is the fallback.
	nand := newEnvNAND(t, func(env *ubootenv.Env) {
		env.Set("bootcmd", bootcmdPong)
		env.Set("altbootcmd", bootcmdPing)
		env.Set("bootlimit", "4")
	})
	target, err := RecoveryPlan(table, SelectedSlot(nand.env(t)))
	if err != nil {
		t.Fatal(err)
	}
	in := nand.installer(target.Running, target.Alternate)
	in.Target.BootCount = target.BootCount
	if err := in.SelectBootSlot(nand.read, target.Alternate, SoundTapMarkers("")); err != nil {
		t.Fatal(err)
	}
	env := nand.env(t)
	if got, _ := env.Get("bootcmd"); got != bootcmdPing {
		t.Fatalf("bootcmd = %q, want PING's", got)
	}
	if alt, _ := env.Get("altbootcmd"); alt != bootcmdPong {
		t.Errorf("altbootcmd = %q, want PONG's: the fallback is the slot that is selected", alt)
	}
}

// A speaker that is not reinstalled keeps the bootlimit the soundtapd that installed it
// wrote. The confirm brings an armed one to BootLimit in its own write, once; an
// environment without one (no rollback armed, stock) is left alone.
func TestConfirmBringsBootLimitUpToDate(t *testing.T) {
	nand := newEnvNAND(t, func(env *ubootenv.Env) {
		env.Set("bootcmd", bootcmdPong)
		env.Set("altbootcmd", bootcmdPing)
		env.Set("bootlimit", "3")
		env.Set("upgrade_available", "1")
		env.Set("bootcount", "2")
	})
	in := nand.installer(pong, ping)
	in.Target.BootCount = true
	writes := nand.writes
	if err := in.ConfirmPendingBoot(nand.read); err != nil {
		t.Fatal(err)
	}
	env := nand.env(t)
	if v, _ := env.Get("bootlimit"); v != fmt.Sprint(BootLimit) || nand.writes != writes+1 {
		t.Errorf("bootlimit = %q in %d writes, want %d in the confirm's one", v, nand.writes-writes, BootLimit)
	}
	for _, key := range []string{"upgrade_available", "bootcount"} {
		if v, ok := env.Get(key); ok {
			t.Errorf("the confirm left %s=%q", key, v)
		}
	}
	// Confirmed already, with the old limit: one write for the limit alone.
	if err := in.rewriteEnv(nand.read, func(env *ubootenv.Env) (bool, error) {
		env.Set("bootlimit", "3")
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	writes = nand.writes
	if err := in.ConfirmPendingBoot(nand.read); err != nil || nand.writes != writes+1 {
		t.Fatalf("confirm of an old limit: %v, %d writes", err, nand.writes-writes)
	}
	if v, _ := nand.env(t).Get("bootlimit"); v != fmt.Sprint(BootLimit) {
		t.Errorf("bootlimit = %q, want %d", v, BootLimit)
	}
	// Then never again, and never one that was not there.
	writes = nand.writes
	if err := in.ConfirmPendingBoot(nand.read); err != nil || nand.writes != writes {
		t.Errorf("a second confirm: %v, %d writes", err, nand.writes-writes)
	}
	if err := in.rewriteEnv(nand.read, func(env *ubootenv.Env) (bool, error) {
		env.Delete("bootlimit")
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	writes = nand.writes
	if err := in.ConfirmPendingBoot(nand.read); err != nil || nand.writes != writes {
		t.Errorf("a confirm without bootlimit: %v, %d writes", err, nand.writes-writes)
	}
	if v, ok := nand.env(t).Get("bootlimit"); ok {
		t.Errorf("the confirm added bootlimit=%q", v)
	}
}

// Our U-Boot saves board_serial out of uninitialised SRAM with the boot count, newlines
// and all, as it did on an ST10 (2026-11-23). fw_printenv prints such a value over
// several lines; the boot count still reads and the confirm still clears it, leaving the
// value as it was.
func TestConfirmReadsAnEnvironmentWithAMultiLineValue(t *testing.T) {
	garbage := "\x01\xb8(b\f\xab\xf1\n\xb2%\xc1\n:\xd4=\xf1[\x8b}oO\n\x80\xa7"
	nand := newEnvNAND(t, func(env *ubootenv.Env) {
		if err := SelectSlot(env, pong, SoundTapMarkers("")); err != nil {
			t.Fatal(err)
		}
		env.Set("board_serial", garbage)
		env.Set("upgrade_available", "1")
		env.Set("bootcount", "1")
	})
	in := nand.installer(pong, ping)
	in.Target.BootCount = true
	if n, err := in.PendingBootCount(nand.read); err != nil || n != 1 {
		t.Fatalf("pending count %d, err %v", n, err)
	}
	if err := in.ConfirmPendingBoot(nand.read); err != nil {
		t.Fatal(err)
	}
	env := nand.env(t)
	if v, ok := env.Get("upgrade_available"); ok {
		t.Errorf("the confirm left upgrade_available=%q", v)
	}
	if v, _ := env.Get("board_serial"); v != garbage {
		t.Errorf("board_serial = %q, want it untouched", v)
	}
}
