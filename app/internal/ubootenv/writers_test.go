// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

package ubootenv

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stein155/soundtap/app/internal/ubootenv/envfixture"
)

// Three programs write the redundant environment from scratch: this package (soundtapd's
// repair and conversion), the installer that runs on stock firmware
// (os/installer/module/installer.c) and the offline writer the build's dry run uses
// (os/support/uboot_env.py).
// U-Boot boots whichever copy checks out with the newer flag, so for the same entries they
// must write the same bytes: the CRC window, the flag, the order U-Boot's env_export sorts
// in, the padding. Each one is run here on an installed environment, for both fresh flags.
func TestEveryWriterWritesTheSameEnvironment(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, "os/installer/module")
	// Read here, not only by the compiler, so a change to them reruns a cached test.
	for _, src := range []string{filepath.Join(module, "installer.c"), filepath.Join(root, "os/support/uboot_env.py")} {
		if _, err := os.ReadFile(src); err != nil {
			t.Fatal(err)
		}
	}
	installer := filepath.Join(t.TempDir(), "installer")
	build := exec.CommandContext(t.Context(), cc, "-O2", "-DSOUNDTAP_INSTALL_NAND", "-DSOUNDTAP_INSTALL_BOOTLOADERS",
		"-o", installer, filepath.Join(module, "installer.c"), filepath.Join(module, "swupdate.c"),
		filepath.Join(module, "mtd.c"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the installer: %v\n%s", err, out)
	}

	env, err := ParseLegacy(envfixture.Image())
	if err != nil {
		t.Fatal(err)
	}
	// What an install leaves on top of Bose's entries, unsorted on purpose: "nand_args"
	// sorts before "nand_args2" by key although the whole entries sort the other way.
	env.Set("bootcmd", "run nand_boot2;")
	env.Set("altbootcmd", "run nand_boot;")
	env.Set("bootlimit", "4")
	env.Set("upgrade_available", "1")
	env.Set("mtdids", "nand0=nand0")
	text := env.Text()

	for _, flag := range []byte{FreshRedundantFlag, FreshPrimaryFlag} {
		want, err := env.EncodeCopy(flag)
		if err != nil {
			t.Fatal(err)
		}
		c := exec.CommandContext(t.Context(), installer, "--env-image", strconv.Itoa(int(flag)))
		c.Stdin = bytes.NewReader(text)
		fromC, err := c.Output()
		if err != nil {
			t.Fatalf("installer --env-image: %v", err)
		}
		script := "import sys; sys.path.insert(0, sys.argv[1]); import uboot_env; " +
			"entries = [l.encode() for l in sys.stdin.read().splitlines() if l]; " +
			"sys.stdout.buffer.write(uboot_env.encode_copy(entries, int(sys.argv[2])))"
		p := exec.CommandContext(t.Context(), python, "-I", "-c", script, filepath.Join(root, "os/support"),
			strconv.Itoa(int(flag)))
		p.Stdin = strings.NewReader(string(text))
		fromPython, err := p.Output()
		if err != nil {
			t.Fatalf("uboot_env.encode_copy: %v", err)
		}
		for name, got := range map[string][]byte{"installer.c": fromC, "uboot_env.py": fromPython} {
			if !bytes.Equal(got, want) {
				t.Errorf("flag %d: %s writes %d bytes that differ from this package's %d", flag, name, len(got), len(want))
			}
		}
		if back, f, err := ParseCopy(want); err != nil || f != flag || !back.Equal(env) {
			t.Errorf("flag %d: the copy does not read back: %v", flag, err)
		}
	}
}
