# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# A native C compiler for the offline installer gate. The self-hosted runner has Docker but no
# toolchain, and the gate compiles the same installer sources the board runs.
FROM debian:bookworm-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818
RUN apt-get -qq update \
 && apt-get -qq install -y --no-install-recommends gcc libc6-dev \
 && rm -rf /var/lib/apt/lists/*
