#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Which U-Boot copy our SPL boots (os/bootloader/uboot/uboot-2026.07-spl-second-uboot.patch).

NAND.U-Boot holds two copies, the first at its start and the spare 1 MiB in (ADR 0009). The SPL
boots the first unless it fails to load or its CRC, and, since a first install from stock writes
our SPL before the first copy, unless it is not ours while the spare is: then the spare. Its
chooser is compiled here on the host with the NAND loads stubbed.
"""
import itertools
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
PATCH = (ROOT / "os/bootloader/uboot/uboot-2026.07-spl-second-uboot.patch").read_text()


def patch_function(signature):
    """The function as the patched file has it: from its signature to its closing brace, which
    the diff may carry as an added line or as context."""
    lines = PATCH.split("+" + signature, 1)[1].split("\n")
    end = next(i for i, line in enumerate(lines) if line in ("+}", " }"))
    return signature + "\n".join(line[1:] for line in lines[:end + 1])


# FIRST and SPARE are what each copy holds: "ours", "bose" (another U-Boot that checks out),
# "torn" (a good header in front of data that fails its CRC) or "none".
PROBE = r"""
#include <stdbool.h>
#include <stdio.h>
#include <string.h>
#define __maybe_unused __attribute__((unused))
#define IH_NMLEN 32
#define EIO 5
struct spl_image_info { const char *name; int loaded; };
struct spl_boot_device;
NAME_DEFINE
static const char *copies[2] = { FIRST, SPARE };
static int spl_nand_load_element(struct spl_image_info *spl_image, struct spl_boot_device *bootdev, int offset)
{
	const char *c = copies[offset == 0x180000];

	(void)bootdev;
	spl_image->loaded = offset;
	spl_image->name = !strcmp(c, "ours") ? SOUNDTAP_UBOOT_NAME : "Bose U-Boot";
	return !strcmp(c, "ours") || !strcmp(c, "bose") ? 0 : -EIO;
}
FUNCTIONS
int main(void)
{
	struct spl_image_info image = {0};
	int err = spl_nand_load_uboot(&image, NULL, 0x80000, 0x180000);

	printf("%s\n", err ? "none" : image.loaded == 0x80000 ? "first" : "spare");
	return 0;
}
"""


class SplSecondUBoot(unittest.TestCase):
    def test_our_name_is_the_one_mkimage_gives(self):
        """The SPL compares the image name mkimage writes; the two spellings live in a patch
        and two build scripts that only a cross build runs, so they are compared as text."""
        name = re.search(r'^\+#define SOUNDTAP_UBOOT_NAME\s+"([^"]+)"$', PATCH, re.M).group(1)
        for script in ("os/bootloader/build-wla-uboot.sh", "os/package/soundtap-bootloaders/build-variant.sh"):
            self.assertIn(f'-n "{name}"', (ROOT / script).read_text(), script)

    @unittest.skipUnless(shutil.which("cc"), "needs a C compiler")
    def test_which_copy_boots(self):
        define = re.search(r"^\+(#define SOUNDTAP_UBOOT_NAME.*)$", PATCH, re.M).group(1)
        functions = "\n".join(patch_function(s) for s in (
            "static bool __maybe_unused spl_nand_ours(const struct spl_image_info *spl_image)",
            "static int __maybe_unused spl_nand_load_uboot(struct spl_image_info *spl_image,"))
        with tempfile.TemporaryDirectory() as d:
            got = {}
            for first, spare in itertools.product(("ours", "bose", "torn", "none"), repeat=2):
                src = (PROBE.replace("NAME_DEFINE", define).replace("FUNCTIONS", functions)
                       .replace("FIRST", f'"{first}"').replace("SPARE", f'"{spare}"'))
                Path(d, "probe.c").write_text(src)
                subprocess.run(["cc", "-std=gnu11", "-Wall", "-Werror", "-o", f"{d}/probe", f"{d}/probe.c"], check=True)
                got[(first, spare)] = subprocess.run([f"{d}/probe"], check=True, capture_output=True,
                                                     text=True).stdout.strip()
        want = {
            # Ours in the first copy: that one, whatever the spare holds.
            ("ours", "ours"): "first", ("ours", "bose"): "first", ("ours", "torn"): "first", ("ours", "none"): "first",
            # Bose's in the first copy, the moment a first install from stock has written our SPL
            # and not yet the first copy: our spare. Without one of ours, Bose's after all.
            ("bose", "ours"): "spare", ("bose", "bose"): "first", ("bose", "torn"): "first", ("bose", "none"): "first",
            # A first copy cut mid-write: the spare, as before.
            ("torn", "ours"): "spare", ("torn", "bose"): "spare", ("torn", "torn"): "none", ("torn", "none"): "none",
            ("none", "ours"): "spare", ("none", "bose"): "spare", ("none", "torn"): "none", ("none", "none"): "none",
        }
        self.assertEqual(got, want)


if __name__ == "__main__":
    unittest.main()
