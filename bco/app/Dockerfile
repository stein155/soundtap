# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
RUN apt-get -qq update && apt-get -qq install -y --no-install-recommends \
    gcc-arm-none-eabi binutils-arm-none-eabi libnewlib-arm-none-eabi python3 python3-pip libglib2.0-0 libpixman-1-0 libfdt1 \
    && pip3 install --quiet --break-system-packages unicorn==2.1.4 \
    && apt-get clean
WORKDIR /repo
