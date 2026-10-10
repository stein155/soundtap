#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Runs the end-to-end tests inside the Makefile's PLAYWRIGHT_IMAGE, with /e2e (this
# directory), /tools (docs/tools, for the demo speaker) and /dist (the built web UI) mounted
# read-only: `make test-e2e`. The test runner is the one whose browsers the image carries.
set -eu
mkdir /tmp/w && cd /tmp/w
npm i --no-audit --no-fund @playwright/test@1.64.1 >/dev/null 2>&1
cp /e2e/*.mjs .
node /tools/demo-server.mjs /dist 8080 &
server=$!
trap 'kill "$server"' EXIT
# The server is up when it serves the page.
i=0
until node -e "fetch('http://localhost:8080/').then((r) => process.exit(r.ok ? 0 : 1), () => process.exit(1))"; do
	i=$((i + 1))
	[ "$i" -lt 50 ] || { echo 'demo server did not start' >&2; exit 1; }
	sleep 0.2
done
npx playwright test --config playwright.config.mjs "$@"
