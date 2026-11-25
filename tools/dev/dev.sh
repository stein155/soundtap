#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors
#
# The developer loop behind `make dev` (CONTRIBUTING.md, "Develop without a speaker"):
# soundtapd -dev on the simulated speaker, and the Vite dev server with hot reload proxying
# /api (SSE included) to it. No speaker, no audio, nothing announced on the LAN; both
# listen on 127.0.0.1 only.
#
#   tools/dev/dev.sh           daemon and UI; Ctrl-C stops both
#   tools/dev/dev.sh daemon    the daemon alone, in the foreground (os/tests/build/test_dev_loop.py)
#
# MODEL        simulated speaker: st10 (default), st20, st300 or wla
# DEV_PORT     soundtapd's API port (8080); DEV_UI_PORT Vite's (5173)
# DEV_HOME     the throwaway homes, one per MODEL (output/dev-home); delete it to start over
# DEV_BIN      where soundtapd is built (output/dev)
# DEV_CONTAINERS  auto (default): the host's Go >= 1.26 and Node >= 22 when both are there,
#              else the pinned Go and Node containers (GO_IMAGE, NODE_IMAGE: make dev passes
#              them); 1: always the containers; 0: never (fails without the host tools)
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
MODEL=${MODEL:-st10}
DEV_PORT=${DEV_PORT:-8080}
DEV_UI_PORT=${DEV_UI_PORT:-5173}
DEV_HOME=${DEV_HOME:-${ROOT}/output/dev-home}
DEV_CONTAINERS=${DEV_CONTAINERS:-auto}
FRONTEND=${ROOT}/app/frontend
BIN_DIR=${DEV_BIN:-${ROOT}/output/dev}
mode=${1:-all}

case ${MODEL} in
st10 | st20 | st300 | wla) ;;
*)
	echo "MODEL must be st10, st20, st300 or wla (got '${MODEL}')" >&2
	exit 2
	;;
esac
case ${mode} in
all | daemon) ;;
*)
	echo "usage: $0 [daemon]" >&2
	exit 2
	;;
esac

# at_least VERSION MIN: whether dotted VERSION is MIN or newer.
at_least() {
	local IFS=.
	# shellcheck disable=SC2206 # splitting on dots is the point
	local have=($1) want=($2) i
	for i in 0 1 2; do
		if ((${have[i]:-0} != ${want[i]:-0})); then
			((${have[i]:-0} > ${want[i]:-0}))
			return
		fi
	done
}

