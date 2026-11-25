#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# One BCO app variant: build-app.sh VARIANT SRC_DIR OUT_DIR   (CROSS=arm-none-eabi- prefix in env)
# Writes OUT_DIR/VARIANT/{app.elf,app.bin,app.bcd}. No stock bytes, no QEMU: the QEMU gate stays
# a local step (build-*.sh).
set -eu
v=$1 src=$2 out=$3/$1 cc=${CROSS:-arm-none-eabi-}
case ${v} in
hello)       files=src/hello.c ;;
usb)         files=src/usb.c;  defs=-DUSB_IRQ ;;
usb-connect) files=src/usb.c;  defs="-DUSB_IRQ -DUSB_CONNECT" ;;
mon)         files=src/mon.c ;;
link-own)    # the network card with our own WiLD core linked in (WILD_BIN=wild.bin, bco/wild/fw)
             files="src/netcard.c src/usblink.c src/mgmt.c src/wifi.c src/wpa.c src/wired.c src/wild_own.S"
             defs="-DNETCARD_HW -fno-tree-loop-distribute-patterns -DWILD_OWN -DWILD_BIN=\"${WILD_BIN:?}\"" ;;
*) echo "unknown variant ${v}" >&2; exit 1 ;;
esac
mkdir -p "${out}"
flags="-mcpu=arm926ej-s -marm -Os -ffreestanding -fno-unwind-tables -fno-asynchronous-unwind-tables -nostdlib -ffile-prefix-map=${src}=. ${defs:-}"
# shellcheck disable=SC2086 # flags is a list of compiler options, one word each
"${cc}gcc" ${flags} -Wl,-T,"${src}/first.ld" "${src}/src/start.S" "${src}/src/first.c" -o "${out}/first.elf"
# shellcheck disable=SC2046,SC2086 # one argument per source file; flags is a list of compiler options
"${cc}gcc" ${flags} -Wl,-T,"${src}/app.ld" "${src}/src/app.S" $(for f in ${files}; do echo "${src}/${f}"; done) -lgcc -o "${out}/app.elf"
for s in first app; do "${cc}objcopy" -O binary "${out}/${s}.elf" "${out}/${s}.bin"; done
python3 "${BCO_PACK:-${src}/../../bco/tools/bco-pack}" "${out}/first.bin" "${out}/app.bin" "${out}/app.bcd"
rm -f "${out}/first.elf" "${out}/first.bin"
