#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# The kernel twice, because build-stu.py checks each uImage's name against the model it is
# packaged for: "st10" for rhino and binky, "st20" for spotty. Same zImage in both.
set -eu

images=$1
: "${HOST_DIR:?Buildroot must export HOST_DIR}" "${BUILD_DIR:?}"
# mkimage writes SOURCE_DATE_EPOCH into each uImage header, and the clock when it is unset:
# then no two builds would give the same uImage (ADR 0011).
: "${SOURCE_DATE_EPOCH:?the build sets SOURCE_DATE_EPOCH}"
for model in st10 st20; do
	out=${images}/uImage-${model}
	"${HOST_DIR}/bin/mkimage" -A arm -O linux -T kernel -C none \
		-a 0x80008000 -e 0x80008000 -n "Linux-6.12 SoundTap ${model}" \
		-d "${images}/zImage" "${out}"
	test "$(wc -c <"${out}" || true)" -le 4980736 || { echo "${out} exceeds 4.75 MiB" >&2; exit 1; }
done
kver=$(sed -n 's/^BR2_LINUX_KERNEL_CUSTOM_VERSION_VALUE="\(.*\)"$/\1/p' "${BR2_CONFIG:?}")
cp "${BUILD_DIR}/linux-${kver}/.config" "${images}/kernel.config"
for opt in CONFIG_MODULES=y CONFIG_RTC_DRV_OMAP=y CONFIG_I2C_OMAP=y CONFIG_LEDS_PWM=y; do
	grep -qx "${opt}" "${images}/kernel.config" || { echo "kernel lacks ${opt}" >&2; exit 1; }
done
grep -qx 'CONFIG_CMDLINE=""' "${images}/kernel.config" \
	|| ! grep -q '^CONFIG_CMDLINE=' "${images}/kernel.config" \
	|| { echo "kernel carries a built-in command line" >&2; exit 1; }
(
	cd "${images}"
	sha256sum zImage uImage-st10 uImage-st20 kernel.config rootfs.ubi \
		am335x-soundtap-st10.dtb am335x-soundtap-wla.dtb am335x-soundtap-st20.dtb am335x-soundtap-st300.dtb \
		am335x-soundtap-st20-iii.dtb am335x-soundtap-portable.dtb am335x-soundtap-sa5.dtb am335x-soundtap-lisa.dtb am335x-soundtap-lisa-scm.dtb wla-spl/MLO wla-uboot/u-boot.img st20-spl/MLO >SHA256SUMS
)
