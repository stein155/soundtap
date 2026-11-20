#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Runs inside the Makefile's PLAYWRIGHT_IMAGE with /tools (this directory), /dist (the built
# web UI), /shots (screenshots) and /out (docs/assets) mounted. `make screenshots` builds the
# UI and runs it twice: in-container.sh shoot.mjs, then in-container.sh compose.mjs.
set -e
mkdir /tmp/w && cd /tmp/w
npm i playwright@1.64.1 >/dev/null 2>&1
cp /tools/*.mjs .
node demo-server.mjs /dist 8080 &
sleep 1
node "${1:-shoot.mjs}" /shots
kill $!
