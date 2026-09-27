#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Host unit tests of the own BCO app (no hardware): every tests/*_test.c, built with
# -Werror in a pinned gcc container, so no host compiler is needed. A new test file is
# picked up by the glob; only extra sources it links against need naming below.
set -eu
dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
# Pinned by digest (multi-arch index) so a new gcc:13 cannot change what passes.
IMAGE=gcc:13@sha256:16ae525998c94df36a116c191524256b1d46e72d7a0e9aaf6c153455e40eb5b8
docker run --rm -v "$dir":/w -w /w "$IMAGE" sh -eu -c '
for t in tests/*_test.c; do
	name=$(basename "$t" _test.c)
	case $name in
	mgmt) extra="tests/wifi_fake.c src/mgmt.c" ;;
	netcard) extra="tests/wifi_fake.c src/netcard.c src/mgmt.c" ;;
	*_fuzz) extra="-g -fsanitize=address,undefined -fno-sanitize-recover=all" ;;
	wifi_join) extra="-no-pie" ;; # includes wifi.c, whose 32-bit addresses must reach the globals
	*) extra= ;;
	esac
	echo "== $name"
	cc -Wall -Werror -Wno-unused-function -Isrc -o "/tmp/$name" "$t" $extra
	"/tmp/$name"
done'
