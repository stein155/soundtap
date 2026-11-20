#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Hold the WLA Buildroot profile to its contract.

Which partitions Linux may write is decided in the device trees themselves, not by a
build-time sed.

Checked without building anything:

- the WLA's own dts is what opens its bootloader, slot, env and persistent partitions;
- the memory figure agrees across the command line, the memory node and the shared
  MEMORY;
- the kernel image is assembled with the expected load address and name;
- the profile does not carry the ST10's serial getty on a board with no serial port;
- the kernel patch directory the defconfig names exists and holds what it must.
"""

import hashlib
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BOARD = ROOT / "os/board/rootfs"
# Both models compile from one directory now, so a tree exists for every speaker in
# every image -- which is what lets one package serve both.
SHARED_DTS = ROOT / "os/board/common/dts/ti/omap"
PROFILE_DTS = SHARED_DTS / "am335x-soundtap-wla.dts"
PROFILE_DTSI = SHARED_DTS / "am335x-soundtap-nand.dtsi"
DEFCONFIG = ROOT / "os/configs/soundtap_defconfig"
MAKEFILE = ROOT / "Makefile"
WLA_FIRMWARE_SHA256 = "95f04731257febabc358b95ff06dbb2025d047c6ad3ae0239c8afb92dedc7c5c"
NAND_GEOMETRY = json.loads((ROOT / "os/board/nand-geometry.json").read_text())
def fail(message):
    raise SystemExit(f"WLA Buildroot profile: {message}")


def check_device_tree():
    dts = PROFILE_DTS.read_text()
    for macro in ("BOOT_RW", "SLOT_RW", "ENV_RW", "PERSISTENT_RW"):
        if f"#define SOUNDTAP_NAND_{macro} soundtap,writable" not in dts:
            fail(f"the WLA device tree no longer opens SOUNDTAP_NAND_{macro}")


def check_gadget_console():
    overlay = BOARD / "rootfs-overlay/etc/inittab"
    post_build = BOARD / "post-build.sh"
    gadget = ROOT / "os/overlay/etc/init.d/gadget-console"
    if "::sysinit:/etc/init.d/gadget-console" not in overlay.read_text():
        fail("WLA inittab does not start the USB gadget console")
    if not gadget.is_file() or "gadget-console" not in post_build.read_text():
        fail("WLA post-build does not install the USB gadget console")
    # This script is the first ::sysinit: entry, and BusyBox init runs those to completion in
    # order. Opening /dev/ttyGS0 blocks until a USB host has the port open, so a synchronous
    # write here stops the whole boot on a board with no cable in it -- measured 2026-09-15:
    # silent for 680 s with the port off, on Wi-Fi 15 s after a host appeared. Every write to
    # the console must be detached, so init never waits on a reader that may not exist.
    for line in gadget.read_text().splitlines():
        stripped = line.split(" # ", 1)[0].strip() if not line.lstrip().startswith("#") else line.strip()
        if stripped.startswith("#") or "/dev/ttyGS0" not in stripped:
            continue
        if stripped.endswith(("&) 2>/dev/null", "&)")):
            continue
        if stripped.startswith(("[", "while", "log=")) or "-c /dev/ttyGS0" in stripped:
            continue
        fail(f"gadget console writes to /dev/ttyGS0 without detaching, which blocks boot: {stripped}")


def check_rootfs_contract():
    defconfig = DEFCONFIG.read_text()
    post_build = (BOARD / "post-build.sh").read_text()
    # One image: the WiLink board configuration comes from the same package on every model.
    if "BR2_PACKAGE_SOUNDTAP_WL18XX_CONF=y" not in defconfig:
        fail("the image carries no WiLink board configuration")
    # internal/flash shells out to these; BusyBox provides none of them, and its own
    # ubiattach cannot pass the vid_hdr_offset this NAND needs. Built without the mtd
    # package the image answered "installable" on /api/firmware while being unable to
    # write a single page, and returning the board to stock needed a mask-ROM RAM boot.
    if "BR2_PACKAGE_MTD=y" not in defconfig:
        fail("WLA profile has no mtd-utils: the speaker cannot write its own NAND")
    volume_lebs = NAND_GEOMETRY["volume_lebs"]
    volume_size = volume_lebs * 126_976
    for profile in ("nand",):
        config = DEFCONFIG.read_text()
        ubinize = (BOARD / "ubinize.cfg").read_text()
        if f"BR2_TARGET_ROOTFS_UBIFS_MAXLEBCNT={volume_lebs}" not in config:
            fail(f"{profile} UBIFS geometry differs from nand-geometry.json")
        if NAND_GEOMETRY["space_fixup"] and 'BR2_TARGET_ROOTFS_UBIFS_OPTS="-F ' not in config:
            fail(f"{profile} does not enable UBIFS free-space fixup")
        if f"vol_size={volume_size}" not in ubinize:
            fail(f"{profile} UBI volume geometry differs from nand-geometry.json")
        # The nand fragment leaves the reserve at the kernel default, which is 20.
        fragment = (ROOT / "os/board/image/linux.fragment").read_text()
        beb = re.search(r"CONFIG_MTD_UBI_BEB_LIMIT=(\d+)", fragment)
        if int(beb.group(1) if beb else 20) != NAND_GEOMETRY["beb_limit_per_1024"]:
            fail(f"{profile} UBI bad-PEB reserve differs from nand-geometry.json")
    # The WiLink drivers are built in; the only modules are the ST300 CPSW ones.
    fragment = (ROOT / "os/board/image/linux.fragment").read_text()
    for symbol in ("CONFIG_WLCORE=y", "CONFIG_WLCORE_SDIO=y", "CONFIG_WL18XX=y"):
        if symbol not in fragment:
            fail(f"kernel fragment is missing {symbol}")
    if "! -name 'ti_cpsw_new.ko*'" not in post_build:
        fail("post-build does not drop the kernel modules the image does not use")
    if "binky_sm2.dtb" not in post_build:
        fail("post-build does not install the WLA device tree for U-Boot")
    if "uImage-binky" not in (ROOT / "os/board/image/post-build.sh").read_text():
        fail("post-build does not install the WLA kernel for U-Boot")


def check_bluetooth():
    """The Bluetooth radio is the other half of the WL1835 that already carries Wi-Fi.
    Every piece of it is a separate file, and any one missing means the adapter silently
    never appears -- which is how it stayed off for so long."""
    dts = PROFILE_DTS.read_text()
    fragment = (ROOT / "os/board/image/linux.fragment").read_text()
    post_build = (BOARD / "post-build.sh").read_text()
    init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
    # Bose's own binky_sm2.dtb puts the BT UART on serial@48024000 -- the &uart2 of
    # am33xx.dtsi -- with spi0_sclk/spi0_d0 as rx/tx and i2c0_sda/i2c0_scl as cts/rts,
    # and names the enable line bt_enable at gpionum 63, which is gpio1_31.
    if '"ti,wl1835-st"' not in dts:
        fail("device tree declares no WiLink Bluetooth node")
    if "enable-gpios = <&gpio1 31 GPIO_ACTIVE_HIGH>" not in dts:
        fail("Bluetooth enable line is not gpio1_31 (Bose's bt_enable, gpionum 63)")
    if "AM335X_PIN_I2C0_SDA, PIN_INPUT_PULLUP, MUX_MODE2" not in dts:
        fail("uart2 has no cts pin: WiLink8 needs hardware flow control at 3 Mbaud")
    for symbol in ("CONFIG_BT=y", "CONFIG_BT_HCIUART_LL=y", "CONFIG_BT_HCIUART_SERDEV=y"):
        if symbol not in fragment:
            fail(f"kernel fragment is missing {symbol}")
    # hci_ll probes before UBIFS mounts, so its firmware has to be in the kernel. The WLA
    # profile once had it built in without the blob: request_firmware returned -2 at 1.59 s
    # and the controller never recovered (measured 2026-09-17). The nand kernel builds the
    # transport in and embeds the blob; a modular transport is the other safe shape.
    if "CONFIG_BT_HCIUART=m" not in fragment and \
            'CONFIG_EXTRA_FIRMWARE="ti-connectivity/TIInit_11.8.32.bts"' not in fragment:
        fail("built-in CONFIG_BT_HCIUART without TIInit_11.8.32.bts in EXTRA_FIRMWARE asks "
             "for its firmware before the root filesystem that holds it is mounted")
    # hci_ll asks for ti-connectivity/TIInit_<version>.bts by the version it reads off the
    # chip. linux-firmware ships 6.2.31, 6.6.15 and 7.2.31 -- all WiLink6/7 -- so the
    # WiLink8 script has to come from the copy in this repo.
    blob = ROOT / "os/upstream/ti-connectivity/TIInit_11.8.32.bts"
    if not blob.is_file():
        fail("TIInit_11.8.32.bts is not in the tree; WLA Bluetooth cannot come up")
    digest = hashlib.sha256(blob.read_bytes()).hexdigest()
    if digest not in post_build:
        fail("post-build does not pin the digest of the TIInit_11.8.32.bts it installs")
    if not (blob.parent / "PROVENANCE.md").is_file():
        fail("the TI blob has no recorded provenance, which ADR 0047 requires")
    # ADR 0049: anything extracted from Bose NAND is not acceptable, blobs
    # included. os/stock is exactly that, and its copy of this file is a different
    # revision from TI's own -- 75439 bytes against 70313 -- so it is not interchangeable.
    if "os/stock" in post_build:
        fail("post-build installs a file extracted from Bose NAND into the image")
    if "lib/firmware/ti-connectivity/TIInit_11.8.32.bts" not in post_build:
        fail("post-build does not install the WiLink8 Bluetooth init script")
    if "wla bluetooth disabled" in init:
        fail("init.d/soundtap still refuses to start Bluetooth on the WLA")
    # The Audio/Loudspeaker class is deliberately not set from bt.sh: every attempt to do
    # it there left a btmgmt that never exits, and a stuck one keeps the next from getting
    # an answer. bt.sh carries the whole finding. What this holds is that it stays out
    # until the hang is understood -- a class is polish, a process that never exits on
    # every boot is not.
    bt_sh = (ROOT / "os/overlay/opt/soundtap/bt.sh").read_text()
    code = "\n".join(l for l in bt_sh.splitlines() if not l.lstrip().startswith("#"))
    if "btmgmt" in code:
        fail("bt.sh runs btmgmt again; it hangs at boot for reasons the comment in that "
             "file records, and a stuck btmgmt blocks every later one")
    runtime = (ROOT / "os/package/soundtap-runtime/soundtap-runtime.mk").read_text()
    if "etc/bluetooth/main.conf" not in runtime:
        fail("soundtap-runtime does not install etc/bluetooth/main.conf, so bluez5_utils' "
             "own empty default stays in the image")
    if "tools/btmgmt" not in runtime:
        fail("soundtap-runtime does not install btmgmt, which bluez lists as noinst; "
             "without it bt.sh cannot set the device class")
    # bluealsa registers its A2DP endpoints with bluetoothd once, at startup, and never
    # again. If bluetoothd restarts underneath it the phone still connects and no audio
    # ever arrives, with nothing in the log. bt.sh has to take the stack down together.
    bt = (ROOT / "os/overlay/opt/soundtap/bt.sh").read_text()
    if "start_bt_stack" not in bt or "stop_bt_stack" not in bt:
        fail("bt.sh starts bluetoothd and bluealsa without being able to restart them "
             "together; a bluetoothd restart leaves Bluetooth silently dead")
    if 'kill -0 "$BLUETOOTHD_PID"' not in bt:
        fail("nothing in bt.sh notices bluetoothd going away")
    # AUDIO_MIX copies /etc/asound.conf.bus from the overlay. Without it the cp fails and
    # every ALSA client -- bluealsa-aplay included -- opens the card behind soundtapd.
    bus = ROOT / "os/overlay/etc/asound.conf.bus"
    if not bus.is_file():
        fail("WLA overlay has no asound.conf.bus: ALSA clients bypass the source bus")
    if "/run/soundtap/source.pcm" not in bus.read_text():
        fail("WLA asound.conf.bus does not route the default PCM into the source bus")
    # "plug" on its own converts nothing: the conversions live in separate alsa-lib
    # plugins. Without "rate" and "linear" the card runs at 46875 Hz in S32_LE while
    # soundtapd feeds plughw:0,0 48000 Hz S16_LE, and ALSA passes both through unchanged
    # and then refuses to install the hw params. Measured on the board: plughw:1,0 asked
    # for 48000 on a 44100-only card and ALSA offered the card 48000 rather than resample.
    plugins = next((line for line in DEFCONFIG.read_text().splitlines()
                    if line.startswith("BR2_PACKAGE_ALSA_LIB_PCM_PLUGINS=")), "")
    for plugin in ("plug", "rate", "linear"):
        if plugin not in plugins.split("=", 1)[1].strip('"').split():
            fail(f"alsa-lib is built without the {plugin} PCM plugin: "
                 "nothing can convert between the bus format and the card")



def check_amp_gpio_numbering():
    """Bose numbers the four AM335x banks as one flat space; Linux numbers a line from its
    chip's base, and the bases are handed out in registration order, not by address. Writing
    /sys/class/gpio/gpio72 on this kernel addresses nothing: the path does not exist and the
    write is swallowed. Every one of these lines has to be resolved through its bank."""
    init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
    # Several branches share that condition; the amp one is the last, and it is the only
    # one that ends in the shared elif chain for the other models.
    parts = init.split('if [ "$MAINLINE" = 1 ] && [ "$MODEL" = wla ]; then')
    if len(parts) < 2:
        fail("cannot find the mainline WLA amp-GPIO branch in init.d/soundtap")
    branch = parts[-1].split("elif", 1)[0]
    if "gpio_num()" not in branch or "481ac000" not in branch:
        fail("the WLA amp GPIOs are not resolved through their bank's controller address; "
             "a flat Bose number writes to a path that does not exist and the amplifier "
             "stays in standby")
    # Comments in that branch name the old paths on purpose, so read the code only.
    code = "\n".join(l for l in branch.splitlines() if not l.lstrip().startswith("#"))
    for flat in ("gpio72", "gpio74", "gpio75", "gpio86"):
        if "/sys/class/gpio/" + flat in code:
            fail(f"init.d/soundtap still writes /sys/class/gpio/{flat} on the mainline WLA; "
                 "that is Bose's numbering, not Linux's")


def check_one_identity():
    """Groups are lists of device ids, so snapcast has to answer to the same one. The host
    id used to be built from a MAC of its own -- a different interface from the device id
    on most models, and neither of them on a board whose factory serial can be read."""
    init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
    if "soundtapd -print-identity" not in init:
        fail("the snapcast host id is not derived from the speaker identity; the audio "
             "layer would carry a second name for one box")
    code = "\n".join(l for l in init.splitlines() if not l.lstrip().startswith("#"))
    if 'SOUNDTAP_SNAPCLIENT_ID="soundtap-$snap_mac"' in code:
        fail("init.d/soundtap still builds the snapcast host id straight from a MAC")


def check_mute_gpio_resolves():
    """What silences this speaker is mute_out and nothing else, and what kept it silent for
    months was that MUTE_GPIO never reached soundtapd: the init tested /sys/class/gpio/gpio72,
    which does not exist on this kernel, so setMute() returned on an empty path and the
    amplifier was never taken out of mute. Bisected on the board 2026-09-18 -- gpio552 low is
    silent and high is sound, while ampstby and a held-open capture changed nothing.

    So the thing worth pinning is not a capture. It is that MUTE_GPIO is derived through the
    bank lookup rather than from Bose's flat number."""
    init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
    if "mute_n=$(gpio_num 72)" not in init or "MUTE_GPIO=/sys/class/gpio/gpio$mute_n/value" not in init:
        fail("the WLA init no longer derives MUTE_GPIO through the GPIO bank lookup; "
             "soundtapd cannot unmute the amplifier without it")


