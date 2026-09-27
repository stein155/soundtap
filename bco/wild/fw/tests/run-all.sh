#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Host unit tests of our WiLD core (no hardware): every tests/*_test.c, each one program that
# includes the sources it tests, built with -Werror under AddressSanitizer and UBSan in the same
# pinned gcc container as bco/app/tests, so no host compiler is needed. -no-pie keeps the globals
# below 4 GiB, where the core's 32-bit addresses reach them (tests/host.h). A new test file is
# picked up by the glob.
set -eu
dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
# Pinned by digest (multi-arch index) so a new gcc:13 cannot change what passes.
IMAGE=gcc:13@sha256:16ae525998c94df36a116c191524256b1d46e72d7a0e9aaf6c153455e40eb5b8
docker run --rm -v "$dir":/w:ro -w /w "$IMAGE" sh -eu -c '
flags="-std=gnu11 -O1 -g -Wall -Wextra -Werror -Wno-unused-function -Wno-unused-parameter
	-Wno-sign-compare -Wno-pointer-to-int-cast -Wno-int-to-pointer-cast -no-pie
	-fsanitize=address,undefined -fno-sanitize-recover=all -fno-omit-frame-pointer"
export ASAN_OPTIONS=detect_leaks=0:abort_on_error=0 UBSAN_OPTIONS=print_stacktrace=1
for t in tests/*_test.c; do
	name=$(basename "$t" _test.c)
	echo "== wild $name"
	cc $flags -Isrc -o "/tmp/$name" "$t"
	"/tmp/$name"
done'
