# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""U-Boot's boot count of an unconfirmed slot survives a cold reset (ADR 0010).

os/bootloader/uboot/uboot-2026.07-bootcount-env.patch keeps the count in RTC SCRATCH2, which
a cold reset or a power cut clears, and, while an install's selection is unconfirmed
(upgrade_available=1), in the environment as well. Its bootcount_store and bootcount_load
are compiled here on the host with the RTC and the environment stubbed, and run through
U-Boot's own sequence per boot (bootcount_inc, then bootcount_error picks altbootcmd).
"""
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
PATCH = (ROOT / "os/bootloader/uboot/uboot-2026.07-bootcount-env.patch").read_text()
BOOTSELECT = (ROOT / "app/internal/flash/bootselect.go").read_text()


def patch_function(signature: str) -> str:
    """Return the function with this signature as the patch adds it, without the diff's '+' column."""
    body = PATCH.split("+" + signature)[1].split("\n+}\n")[0]
    return "\n".join(line[1:] for line in ("+" + signature + body + "\n+}").split("\n"))


# One boot after another, as the board does them. RESETS is a string of 'c' (cold reset or
# power cut: SCRATCH2 cleared) and 'w' (warm reset: SCRATCH2 kept), one per boot; ENV is the
# environment an install left. Nothing confirms: that is the case the count exists for.
PROBE = r"""
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
typedef unsigned long ulong;

/* SCRATCH2: a count and whether it holds the magic, as bootcount_davinci.c reads it. */
static ulong rtc;
static int rtc_valid;
static void bootcount_rtc_store(ulong a) { rtc = a; rtc_valid = 1; }
static ulong bootcount_rtc_load(void) { return rtc_valid ? rtc : 0; }

/* The environment in RAM and what NAND holds, as env_save leaves it. */
#define MAX 8
static char keys[MAX][32], vals[MAX][32], saved_keys[MAX][32], saved_vals[MAX][32];
static int saves;
static ulong env_get_ulong(const char *k, int base, ulong def)
{
	(void)base;
	for (int i = 0; i < MAX; i++)
		if (keys[i][0] && !strcmp(keys[i], k))
			return strtoul(vals[i], NULL, 10);
	return def;
}
static void env_set_ulong(const char *k, ulong v)
{
	int free_slot = -1;

	for (int i = 0; i < MAX; i++) {
		if (keys[i][0] && !strcmp(keys[i], k)) {
			snprintf(vals[i], sizeof vals[i], "%lu", v);
			return;
		}
		if (!keys[i][0] && free_slot < 0)
			free_slot = i;
	}
	strcpy(keys[free_slot], k);
	snprintf(vals[free_slot], sizeof vals[free_slot], "%lu", v);
}
static int env_save(void)
{
	memcpy(saved_keys, keys, sizeof keys);
	memcpy(saved_vals, vals, sizeof vals);
	saves++;
	return 0;
}

FUNCTIONS

int main(void)
{
	const char *resets = RESETS;
	ENV
	env_save();
	saves = 0;
	for (const char *r = resets; *r; r++) {
		/* Power on: the environment from NAND; a cold reset clears SCRATCH2. */
		memcpy(keys, saved_keys, sizeof keys);
		memcpy(vals, saved_vals, sizeof vals);
		if (*r == 'c')
			rtc_valid = 0;
		/* bootcount_inc and bootcount_error, include/bootcount.h */
		ulong count = bootcount_load();
		bootcount_store(++count);
		env_set_ulong("bootcount", count);
		ulong limit = env_get_ulong("bootlimit", 10, 0);
		printf("%s%lu", r == resets ? "" : " ", limit && bootcount_load() > limit ? 0UL : count);
	}
	printf("|%d\n", saves);
	return 0;
}
"""


@unittest.skipUnless(shutil.which("cc"), "needs a C compiler")
class BootCountAcrossResets(unittest.TestCase):
    """The patched bootcount_store and bootcount_load over a run of boots."""

    LIMIT = int(re.search(r"const BootLimit = (\d+)", BOOTSELECT).group(1))

    def boots(self, resets: str, env: dict[str, int]) -> tuple[list[int], int]:
        """Return the bootcmd each boot runs (its count, or 0 for altbootcmd) and the NAND saves."""
        functions = "\n".join(patch_function(s) for s in ("void bootcount_store(ulong a)", "ulong bootcount_load(void)"))
        setup = " ".join(f'env_set_ulong("{k}", {v});' for k, v in env.items())
        src = (PROBE.replace("FUNCTIONS", functions).replace("RESETS", f'"{resets}"')
               .replace("ENV", setup))
        with tempfile.TemporaryDirectory() as d:
            Path(d, "probe.c").write_text(src)
            subprocess.run(["cc", "-std=gnu11", "-Wall", "-Werror", "-o", f"{d}/probe", f"{d}/probe.c"], check=True)
            out = subprocess.run([f"{d}/probe"], check=True, capture_output=True, text=True).stdout.strip()
        counts, saves = out.split("|")
        return [int(c) for c in counts.split()], int(saves)

    def tries(self, n: int) -> list[int]:
        """Return bootcmd for the first bootlimit boots, then altbootcmd (0), over n boots."""
        return [c if c <= self.LIMIT else 0 for c in range(1, n + 1)]

    def test_an_unconfirmed_slot_falls_back_across_power_cuts(self) -> None:
        """An unconfirmed slot falls back after bootlimit boots, even when every boot is a power cut.

        The install sets altbootcmd, bootlimit and upgrade_available in one write.
        """
        boots, saves = self.boots("ccccccc", {"bootlimit": self.LIMIT, "upgrade_available": 1})
        self.assertEqual(boots, self.tries(7))
        # One save per boot up to bootlimit + 1, then none: altbootcmd runs either way.
        self.assertEqual(saves, self.LIMIT + 1)

    def test_three_power_cuts_leave_a_boot_to_confirm(self) -> None:
        """Three power cuts during trial boots leave a fourth boot on the new slot to confirm.

        Owner, 2026-11-20 (ADR 0010): three power cuts during trial boots, each before
        that boot's confirm, never roll back a good update; the fourth boot is still the
        new slot. Four unconfirmed boots do: the fifth runs altbootcmd.
        """
        boots, _ = self.boots("ccccc", {"bootlimit": self.LIMIT, "upgrade_available": 1})
        self.assertEqual(boots, [1, 2, 3, 4, 0])

    def test_warm_and_cold_resets_add_up(self) -> None:
        """Warm and cold resets count together."""
        boots, _ = self.boots("cwcwcwc", {"bootlimit": self.LIMIT, "upgrade_available": 1})
        self.assertEqual(boots, self.tries(7))

    def test_a_confirmed_slot_counts_in_the_rtc_alone(self) -> None:
        """A confirmed slot counts in the RTC alone.

        No upgrade_available: no NAND write on any boot, and a warm reset loop (the
        watchdog) still falls back.
        """
        boots, saves = self.boots("cwwwwww", {"bootlimit": self.LIMIT})
        self.assertEqual((boots, saves), (self.tries(7), 0))
        # A cold reset starts it over, as before: that is what upgrade_available is for.
        boots, saves = self.boots("cccc", {"bootlimit": self.LIMIT})
        self.assertEqual((boots, saves), ([1, 1, 1, 1], 0))

    def test_upgrade_available_without_bootlimit_writes_nothing(self) -> None:
        """upgrade_available without a bootlimit counts nothing and writes nothing."""
        boots, saves = self.boots("ccc", {"upgrade_available": 1})
        self.assertEqual((boots, saves), ([1, 1, 1], 0))


if __name__ == "__main__":
    unittest.main()