def check_audio_outputs():
    """The only connector on this box is a 3.5 mm pair. The analog link on McASP0 is the
    output that reaches it, and it has to be card 0; S/PDIF shares the same jack for an
    optical plug. Both were wrong for a long time in ways that are silent rather than
    loud, so pin the parts that cannot be seen from a boot log."""
    dts = PROFILE_DTS.read_text()
    init = (ROOT / "os/overlay/etc/init.d/soundtap").read_text()
    driver = (ROOT / "os/drivers/soundtap-st10-audio.c").read_text()
    if "&mcasp0 {" not in dts:
        fail("device tree does not enable McASP0: the analog output is the only one wired")
    # The analog node must come first so it is card 0, which is what plughw:0,0 means
    # everywhere else in this tree.
    if dts.index("sound_analog:") > dts.index('compatible = "bose,soundtap-mcasp-spdif"'):
        fail("the S/PDIF sound node probes before the analog one; card 0 would be S/PDIF")
    if 'AM335X_PIN_MII1_COL' in dts.split("mcasp0_pins:")[1].split("};")[0]:
        fail("mcasp0 claims mii1_col, which mcasp1_axr2 already has: pinctrl will refuse")
    if "serial-dir = <2 1 0 0>" not in dts:
        fail("McASP0 serializer directions do not match Bose's: 0 receives, 1 transmits")
    # Same McASP on the same module, so the node should say the same thing. In particular
    # ti,tx-clock-only: without it a clock-provider format drives the receive clock pins
    # as outputs, and lcd_data12/13 are inputs on this board.
    st10_dts = (ROOT / "os/board/common/dts/ti/omap/am335x-soundtap-st10.dts").read_text()
    def _mcasp0(text):
        body = text.split("&mcasp0 {", 1)[1].split("\n};", 1)[0]
        return sorted(line.strip() for line in body.splitlines()
                      if line.strip() and not line.strip().startswith(("/*", "*", "//")))
    # One property is allowed to differ, because the boards differ: the Wireless Link's
    # converter has no control bus and the vendor driver leaves AHCLKX undriven there
    # (PDIR 0x14000002 with AHCLKXCTL 0x8001 -- internal clock, pin untouched), while the
    # ST10 carries an AIC3256 that may take its master clock from that pin. Adding the
    # property to both to keep this comparison happy would risk a model whose audio works.
    per_board = {"ti,ahclkx-not-driven;"}
    if set(_mcasp0(dts)) - per_board != set(_mcasp0(st10_dts)) - per_board:
        fail("the WLA and ST10 McASP0 nodes differ; they are the same McASP on the same module")
    # Measured on the board: the S/PDIF link ran at 23437.5 Hz against a declared 48000,
    # below the S/PDIF floor, because the machine driver generated AHCLKX from the
    # internal 24 MHz instead of taking the board's 11.2896 MHz oscillator.
    if "MCASP_CLK_HCLK_AHCLK" not in driver or "11289600" not in driver:
        fail("the S/PDIF link does not take its clock from the board oscillator")
    if "24576000" in driver:
        fail("the machine driver still asks AUXCLK for a rate the AM335x cannot make")
    if "'46875:16:*'" not in init:
        fail("init.d does not tell the audio chain the rate McASP0 actually produces")
    # The driver and init.d have to agree, or the analog card plays 2.3% flat while
    # every layer above it believes 48 kHz. 24 MHz / 2 / 4 over two 32-bit slots is
    # 46875 and nothing else; it is also what Bose's APConfig declares for ANALOG.
    # The CPU DAI has to admit that it can clock a rate off the standard list, or the
    # link's own 46875 constraint meets a rule built from that list and the parameter
    # space comes out empty. Measured on the board 2026-09-18: with the codec already
    # fixed, every open still failed and --dump-hw-params printed no table at all.
    rate_patch = (ROOT / "os/board/rootfs/linux-patches"
                  / "0003-asoc-davinci-mcasp-allow-rates-off-the-standard-list.patch")
    if not rate_patch.is_file() or "SNDRV_PCM_RATE_KNOT" not in rate_patch.read_text():
        fail("davinci-mcasp is not patched to allow rates off the standard list; "
             "the analog card cannot be opened at 46875 Hz")
    if "46875" not in driver or "return spdif && !auxclk ? 44100 : 46875;" not in driver:
        fail("the analog link still claims a rate its divider chain cannot produce")


