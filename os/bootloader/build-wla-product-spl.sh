#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Build WLA product SPL: NAND loads U-Boot; lab USB/network transports stay absent.
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO=$(CDPATH= cd -- "$HERE/../.." && pwd)
OUT=${WLA_PRODUCT_SPL_OUT:-$HERE/build/out-product-wla-spl}
ENTRY_RESET=${WLA_PRODUCT_SPL_ENTRY_RESET:-}
NAND_MARKERS=${WLA_PRODUCT_SPL_NAND_MARKERS:-}
USB_RECOVERY=${WLA_SPL_USB_RECOVERY:-}
# ST20: the same SPL with USB gadget first, because the ROM there boots NAND before USB
# while the ST10 and WLA ROMs ask USB on their own. Only the config differs: the WLA ROM
# is proven to load an SPL of at most 0x7428 bytes, too small for the USB stack.
ST20_SPL=${ST20_SPL:-}
IMAGE=debian:bookworm-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818
VERSION=2026.07
SOURCE_SHA256=78e8bfc382fe388f9b55aa1daf8c563522a037779b5d4c349d1415e381f1243e
# Same constant as the U-Boot build, for the same reason. The product SPL has come out
# byte-identical across every build so far even with this taken from HEAD -- it carries no
# banner -- but that was luck rather than design, and the two belong to one boot chain.
: "${SOURCE_DATE_EPOCH:=$(cat "$REPO/os/bootloader/source-date-epoch")}"
export SOURCE_DATE_EPOCH

