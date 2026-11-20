# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[3]


class WlaWiLinkTests(unittest.TestCase):
    def test_wla_delays_sdio_wifi_until_rootfs_has_firmware(self):
        dts = (ROOT / "os/board/common/dts/ti/omap/am335x-soundtap-wla.dts").read_text()
        wla_defconfig = (ROOT / "os/configs/soundtap_defconfig").read_text()

        init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
        # Built in: the image carries no modules and init loads none.
        self.assertNotIn("insmod", init)
        for text in (
            "vmmc-supply = <&wlan_en_reg>;", "bus-width = <4>;", "non-removable;",
            "sdhci-caps-mask = <0 0x00580000>;", 'compatible = "ti,wl1835";',
            "reg = <2>;", "interrupts = <31 IRQ_TYPE_EDGE_FALLING>;",
        ):
            self.assertIn(text, dts)
        wla_start = init.rindex('if [ "$MAINLINE" = 1 ] && [ "$MODEL" = wla ]; then')
        wla_end = init.index('elif [ "$MAINLINE" = 1 ]; then', wla_start)
        self.assertNotIn("file:filename=/dev/null", init[wla_start:wla_end])
        self.assertIn("BR2_PACKAGE_SOUNDTAP_WL18XX_CONF=y", wla_defconfig)
        self.assertIn("BR2_PACKAGE_LINUX_FIRMWARE_TI_WL18XX=y", wla_defconfig)

    def test_wla_writes_only_its_slots_persistent_storage_and_verified_boot_chain(self):
        dtsi = (ROOT / "os/board/common/dts/ti/omap/am335x-soundtap-nand.dtsi").read_text()
        for offset in ("280000", "780000", "c80000", "6c80000"):
            self.assertRegex(
                dtsi, rf"partition@{offset} \{{.*SOUNDTAP_NAND_SLOT_RW; \}};"
            )
        # cc80000 is deliberately absent from this list. Persistent storage was read-only
        # here until 2026-09-03, which left /mnt/nv as a tmpfs and the speaker unable to
        # remember wifi credentials, presets, its own name, the Spotify login, the Dropbear
        # host key or the clock across a power cycle. Opened by project decision once the
        # partition had a verified backup off the board. Native install also opens bootloader
        # partitions after proof. Environment, staging and manufacturing data stay shut.
        for offset in ("0", "20000", "40000", "60000", "80000"):
            self.assertRegex(
                dtsi, rf"partition@{offset} \{{.*SOUNDTAP_NAND_BOOT_RW; \}};"
            )
        # The environment is writable through its own macro, not the slot one: one
        # entry in it decides which slot boots, so firmware that installs into the
        # other slot has to rewrite it. Staging and manufacturing data stay shut.
        self.assertRegex(dtsi, r"partition@260000 \{.*SOUNDTAP_NAND_ENV_RW; \};")
        for offset in ("f280000", "fe00000"):
            self.assertRegex(
                dtsi, rf"partition@{offset} \{{.*SOUNDTAP_NAND_RO; \}};"
            )
        wla = (ROOT / "os/board/common/dts/ti/omap/am335x-soundtap-wla.dts").read_text()
        self.assertIn("#define SOUNDTAP_NAND_BOOT_RW soundtap,writable", wla)
        self.assertIn("#define SOUNDTAP_NAND_PERSISTENT_RW soundtap,writable", wla)

if __name__ == "__main__":
    unittest.main()
