#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# The checks CI runs on every pull request and on main, by step name. Each CI job runs one
# step through this script; a laptop or a build host runs all of them the same way
# (docs/contributors/testing/README.md).
#
#   tools/ci/run-ci.sh               every step, side by side, one log per step
#   tools/ci/run-ci.sh go python     only these steps
#
#   lint      make lint           formatting and static analysis, workflows included
#   web       make test-web       the frontend: lint, format, types, vitest, build
#   e2e       make test-e2e       the frontend end to end: Playwright against the demo speaker
#   go        make test-go        gofmt, both build targets, go vet, go test
#   go-race   make test-go-race   go test -race
#   python    make test-python    the Python tests in os/tests
#   firewall  make test-firewall  the production firewall in a network namespace
#   bco       make test-bco       the BCO app's and WiLD core's C tests, the first stage
#   vuln      make vuln           govulncheck and npm audit
#
# One step runs in the foreground with its output as it comes (a CI job). Several run at
# the same time, each into output/ci/<step>.log, so a run takes as long as its slowest
# step; the tail of each failed log is printed at the end. Exits 1 when a step fails.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
ALL="lint web e2e go go-race python firewall bco vuln"
MAKE=${MAKE:-make}

target() {
	case $1 in
	lint) echo lint ;;
	web) echo test-web ;;
	e2e) echo test-e2e ;;
	go) echo test-go ;;
	go-race) echo test-go-race ;;
	python) echo test-python ;;
	firewall) echo test-firewall ;;
	bco) echo test-bco ;;
	vuln) echo vuln ;;
	*) echo "unknown step $1; steps: $ALL" >&2; return 2 ;;
	esac
}

steps=${*:-$ALL}
for s in $steps; do target "$s" >/dev/null || exit 2; done
if [ "$#" -eq 1 ]; then
	exec $MAKE -C "$ROOT" --no-print-directory "$(target "$1")"
fi

logs=$ROOT/output/ci
mkdir -p "$logs"
for s in $steps; do
	rm -f "$logs/$s.rc"
	( rc=0; $MAKE -C "$ROOT" --no-print-directory "$(target "$s")" >"$logs/$s.log" 2>&1 || rc=$?
	  echo "$rc" >"$logs/$s.rc" ) &
done
wait
failed=
for s in $steps; do
	rc=$(cat "$logs/$s.rc" 2>/dev/null || echo '?')
	if [ "$rc" = 0 ]; then
		echo "ok    $s"
	else
		echo "FAIL  $s (exit $rc): $logs/$s.log"
		failed="$failed $s"
	fi
done
for s in $failed; do
	echo "== $s"
	tail -n 40 "$logs/$s.log"
done
[ -z "$failed" ]