mkdir -p "$OUT"
OUT=$(CDPATH= cd -- "$OUT" && pwd)
docker run --rm -e SOURCE_DATE_EPOCH -v "$REPO:/repo:ro" -v "$OUT:/out" "$IMAGE" sh -euc '
  export DEBIAN_FRONTEND=noninteractive
  apt-get -qq update
  apt-get -qq install -y --no-install-recommends build-essential gcc-arm-linux-gnueabihf \
    bison flex libssl-dev libgnutls28-dev uuid-dev pkg-config python3 python3-dev \
    python3-setuptools python3-pyelftools device-tree-compiler swig bc git ca-certificates curl
  cd /tmp
  curl -fsSLO https://ftp.denx.de/pub/u-boot/u-boot-'"$VERSION"'.tar.bz2
  printf "%s  %s\n" '"$SOURCE_SHA256"' u-boot-'"$VERSION"'.tar.bz2 | sha256sum -c -
  tar -xjf u-boot-'"$VERSION"'.tar.bz2
  cd u-boot-'"$VERSION"'
  test "$(sed -n "s/^VERSION = //p" Makefile).$(sed -n "s/^PATCHLEVEL = //p" Makefile)" = '"$VERSION"'
  git init -q
  git add . && GIT_AUTHOR_DATE="@$SOURCE_DATE_EPOCH +0000" GIT_COMMITTER_DATE="@$SOURCE_DATE_EPOCH +0000" \
    git -c user.name=build -c user.email=build@example.invalid commit -qm source
  # The same patch stack as the lab SPL, which is the only SPL proven to run on this
  # board. The product SPL used to be built from docs/hardware/wla/uboot-2026.07-nand-final, which
  # has none of the WLA clock and DDR work in s_init, and it got control and then stopped:
  # loaded by the ROM, reset marker at board_init_f fired, nothing after. One stack, two
  # configs -- this one has no USB at all, and wla-am335x-bringup.patch takes the ordinary
  # boot-device path when SPL_USB_ETHER is off.
  git apply /repo/os/bootloader/uboot/wla-am335x-bringup.patch
  git apply /repo/os/bootloader/uboot/wla-uboot-nand-base.patch
  # Diagnostic-only. WLA_PRODUCT_SPL_ENTRY_RESET=1 makes the SPL warm-reset the moment
  # it gets control, so a fresh mask-ROM generation answers whether it runs at all.
  [ "'"$ENTRY_RESET"'" = 1 ] && git apply /repo/os/bootloader/uboot/wla-product-spl-entry-reset.patch
  # Diagnostic-only. Erases first two mtd11 eraseblocks and writes raw stage bytes.
  [ "'"$NAND_MARKERS"'" = 1 ] && git apply /repo/os/bootloader/uboot/wla-product-spl-nand-markers.patch
  # Replacing NAND.U-Boot is the one step with no way back: mtd0 has three spare copies
  # the mask ROM reads in order, mtd4 has no redundant partner, and the stock button
  # recovery that reads Update.stu from a stick lives in the U-Boot being replaced. This
  # rebuilds that net one layer lower, where replacing U-Boot cannot remove it: NAND
  # first, then a FAT USB stick. See docs/decisions/0009-mainline-linux-own-bootloaders.md.
  [ "'"$USB_RECOVERY"'" = 1 ] && git apply /repo/os/bootloader/uboot/uboot-2026.07-spl-usb-recovery.patch
  # Every model: module clocks the ROM may leave off, a 120 s WDT1 boot watchdog, and the
  # USB-first step that only a USB-capable build on the ST20 takes.
  git apply /repo/os/bootloader/uboot/uboot-2026.07-spl-board.patch
  git apply /repo/os/bootloader/uboot/uboot-2026.07-spl-second-uboot.patch
  cp /repo/os/bootloader/uboot/uboot-am335x-soundtap-wla.dts arch/arm/dts/am335x-soundtap-wla.dts
  export ARCH=arm CROSS_COMPILE=arm-linux-gnueabihf-
  make am335x_evm_defconfig < /dev/null
  ./scripts/config --file .config \
    --disable OF_UPSTREAM \
    --enable MTD --enable MTD_RAW_NAND --enable NAND_OMAP_GPMC --enable NAND_OMAP_ELM \
    --enable NAND_OMAP_ECCSCHEME_BCH8_CODE_HW --disable NAND_OMAP_GPMC_PREFETCH \
    --enable SPL_MTD --enable SPL_LOAD_BLOCK --enable SPL_NAND_SUPPORT \
    --enable SPL_NAND_DRIVERS --enable SPL_NAND_ECC --enable SPL_NAND_BASE \
    --enable SPL_NAND_IDENT --enable SPL_NAND_AM33XX_BCH \
    --enable SPL_NAND_INIT --enable SPL_LEGACY_IMAGE_FORMAT \
    --disable SPL_LOAD_FIT --disable SPL_OS_BOOT \
    --disable SPL_I2C --disable SPL_DM_I2C \
    --disable SPL_POWER --disable SPL_POWER_TPS65910 --disable SPL_PMIC \
    --disable SPL_SERIAL --disable SPL_YMODEM_SUPPORT --disable SPL_WDT \
    --disable SPL_FAT_SUPPORT --disable SPL_LIBDISK_SUPPORT --disable SPL_FS_FAT \
    --disable SPL_SYS_MALLOC --set-val SPL_SYS_MALLOC_F_LEN 0x2000 \
    --disable SPL_USB_GADGET --disable SPL_USB_ETHER --disable SPL_NET --disable SPL_ETH \
    --disable SPL_MMC --disable MMC --disable USB --disable USB_GADGET --disable NET \
    --disable CMD_DHCP --disable CMD_TFTPBOOT --disable CMD_BOOTP --disable CMD_MMC \
    --disable USB_ETHER --disable USB_ETH_RNDIS --disable USB_GADGET_DOWNLOAD \
    --disable USB_FUNCTION_ACM --disable SPL_MUSB_NEW --disable SPL_DM_USB_GADGET \
    --disable SPL_FALCON_BOOT_MMCSD --disable SPL_SYS_MMCSD_RAW_MODE \
    --set-str DEFAULT_DEVICE_TREE am335x-soundtap-wla \
    --set-val SPL_TEXT_BASE 0x402f0400 --set-val SPL_SIZE_LIMIT 0x7420 \
    --enable SYS_NAND_U_BOOT_LOCATIONS --enable SYS_NAND_ONFI_DETECTION \
    --set-val SYS_NAND_PAGE_SIZE 0x800 --set-val SYS_NAND_OOBSIZE 0x40 \
    --set-val SYS_NAND_BLOCK_SIZE 0x20000 --set-val SYS_NAND_U_BOOT_OFFS 0x80000 --set-val SYS_NAND_U_BOOT_OFFS_REDUND 0x180000 --enable SPL_LEGACY_IMAGE_CRC_CHECK
  # A second call rather than edits above, so the default build is byte for byte the one
  # check_wla_product_spl.py pins. 0x7420 is not a hardware limit -- it matches the size
  # stock ships -- and the SRAM budget the lab SPL already lives inside is 0xfc00, which
  # is what USB host, FAT and the block layer have to fit within.
  [ "'"$USB_RECOVERY"'" = 1 ] && ./scripts/config --file .config \
    --enable USB --enable USB_MUSB_HOST --enable USB_MUSB_TI --enable USB_MUSB_DSPS \
    --enable AM335X_PHY_USB --enable NOP_USB_XCEIV --enable USB_STORAGE --enable BLK \
    --enable SPL_USB_HOST --enable SPL_USB_STORAGE --enable SPL_BLK \
    --enable SPL_FS_FAT --enable SPL_LIBDISK_SUPPORT --enable SPL_DM --enable SPL_OF_CONTROL \
    --set-val SPL_SIZE_LIMIT 0xfc00 \
    --disable SPL_MMC --disable SPL_SYS_MMCSD_RAW_MODE --disable SPL_FALCON_BOOT_MMCSD \
    --disable DM_USB_GADGET --disable USB_MUSB_GADGET --disable SPL_DM_USB_GADGET
  # Host only. ti-musb.c compiles its peripheral half behind DM_USB_GADGET, which the
  # EVM defconfig leaves on, and then the link fails on usb_del_gadget_udc because
  # USB_GADGET is off. The fallback reads a stick; it never needs to be a device.
  # The USB-net config of the lab SPL (build-wla-uboot.sh) on top of the NAND one. One BOOTP
  # retry keeps an unanswered port at about 11 s: 3 s connect, 8 s BOOTP.
  [ "'"$ST20_SPL"'" = 1 ] && ./scripts/config --file .config \
    --enable USB --enable USB_GADGET --enable NET --enable USB_ETHER --enable USB_ETH_RNDIS \
    --enable USB_MUSB_GADGET --enable USB_MUSB_TI --enable USB_MUSB_DSPS \
    --enable SPL_USB_GADGET --enable SPL_USB_ETHER --enable SPL_NET --enable SPL_MUSB_NEW \
    --enable SPL_DM_USB_GADGET --set-val NET_RETRY_COUNT 1 \
    --set-val SPL_SIZE_LIMIT 0xfc00
  make olddefconfig < /dev/null
  if [ "'"$ST20_SPL"'" = 1 ]; then
    for c in SPL_USB_ETHER=y SPL_NAND_SUPPORT=y SYS_NAND_U_BOOT_OFFS=0x80000 SYS_NAND_U_BOOT_OFFS_REDUND=0x180000 SPL_LEGACY_IMAGE_CRC_CHECK=y NET_RETRY_COUNT=1; do
      grep -qx "CONFIG_$c" .config || { echo "ST20 SPL lost CONFIG_$c" >&2; exit 1; }
    done
  fi
  # AM335x board code declares dormant non-DM MMC platdata without an MMC guard.
  # MLO is the mkimage omapimage wrapper around spl/u-boot-spl.bin: TI configuration
  # header, then a GP header carrying size and load address. That is what the mask ROM
  # reads out of NAND: the stock mtd0 on this board has that exact shape. The raw .bin is
  # the USB format.
  KCFLAGS="-DCONFIG_SYS_MMC_MAX_BLK_COUNT=0 -DCONFIG_USB_MAX_CONTROLLER_COUNT=0" make -j${WLA_PRODUCT_SPL_JOBS:-2} spl/u-boot-spl.bin
  KCFLAGS="-DCONFIG_SYS_MMC_MAX_BLK_COUNT=0 -DCONFIG_USB_MAX_CONTROLLER_COUNT=0" make -j${WLA_PRODUCT_SPL_JOBS:-2} MLO
  cp MLO /out/MLO
  # Stock U-Boot bootm-s this into SRAM, which runs the SPL once without writing NAND.
  [ "'"$ST20_SPL"'" = 1 ] && ./tools/mkimage -A arm -O linux -T kernel -C none -a 0x402f0400 \
    -e 0x402f0400 -n "ST20 SPL SRAM test" -d spl/u-boot-spl.bin /out/uImage-spl-sram
  cp .config spl/u-boot-spl spl/u-boot-spl.bin spl/u-boot-spl.map /out/
  arm-linux-gnueabihf-size -A -d spl/u-boot-spl > /out/spl-size.txt
  arm-linux-gnueabihf-nm -n spl/u-boot-spl > /out/spl-symbols.txt
  arm-linux-gnueabihf-objdump -d spl/u-boot-spl > /out/spl-disassembly.txt
  {
    printf "source_url=https://ftp.denx.de/pub/u-boot/u-boot-'"$VERSION"'.tar.bz2\n"
    printf "source_sha256='"$SOURCE_SHA256"'\n"
    printf "source_revision=ece349ade2973e220f524ce59e59711cc919263f\n"
    printf "patch_sha256="; { cat /repo/os/bootloader/uboot/wla-am335x-bringup.patch /repo/os/bootloader/uboot/wla-uboot-nand-base.patch /repo/os/bootloader/uboot/uboot-2026.07-spl-board.patch; [ "'"$NAND_MARKERS"'" = 1 ] && cat /repo/os/bootloader/uboot/wla-product-spl-nand-markers.patch || :; } | sha256sum | cut -d" " -f1
    [ "'"$NAND_MARKERS"'" = 1 ] && printf "diagnostic_nand_markers=mtd11:0x0,0x20000\n" || :
    printf "dts_sha256="; sha256sum /repo/os/bootloader/uboot/uboot-am335x-soundtap-wla.dts | cut -d" " -f1
    printf "config_sha256="; sha256sum .config | cut -d" " -f1
    printf "mlo_sha256="; sha256sum /out/MLO | cut -d" " -f1
    printf "source_date_epoch=%s\n" "$SOURCE_DATE_EPOCH"
    printf "compiler="; arm-linux-gnueabihf-gcc --version | head -n 1
    printf "container='"$IMAGE"'\n"
  } > /out/PROVENANCE
  sha256sum /out/MLO > /out/MLO.sha256
'
[ "$ENTRY_RESET$NAND_MARKERS$ST20_SPL" != "" ] || python3 "$REPO/os/support/check_wla_product_spl.py" "$OUT"
