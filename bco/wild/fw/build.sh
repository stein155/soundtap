#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Build our WiLD core image: build.sh OUT_DIR. Uses the BCO toolchain image (bco/app/Dockerfile).
set -eu
src=$(cd "$(dirname "$0")" && pwd)
out=${1:-$src/out}
mkdir -p "$out"
cc=${CROSS:-arm-none-eabi-}
flags="-march=armv4t -marm -Os -ffreestanding -nostdlib -fno-unwind-tables -fno-asynchronous-unwind-tables -Wall -Werror -ffunction-sections -fdata-sections -I$src/../../app/src"
${cc}gcc $flags -Wl,-T,"$src/wild.ld" "$src"/src/start.S "$src"/src/*.c -Wl,--gc-sections -lgcc -o "$out/wild.elf"
${cc}objcopy -O binary "$out/wild.elf" "$out/wild.bin"
${cc}size "$out/wild.elf"