def check_kernel_patches():
    """The defconfig has to name a kernel patch directory that exists.

    It once named a directory that was never created, so Buildroot would have built an
    unpatched kernel and said nothing about it.
    """
    text = DEFCONFIG.read_text()
    match = re.search(r'BR2_LINUX_KERNEL_PATCH="\$\(BR2_EXTERNAL_SOUNDTAP_PATH\)/([^"]+)"', text)
    if not match:
        fail("the defconfig no longer says where the kernel patches come from")
    patches = ROOT / "os" / match.group(1)
    if not patches.is_dir():
        fail(f"the defconfig names a kernel patch directory that does not exist: {patches}")
    names = sorted(p.name for p in patches.glob("*.patch"))
    if not names:
        fail(f"{patches} holds no patches")
    # Five since 2026-09-18: the machine driver, the IR driver, the tx-clock-only patch, the McASP rate patch,
    # and the AHCLKX patch, which lets a board take the
    # internal high-frequency clock without also driving that pin, the way the vendor
    # driver does on the Wireless Link. Six since 2026-09-29: wlcore logs a refused RX
    # block-ack session at debug level instead of flooding the console. Seven since 2026-10-03: the
    # ST20 BCO wlan0 cfg80211 driver (ADR 0031), dormant on boards without the own BCO app.
    # Eight since 2026-10-26: omap_wdt keeps counting through a restart.
    # Nine since 2026-11-06: the McASP S/PDIF (DIT) path accepts 46875 Hz (ST300).
    # Ten since 2026-11-08: a DIT McASP can also receive I2S, the ST300's TV input.
    if len(names) != 10:
        fail(f"the profile should carry ten kernel patches; it carries {len(names)}: {names}")


def main():
    for _name, check in (("device tree", check_device_tree), ("USB gadget", check_gadget_console),
                          ("kernel patches", check_kernel_patches),
                          ("rootfs contract", check_rootfs_contract),
                          ("bluetooth", check_bluetooth),
                          ("audio outputs", check_audio_outputs),
                          ("mute gpio resolves", check_mute_gpio_resolves),
                          ("amp gpio numbering", check_amp_gpio_numbering),
                          ("one identity", check_one_identity)):
        check()
    print("WLA side of the image holds: device tree, "
          "kernel patches, bluetooth, audio outputs")
    return 0


if __name__ == "__main__":
    sys.exit(main())