host_go() {
	command -v go >/dev/null 2>&1 || return 1
	local v
	v=$(go env GOVERSION 2>/dev/null) || return 1
	v=${v#go}
	at_least "${v%%[!0-9.]*}" 1.26
}

host_node() {
	command -v node >/dev/null 2>&1 && command -v npm >/dev/null 2>&1 || return 1
	at_least "$(node -p 'process.versions.node' || true)" 22
}

# //go:embed all:dist needs one file; the real UI comes from Vite here (as make go-dist).
embed_dir() {
	mkdir -p "${ROOT}/app/internal/web/dist"
	[[ -n "$(ls -A "${ROOT}/app/internal/web/dist" || true)" ]] || touch "${ROOT}/app/internal/web/dist/.keep"
}

# wait_for_api PID: until soundtapd answers, as long as PID runs (60 s at most).
wait_for_api() {
	local i
	if ! command -v curl >/dev/null 2>&1; then
		sleep 2 # no curl to ask with: soundtapd is up within a second
		return 0
	fi
	for ((i = 0; i < 120; i++)); do
		if curl -fsS "http://127.0.0.1:${DEV_PORT}/api/version" >/dev/null 2>&1; then
			return 0
		fi
		kill -0 "$1" 2>/dev/null || break
		sleep 0.5
	done
	echo "soundtapd did not answer on 127.0.0.1:${DEV_PORT}" >&2
	return 1
}

banner() {
	printf '\n  SoundTap dev: simulated %s\n  UI   http://localhost:%s/  (hot reload)\n  API  http://127.0.0.1:%s/api/  home %s\n  Ctrl-C stops both.\n\n' \
		"${MODEL}" "${DEV_UI_PORT}" "${DEV_PORT}" "$1"
}

if [[ "${DEV_CONTAINERS}" = auto ]]; then
	DEV_CONTAINERS=1
	# shellcheck disable=SC2310 # a test: false means containers
	if host_go && { [[ "${mode}" = daemon ]] || host_node; }; then
		DEV_CONTAINERS=0
	fi
elif [[ "${DEV_CONTAINERS}" = 0 ]]; then
	# shellcheck disable=SC2310 # a test: false means containers
	host_go || {
		echo "Go 1.26 or newer is needed on the host (DEV_CONTAINERS=0)" >&2
		exit 1
	}
	# shellcheck disable=SC2310 # a test: false means containers
	[[ "${mode}" = daemon ]] || host_node || {
		echo "Node 22 or newer is needed on the host (DEV_CONTAINERS=0)" >&2
		exit 1
	}
fi

# ---- On the host: the fast path ----

native_build() {
	embed_dir
	mkdir -p "${BIN_DIR}" "${DEV_HOME}/${MODEL}"
	echo "building soundtapd with $(go env GOVERSION || true)" >&2
	(cd "${ROOT}/app" && go build -buildvcs=false -o "${BIN_DIR}/soundtapd" ./cmd/soundtapd)
}

native_run() {
	# SOUNDTAP_FIRMWARE would claim a flashed speaker; -dev refuses it, so do not inherit one.
	unset SOUNDTAP_FIRMWARE
	export MODEL
	exec "${BIN_DIR}/soundtapd" -dev -listen "127.0.0.1:${DEV_PORT}" -home "${DEV_HOME}/${MODEL}/soundtap"
}

# npm ci when package-lock.json changed since the last one, not on every start.
npm_deps() {
	if ! cmp -s package-lock.json node_modules/.soundtap-dev-lock 2>/dev/null; then
		npm ci --ignore-scripts --no-audit --no-fund
		cp package-lock.json node_modules/.soundtap-dev-lock
	fi
}

pids=()
stop_all() {
	trap - EXIT INT TERM
	if ((${#pids[@]})); then
		kill -TERM "${pids[@]}" 2>/dev/null || true
		wait "${pids[@]}" 2>/dev/null || true
	fi
}

# Returns when either process ends; macOS's bash 3.2 has no wait -n.
watch_pids() {
	local p
	while :; do
		for p in "${pids[@]}"; do
			kill -0 "${p}" 2>/dev/null || return 0
		done
		sleep 1
	done
}

native_all() {
	(cd "${FRONTEND}" && npm_deps)
	native_build
	trap stop_all EXIT
	trap 'exit 0' INT # Ctrl-C is how this loop ends
	trap 'exit 143' TERM
	native_run &
	pids+=($!)
	wait_for_api "$!"
	banner "${DEV_HOME}/${MODEL}/soundtap"
	(cd "${FRONTEND}" && SOUNDTAP_URL="http://127.0.0.1:${DEV_PORT}" exec npx vite --port "${DEV_UI_PORT}" --strictPort) &
	pids+=($!)
	watch_pids
}

# ---- In the pinned containers: slower, needs only Docker ----

NET=soundtap-dev
DAEMON=soundtap-dev-daemon
UI=soundtap-dev-ui
DOCKER=${DOCKER:-docker}

need_images() {
	if [[ -z "${GO_IMAGE:-}" ]] || [[ -z "${NODE_IMAGE:-}" ]]; then
		echo "Neither the host's Go and Node nor GO_IMAGE/NODE_IMAGE; run it as make dev" >&2
		exit 1
	fi
}

container_build() {
	embed_dir
	mkdir -p "${BIN_DIR}" "${DEV_HOME}/${MODEL}"
	echo "building soundtapd in ${GO_IMAGE}" >&2
	"${DOCKER}" run --rm -v "${ROOT}:/w" -w /w/app -e CGO_ENABLED=0 \
		-v soundtap-gomod:/go/pkg/mod -v soundtap-gocache:/root/.cache \
		-v "${BIN_DIR}:/dev-bin" "${GO_IMAGE}" go build -buildvcs=false -o /dev-bin/soundtapd-linux ./cmd/soundtapd
}

# The daemon runs as the caller, so the home stays theirs. It listens on every address of
# its container: the UI container reaches it by name, the host through 127.0.0.1 only.
container_run() {
	"${DOCKER}" run --rm --init --name "${DAEMON}" --network "${NET}" --user "$(id -u || true):$(id -g || true)" \
		-p "127.0.0.1:${DEV_PORT}:8080" -e "MODEL=${MODEL}" -v "${BIN_DIR}:/dev-bin:ro" -v "${DEV_HOME}/${MODEL}:/dev-home" \
		"${GO_IMAGE}" /dev-bin/soundtapd-linux -dev -listen :8080 -home /dev-home/soundtap
}

container_down() {
	"${DOCKER}" rm -f "${DAEMON}" "${UI}" >/dev/null 2>&1 || true
	"${DOCKER}" network rm "${NET}" >/dev/null 2>&1 || true
}

container_daemon() {
	need_images
	container_build
	container_down
	"${DOCKER}" network create "${NET}" >/dev/null
	trap container_down EXIT
	container_run
}

container_all() {
	need_images
	container_build
	container_down
	"${DOCKER}" network create "${NET}" >/dev/null
	trap 'stop_all; container_down' EXIT
	trap 'exit 0' INT
	trap 'exit 143' TERM
	container_run &
	pids+=($!)
	wait_for_api "$!"
	banner "${DEV_HOME}/${MODEL}/soundtap"
	# node_modules lives in a volume: Linux binaries never land in a macOS tree.
	"${DOCKER}" run --rm --init --name "${UI}" --network "${NET}" -p "127.0.0.1:${DEV_UI_PORT}:5173" \
		-e "SOUNDTAP_URL=http://${DAEMON}:8080" -e npm_config_update_notifier=false \
		-v "${FRONTEND}:/w/app/frontend" -v "${ROOT}/app/internal/web/openapi.json:/w/app/internal/web/openapi.json:ro" \
		-v soundtap-dev-node-modules:/w/app/frontend/node_modules -w /w/app/frontend "${NODE_IMAGE}" sh -euc '
		if ! cmp -s package-lock.json node_modules/.soundtap-dev-lock 2>/dev/null; then
			npm ci --ignore-scripts --no-audit --no-fund && cp package-lock.json node_modules/.soundtap-dev-lock
		fi
		exec npx vite --host 0.0.0.0 --port 5173 --strictPort' &
	pids+=($!)
	watch_pids
}

case "${mode}:${DEV_CONTAINERS}" in
daemon:0)
	native_build
	native_run
	;;
daemon:1) container_daemon ;;
all:0) native_all ;;
all:1) container_all ;;
*)
	echo "DEV_CONTAINERS must be auto, 0 or 1" >&2
	exit 2
	;;
esac
