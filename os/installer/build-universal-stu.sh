#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Build the universal package, the one STU for the ST10, ST20, Wireless Link and ST300. Our
# installer, model-suffixed payloads, Bose magic.
#
# The release shape is target soundtap-tri: rhino + binky + spotty, which every model
# installs (the ST300 takes the binky payloads). `make stu` asks for it and fails without
# the ST20 kernel. Without SOUNDTAP_STU_TARGET and without an ST20 kernel the script builds
# the development-only "duo" (rhino + binky, no ST20 payload).
#
# Kernel names select model where needed. Rootfs is shared. The WLA gets its SPL and U-Boot in
# place of Bose's, and so do the ST10 and the ST300, the same sm2 module; the ST20 gets its own SPL
# in all four SPL copies and its U-Boot twice in NAND.U-Boot (installer.c).
#
set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO=$(CDPATH= cd -- "$HERE/../.." && pwd)
DIST=${SOUNDTAP_STU_OUT:-$REPO/dist/Update-soundtap.stu}

RHINO_KERNEL=${SOUNDTAP_RHINO_KERNEL:-$HERE/build/out-nand/uImage-st10}
RHINO_ROOTFS=${SOUNDTAP_RHINO_ROOTFS:-$HERE/build/out-nand/rootfs-nand.ubi}
BINKY_KERNEL=${SOUNDTAP_BINKY_KERNEL:-$RHINO_KERNEL}
SPOTTY_KERNEL=${SOUNDTAP_SPOTTY_KERNEL:-$HERE/build/out-nand-st20/uImage-st20}
# SOUNDTAP_STU_TARGET=soundtap-tri (what `make stu` asks for) requires every model's
# inputs: a missing ST20 kernel must not fall back to a two-model package under the
# universal name. Without it the target follows what was built, for development.
if [ "${SOUNDTAP_STU_TARGET:-}" = soundtap-tri ] && [ ! -f "$SPOTTY_KERNEL" ]; then
	echo "missing soundtap-tri input: $SPOTTY_KERNEL (the ST20 kernel); refusing a two-model package" >&2
	exit 2
fi
if [ -f "$SPOTTY_KERNEL" ]; then
	TARGET=soundtap-tri
	SPOTTY_OUT=${SOUNDTAP_SPOTTY_BOOT:-$HERE/build}
	SPOTTY_MLO=${SOUNDTAP_SPOTTY_MLO:-$SPOTTY_OUT/out-st20-spl/MLO}
	SPOTTY_ARG="--spotty-kernel $SPOTTY_KERNEL --spotty-mlo $SPOTTY_MLO"
else
	TARGET=soundtap-duo
	SPOTTY_ARG=
fi
MLO=${SOUNDTAP_BINKY_MLO:-$HERE/build/out-product-wla-spl/MLO}
UBOOT=${SOUNDTAP_BINKY_UBOOT:-$HERE/build/out-uboot-wla/u-boot.img}
for input in "$RHINO_KERNEL" "$RHINO_ROOTFS" "$BINKY_KERNEL" "$MLO" "$UBOOT" \
	${SPOTTY_ARG:+"$SPOTTY_MLO"}; do
	test -f "$input" || { echo "missing $TARGET input: $input" >&2; exit 2; }
done

WLA_NAND=1 WLA_BOOTLOADERS=1 sh "$REPO/os/installer/module/build.sh" \
	"$REPO/os/installer/module/build" >/dev/null
INSTALLER=$REPO/os/installer/module/build/wla-installer-full
test -f "$INSTALLER" || { echo "installer binary $INSTALLER is missing" >&2; exit 2; }

mkdir -p "$(dirname -- "$DIST")"
# shellcheck disable=SC2086 # $SPOTTY_ARG is a deliberate word-split expansion, empty for duo
python3 "$REPO/os/installer/build-stu.py" --tri-model --target "$TARGET" \
	--installer "$INSTALLER" \
	--rhino-kernel "$RHINO_KERNEL" --rhino-rootfs "$RHINO_ROOTFS" \
	--binky-kernel "$BINKY_KERNEL" $SPOTTY_ARG \
	--mlo "$MLO" --u-boot "$UBOOT" --out "$DIST"

python3 "$REPO/os/support/stu_check.py" accept "$DIST" "$TARGET"
if [ "${SOUNDTAP_SKIP_DRY_RUN:-0}" != 1 ]; then
	(cd "$REPO/os/tests" && python3 -m unittest installer.test_wla_installer_offline.TestTriModelDispatch)
	echo "$TARGET dispatch dry run: passed"
	# And the route stock would take: our installer is module 0, which stock runs by
	# position, so this replays stock's half of :17008 from the package's own bytes and
	# lets the real installer run its whole transaction against partition-sized files.
	#
	# As binky, because that is the model this dry run knows: it checks the boot chain
	# order and the mainline recipes, and those are the Wireless Link's. The ST10 takes the
	# same sm2 chain, which TestTriModelDispatch above covers.
	python3 -B "$REPO/os/support/wla_stock_route_dryrun.py" --bootloaders \
		--variant binky "$DIST" >/dev/null
	echo "stock-route dry run on binky: installs cleanly"
	if [ "$TARGET" = soundtap-tri ]; then
		python3 -B "$REPO/os/support/wla_stock_route_dryrun.py" --bootloaders \
			--variant spotty "$DIST" >/dev/null
		echo "stock-route dry run on spotty: installs cleanly"
	fi
fi
(cd "$(dirname -- "$DIST")" && sha256sum "$(basename -- "$DIST")" >"$(basename -- "$DIST").sha256")
cat "$DIST.sha256"
