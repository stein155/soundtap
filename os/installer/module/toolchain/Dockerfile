# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Pinned cross toolchain for the WLA installer: static armv7 userland, no Go.
FROM debian:bookworm-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818
# The packages from one dated Debian snapshot, as in os/container/Dockerfile.builder: the
# live mirror moves with every point release, and a newer gcc or libc would put other bytes
# into the static installer, so into the STU (ADR 0011).
RUN sed -i \
        -e 's|http://deb.debian.org/debian-security|http://snapshot.debian.org/archive/debian-security/20260801T000000Z|g' \
        -e 's|http://deb.debian.org/debian|http://snapshot.debian.org/archive/debian/20260801T000000Z|g' \
        /etc/apt/sources.list.d/debian.sources \
 && apt-get -qq -o Acquire::Check-Valid-Until=false update \
 && apt-get -qq install -y gcc-arm-linux-gnueabihf libc6-dev-armhf-cross \
 && rm -rf /var/lib/apt/lists/*
