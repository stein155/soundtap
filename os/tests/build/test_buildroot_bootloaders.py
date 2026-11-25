# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Static contract for os/package/soundtap-bootloaders, the Buildroot build of the SPL and U-Boot images the STUs carry.

os/bootloader/build-wla-uboot.sh and build-wla-product-spl.sh stay the reference. These
tests hold the package to them without building anything: the same source archive, the same
patches, a saved .config that carries what each variant's flags ask for, and the profiles
that ship the results. check-mainline.sh (CI: buildroot-bootloaders.yml) is the part that
builds both and compares the .config and the bytes.
"""
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
PKG = ROOT / "os/package/soundtap-bootloaders"
UBOOT_SH = ROOT / "os/bootloader/build-wla-uboot.sh"
SPL_SH = ROOT / "os/bootloader/build-wla-product-spl.sh"
VARIANTS = ("st20-spl", "wla-uboot", "wla-spl")
# The value group of the CONFIG_X=value pattern; the "is not set" pattern has only the name.
VALUE_GROUP = 2


def config(variant: str) -> dict[str, str]:
    """Return the saved .config of variant as a map of option to value, "n" for one not set."""
    out = {}
    for line in (PKG / f"configs/{variant}.config").read_text().splitlines():
        m = re.match(r"^(CONFIG_\w+)=(.*)$", line) or re.match(r"^# (CONFIG_\w+) is not set$", line)
        if m:
            out[m.group(1)] = m.group(2) if m.lastindex == VALUE_GROUP else "n"
    return out


def unconditional_patches(script: Path) -> list[str]:
    """Return the patches script applies on every build.

    `git apply /repo/...` at the start of a line runs for every build; the ones behind
    `[ ... ] &&` belong to diagnostic or variant builds.
    """
    return re.findall(r"^\s*git apply (/repo/\S+\.patch)$", script.read_text(), re.MULTILINE)


def variant_patches(variant: str) -> list[str]:
    """Return the patches build-variant.sh applies for variant, as /repo/ paths."""
    text = (PKG / "build-variant.sh").read_text()
    lists = dict(re.findall(r'^(base|spl|uboot)="([^"]*)"$', text, re.MULTILINE))
    for name in ("spl", "uboot"):
        lists[name] = lists[name].replace("$base", lists["base"])
    cases = {}
    for labels, value in re.findall(r'^([\w| -]+)\) patches=("[^"]*"|\S+) ;;$', text, re.MULTILINE):
        for label in labels.split("|"):
            cases[label.strip()] = value.strip('"')
    value = cases[variant]
    for name, words in lists.items():
        value = value.replace(f"${name}", words)
    return ["/repo/" + p for p in value.split()]


def shell_literal(script: Path, guard: str) -> str:
    """Return the BOOTCOMMAND literal script sets behind guard."""
    return re.search(rf"{re.escape(guard)} && BOOTCOMMAND='([^']*)'", script.read_text()).group(1)


class BootloaderPackageTests(unittest.TestCase):
    """The Buildroot bootloader package builds what the reference scripts build."""

    def test_same_source_archive_as_mainline(self) -> None:
        """The package pins the same U-Boot archive and version as the reference scripts."""
        pinned = re.search(r"^SHA256=(\w+)$", UBOOT_SH.read_text(), re.MULTILINE).group(1)
        self.assertEqual(pinned, re.search(r"^SOURCE_SHA256=(\w+)$", SPL_SH.read_text(), re.MULTILINE).group(1))
        self.assertIn(f"sha256  {pinned}  u-boot-2026.07.tar.bz2", (PKG / "soundtap-bootloaders.hash").read_text())
        self.assertIn("HOST_SOUNDTAP_BOOTLOADERS_VERSION = 2026.07", (PKG / "soundtap-bootloaders.mk").read_text())

    def test_same_patches_as_mainline(self) -> None:
        """Each variant applies the same patches as its reference script, and every patch exists."""
        uboot = unconditional_patches(UBOOT_SH)
        spl = unconditional_patches(SPL_SH)
        self.assertEqual(len(uboot), 10)
        self.assertEqual(len(spl), 4)
        # One SPL source for every model; the ST20 differs only in its config.
        self.assertEqual(variant_patches("wla-spl"), spl)
        self.assertEqual(variant_patches("st20-spl"), spl)
        self.assertEqual(variant_patches("wla-uboot"), uboot)
        for p in uboot + spl:
            self.assertTrue((ROOT / p.removeprefix("/repo/")).is_file(), p)

    def test_same_timestamp_and_kcflags(self) -> None:
        """The package uses the same source date and KCFLAGS as the reference SPL script."""
        mk = (PKG / "soundtap-bootloaders.mk").read_text()
        self.assertIn("$(BR2_EXTERNAL_SOUNDTAP_PATH)/bootloader/source-date-epoch", mk)
        kcflags = 'KCFLAGS="-DCONFIG_SYS_MMC_MAX_BLK_COUNT=0 -DCONFIG_USB_MAX_CONTROLLER_COUNT=0"'
        self.assertEqual(SPL_SH.read_text().count(kcflags), 2)
        self.assertEqual((PKG / "build-variant.sh").read_text().count(kcflags), 2)

    def test_saved_configs_carry_each_variants_flags(self) -> None:
        """Each saved .config carries the options its variant's flags ask for."""
        for v in VARIANTS:
            c = config(v)
            self.assertEqual(c["CONFIG_DEFAULT_DEVICE_TREE"], '"am335x-soundtap-wla"', v)
            self.assertEqual(c["CONFIG_SPL_LEGACY_IMAGE_FORMAT"], "y", v)
            self.assertIn("CONFIG_GCC_VERSION", c, v)
        spl, uboot, wla_spl = (config(v) for v in VARIANTS)
        # build-wla-product-spl.sh's own ST20 checks: the same U-Boot offset as the others.
        for key, value in (("SPL_USB_ETHER", "y"), ("SPL_NAND_SUPPORT", "y"),
                           ("SYS_NAND_U_BOOT_OFFS", "0x80000"), ("NET_RETRY_COUNT", "1")):
            self.assertEqual(spl[f"CONFIG_{key}"], value, key)
        self.assertEqual(spl["CONFIG_SYS_NAND_U_BOOT_OFFS_REDUND"], wla_spl["CONFIG_SYS_NAND_U_BOOT_OFFS_REDUND"])
        self.assertEqual(wla_spl["CONFIG_SYS_NAND_U_BOOT_OFFS"], "0x80000")
        self.assertEqual(wla_spl.get("CONFIG_SPL_USB_ETHER", "n"), "n")  # absent: its dependencies are off
        self.assertEqual(wla_spl["CONFIG_SPL_SIZE_LIMIT"], "0x7420")
        self.assertEqual(uboot["CONFIG_LOCALVERSION_AUTO"], "n")
        self.assertEqual(uboot["CONFIG_HUSH_PARSER"], "y")
        self.assertEqual(uboot["CONFIG_CMD_FIXSIG"], "y")
        # The redundant environment (ADR 0010): the primary in mtd5, the other copy in
        # mtd4's last block, 32 KiB each, one erase block each, upgrading the 8 KiB copy.
        for key, value in (("ENV_IS_IN_NAND", "y"), ("ENV_IS_NOWHERE", "n"), ("ENV_OFFSET", "0x260000"),
                           ("ENV_SIZE", "0x8000"), ("ENV_RANGE", "0x20000"), ("ENV_REDUNDANT", "y"),
                           ("ENV_REDUNDANT_UPGRADE", "y"), ("ENV_OFFSET_REDUND", "0x240000"),
                           ("CMD_SAVEENV", "y"), ("CMD_RUN", "y")):
            self.assertEqual(uboot[f"CONFIG_{key}"], value, key)
        # The boot command is the script's literal, character for character.
        self.assertEqual(uboot["CONFIG_BOOTCOMMAND"],
                         '"{}"'.format(shell_literal(UBOOT_SH, '[ "${WLA_UBOOT_NAND_BOOT:-}" = 1 ]')))

    def test_profiles_ship_their_images(self) -> None:
        """The defconfig enables every variant and post-image.sh ships each image."""
        lines = (ROOT / "os/configs/soundtap_defconfig").read_text().splitlines()
        for name in ("", "_ST20", "_WLA"):
            self.assertIn(f"BR2_PACKAGE_HOST_SOUNDTAP_BOOTLOADERS{name}=y", lines)
        post_image = (ROOT / "os/board/image/post-image.sh").read_text()
        for f in ("st20-spl/MLO", "wla-spl/MLO", "wla-uboot/u-boot.img"):
            self.assertIn(f, post_image, f)
        self.assertIn('source "$BR2_EXTERNAL_SOUNDTAP_PATH/package/soundtap-bootloaders/Config.in"',
                      (ROOT / "os/package/Config.in").read_text())

    def test_compiler_is_the_mainline_one(self) -> None:
        """The package pins the Debian cross toolchain the reference Dockerfile installs.

        Dockerfile.wla-uboot installs bookworm's gcc-arm-linux-gnueabihf; the package pins
        the same Debian cross toolchain by version and hash.
        """
        self.assertIn("gcc-arm-linux-gnueabihf", (ROOT / "os/bootloader/Dockerfile.wla-uboot").read_text())
        mk = (PKG / "soundtap-bootloaders.mk").read_text()
        digests = (PKG / "soundtap-bootloaders.hash").read_text()
        debs = re.findall(r"/([\w.+-]+)_(2\.40-2|12\.2\.0-14cross1)_(?:\$\(HOST_SOUNDTAP_BOOTLOADERS_DEB_ARCH\)|all)\.deb", mk)
        self.assertEqual(len(debs), 4)
        for name, version in debs:
            arches = ("all",) if name.endswith("-cross") else ("arm64", "amd64")
            for arch in arches:
                self.assertRegex(digests, rf"sha256  [0-9a-f]{{64}}  {re.escape(name)}_{re.escape(version)}_{arch}\.deb")
        for v in VARIANTS:
            self.assertEqual(config(v)["CONFIG_GCC_VERSION"], "120200", v)


if __name__ == "__main__":
    unittest.main()
