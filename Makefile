# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

SHELL := /bin/bash

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BUILDROOT := $(ROOT)/buildroot
BUILDROOT_COMMIT := 4f924fd52841608d68ac3dcb83fccee8b8c90316
# The pinned Buildroot is the LTS branch, 2025.02.19. A command-line <PKG>_VERSION overrides
# the package's .mk where we need a newer release than the LTS carries; each tarball hash is in
# os/board/common/patches/<package>/<package>.hash. Go stays on the release soundtapd is
# tested with (the LTS moved to 1.27). Drop an override once the LTS carries that version.
SOUNDTAP_GO_VERSION := 1.26.11
# Rust 1.96.1: librespot 0.8 needs 1.85 (edition 2024), the LTS has 1.82. mtd-utils 2.3.1: our
# mkfs.ubifs/ubiformat patches are against it. expat 2.10.0: the LTS's 2.9.0 is older.
# Buildroot checks a licence file against every hash file of the package, and 1.96.1's
# LICENSE-MIT differs from the LTS's, so legal-info reads only ours.
SOUNDTAP_PACKAGE_VERSIONS := RUST_BIN_VERSION=1.96.1 MTD_VERSION=2.3.1 EXPAT_VERSION=2.10.0 \
	HOST_RUST_BIN_HASH_FILES=$(ROOT)/os/board/common/patches/rust-bin/rust-bin.hash
BUILDER_IMAGE := soundtap-builder:2026.05.1
DOCKER ?= docker
# The two inputs of a build that are not files of the tree (ADR 0011): every timestamp in
# the image is SOURCE_DATE_EPOCH, and SOUNDTAP_COMMIT is the commit it names. Both default to
# HEAD and can be set (`make os SOURCE_DATE_EPOCH=... SOUNDTAP_COMMIT=...`); outside a git
# checkout they must be, and require-build-ids refuses a build without them.
SOURCE_DATE_EPOCH ?= $(shell git -C "$(ROOT)" log -1 --format=%ct)
# The commit the image is built from, for /api/version.
SOUNDTAP_COMMIT ?= $(shell git -C "$(ROOT)" rev-parse HEAD)
HOST_OS := $(shell uname -s)

BR2_EXTERNAL ?= $(ROOT)/os
# Packages built side by side (BR2_PER_PACKAGE_DIRECTORIES in the defconfig). Each
# package still builds with its own -j (BR2_JLEVEL), so most of the gain is in the
# configure scripts and small packages that run one core at a time.
BR_JOBS ?= $(shell nproc)
BR2_DL_DIR ?= $(ROOT)/dl
BR2_CCACHE_DIR ?= $(ROOT)/.cache/buildroot-ccache
export BR2_EXTERNAL BR2_DL_DIR BR2_CCACHE_DIR

# The cross toolchain is built once per key by this Buildroot (soundtap_toolchain_defconfig)
# and kept under .cache, which the build host and CI keep between runs; soundtap_defconfig
# uses it as an external toolchain. The key is computed on the host, where git sees the tree.
SOUNDTAP_TOOLCHAIN_KEY ?= $(shell { git -C "$(ROOT)" rev-parse HEAD:buildroot; \
	cat "$(ROOT)/os/configs/soundtap_toolchain_defconfig" "$(ROOT)/os/container/Dockerfile.builder"; } \
	| sha256sum | cut -c1-16)
SOUNDTAP_TOOLCHAIN = $(ROOT)/.cache/toolchain/$(SOUNDTAP_TOOLCHAIN_KEY)
TOOLCHAIN_OUTPUT := $(ROOT)/output/toolchain
export SOUNDTAP_TOOLCHAIN
export SOURCE_DATE_EPOCH SOUNDTAP_COMMIT

# One profile for every speaker (ADR 0011).
OUTPUT := $(ROOT)/output/os
IMAGES := $(OUTPUT)/images
BUILDROOT_MAKE = $(MAKE) -C "$(BUILDROOT)" O="$(OUTPUT)" BR2_EXTERNAL="$(BR2_EXTERNAL)" GO_VERSION=$(SOUNDTAP_GO_VERSION) \
	$(SOUNDTAP_PACKAGE_VERSIONS)
IN_BUILDER = $(DOCKER) run --rm \
	--user "$$(id -u):$$(id -g)" \
	--env HOME=/tmp \
	--env FORCE_UNSAFE_CONFIGURE=1 \
	--env SOURCE_DATE_EPOCH="$(SOURCE_DATE_EPOCH)" \
	--env SOUNDTAP_COMMIT="$(SOUNDTAP_COMMIT)" \
	--env SOUNDTAP_PROFILE="$(SOUNDTAP_PROFILE)" \
	--env SOUNDTAP_TOOLCHAIN_KEY="$(SOUNDTAP_TOOLCHAIN_KEY)" \
	--volume "$(ROOT):/work" \
	--workdir /work \
	"$(BUILDER_IMAGE)"

# The frontend is built here, in the app lane's Node, and Buildroot takes the result
# (ADR 0011). Pinned by digest: the bundle the image embeds must not change underneath a
# commit. npm ci checks every package against package-lock.json's SHA-512.
NODE_IMAGE := node:22.23.3-bookworm-slim@sha256:c3de60bf2f9dd0ac6370e6117950ff62d6e339527e7472301c9c78a017978392

EXTERNAL_TREE_FILES := .gitmodules os/Config.in os/external.desc os/external.mk \
	os/board/README.md os/configs/README.md os/package/Config.in

.DEFAULT_GOAL := all

.PHONY: all lint-license-hashes help app os os-build toolchain-build stu legal-info legal-info-build sbom sbom-build source source-build configure image-check builder \
	require-build-ids repro-check repro-compare os-cves require-builder lint lint-fast lint-shell lint-python lint-c lint-actions lint-dockerfiles lint-npm-licenses lint-go-licenses lint-go-vendor lint-image-pins lint-reuse lint-buildroot web screenshots \
	check-package external-tree-check kernel-config-check test go-dist test-go test-go-race test-web test-e2e test-shell test-python test-firewall test-bco vuln dev

all: os

help:
	@printf '%s\n' \
		'SoundTap build interface' \
		'' \
		'  make / make os   Build SoundTap Core for every speaker, in Docker (output/os/images)' \
		'  make stu         Package it as the universal STU (dist/)' \
		'  make legal-info  License texts, sources and SBOM for a release (output/os)' \
		'  make sbom        Only the SBOM, after make os (output/os/sbom.cdx.json)' \
		'  make os-cves     Known CVEs of the OS packages in that SBOM (needs network)' \
		'  make repro-check Build twice from a clean tree and compare the images (hours)' \
		'  make web         Build the frontend that soundtapd embeds (output/web/dist)' \
		'  make screenshots Remake the README screenshots from that frontend (docs/assets)' \
		'  make app         Build soundtapd for the speakers, UI embedded (output/app/soundtapd)' \
		'  make dev         soundtapd on a simulated speaker plus the UI with hot reload, no speaker' \
		'                   (MODEL=st10|st20|st300|wla; CONTRIBUTING.md, Develop without a speaker)' \
		'  make source      Download every source the build needs' \
		'  make lint        Validate the repository' \
		'  make test        Run every test that needs no speaker and no built image' \
		'  make test-go     Go: gofmt, both build targets, go vet, go test (app/)' \
		'  make test-go-race  Go: go test -race (app/)' \
		'  make test-web    Frontend: lint, format, typecheck, vitest, build' \
		'  make test-e2e    Frontend end to end: Playwright against the demo speaker (docs/tools)' \
		'  make test-shell  Python and shell tests in os/tests: test-python, test-firewall' \
		'  make test-bco    C: the BCO app'"'"'s and WiLD core'"'"'s host tests, the first stage (bco/)' \
		'                   Test map: docs/contributors/testing/README.md' \
		'  make vuln        Known vulnerabilities: govulncheck (Go), npm audit (UI) (needs network)' \
		'  make builder     Build the pinned Linux host environment'

builder:
	$(DOCKER) build --tag "$(BUILDER_IMAGE)" --file "$(ROOT)/os/container/Dockerfile.builder" "$(ROOT)/os/container"

require-builder:
	@$(DOCKER) image inspect "$(BUILDER_IMAGE)" >/dev/null 2>&1 || { \
		printf 'Builder image %s is missing; run `make builder` while online first.\n' "$(BUILDER_IMAGE)" >&2; \
		exit 2; \
	}

# alpha (the default: passwordless root SSH for bring-up) or production; ADR 0011.
SOUNDTAP_PROFILE ?= alpha

# Builds the toolchain unless this key's is already there. The SDK is prepared in place,
# moved to its key's directory and relocated there; the move is the commit, so a build
# cut short leaves no half toolchain behind. Its legal-info (gcc, binutils, musl and the
# kernel headers, with their sources) goes with it for legal-info-build.
toolchain-build:
	@if [ -x "$(SOUNDTAP_TOOLCHAIN)/sdk/bin/arm-buildroot-linux-musleabihf-gcc" ]; then \
		echo "toolchain $(SOUNDTAP_TOOLCHAIN_KEY) from the cache"; \
	else \
		rm -rf "$(TOOLCHAIN_OUTPUT)" "$(SOUNDTAP_TOOLCHAIN).new" && \
		$(MAKE) -C "$(BUILDROOT)" O="$(TOOLCHAIN_OUTPUT)" BR2_EXTERNAL="$(BR2_EXTERNAL)" soundtap_toolchain_defconfig && \
		$(MAKE) -C "$(BUILDROOT)" O="$(TOOLCHAIN_OUTPUT)" BR2_EXTERNAL="$(BR2_EXTERNAL)" prepare-sdk legal-info && \
		mkdir -p "$(SOUNDTAP_TOOLCHAIN).new" && \
		mv "$(TOOLCHAIN_OUTPUT)/host" "$(SOUNDTAP_TOOLCHAIN).new/sdk" && \
		mv "$(TOOLCHAIN_OUTPUT)/legal-info" "$(SOUNDTAP_TOOLCHAIN).new/legal-info" && \
		rm -rf "$(SOUNDTAP_TOOLCHAIN)" && mv "$(SOUNDTAP_TOOLCHAIN).new" "$(SOUNDTAP_TOOLCHAIN)" && \
		"$(SOUNDTAP_TOOLCHAIN)/sdk/relocate-sdk.sh" >/dev/null && \
		rm -rf "$(TOOLCHAIN_OUTPUT)" && echo "toolchain $(SOUNDTAP_TOOLCHAIN_KEY) built"; \
	fi

configure: toolchain-build
	@$(BUILDROOT_MAKE) soundtap_defconfig
	@case "$(SOUNDTAP_PROFILE)" in \
	alpha) ;; \
	production) \
		"$(BUILDROOT)/support/kconfig/merge_config.sh" -m -O "$(OUTPUT)" \
			"$(OUTPUT)/.config" "$(ROOT)/os/configs/production.fragment" && \
		$(BUILDROOT_MAKE) olddefconfig ;; \
	*) echo "SOUNDTAP_PROFILE must be alpha or production" >&2; exit 2 ;; \
	esac

source: builder
	$(IN_BUILDER) make source-build

source-build: configure
	@$(BUILDROOT_MAKE) source

# A copy of app/frontend, so npm ci's node_modules never lands in the source tree.
web:
	$(DOCKER) run --rm --user "$$(id -u):$$(id -g)" --env HOME=/tmp \
		--env npm_config_cache=/work/.cache/npm --env SOURCE_DATE_EPOCH="$(SOURCE_DATE_EPOCH)" \
		--volume "$(ROOT):/work" --workdir /tmp "$(NODE_IMAGE)" sh -euc '\
		cp -a /work/app/frontend /tmp/frontend && cd /tmp/frontend && rm -rf node_modules && \
		npm ci --ignore-scripts --no-audit --no-fund && \
		SOUNDTAP_WEB_OUT_DIR=/work/output/web/dist npm run build'

# The README screenshots and art in docs/assets, remade at every release (RELEASING.md,
# Steps): the web UI this tree builds, served with a fake speaker and photographed in a
# pinned Chromium (docs/tools). docs/assets/screenshots.source records the UI and tools it
# was made from; release.yml and dry-run.sh refuse a commit whose UI differs. Playwright's
# npm package comes from the network.
PLAYWRIGHT_IMAGE := mcr.microsoft.com/playwright:v1.64.1-noble@sha256:2f05f8a734509a7b3f26a912639488ee08f58a7b292d14d62271bcff9038768f
SCREENSHOTS_RUN = $(DOCKER) run --rm --init --ipc=host --user "$$(id -u):$$(id -g)" --env HOME=/tmp \
	--volume "$(ROOT)/docs/tools:/tools:ro" --volume "$(ROOT)/output/web/dist:/dist:ro" \
	--volume "$(ROOT)/output/screenshots:/shots" --volume "$(ROOT)/docs/assets:/out" \
	"$(PLAYWRIGHT_IMAGE)" sh /tools/in-container.sh
screenshots:
	@python3 "$(ROOT)/tools/release/screenshots.py" ready
	$(MAKE) web
	rm -rf "$(ROOT)/output/screenshots" && mkdir -p "$(ROOT)/output/screenshots"
	$(SCREENSHOTS_RUN) shoot.mjs
	$(SCREENSHOTS_RUN) compose.mjs
	@python3 "$(ROOT)/tools/release/screenshots.py" record

# The end-to-end tests (app/frontend/e2e): the web UI this tree builds (`make web`) in the
# screenshots' pinned Chromium, against the demo speaker in docs/tools: Wi-Fi setup, the
# speaker going away and coming back, switching speakers, a volume slider. @playwright/test
# comes from the network, at the image's version.
test-e2e: web
	$(DOCKER) run --rm --init --ipc=host --user "$$(id -u):$$(id -g)" --env HOME=/tmp \
		--env npm_config_update_notifier=false \
		--volume "$(ROOT)/app/frontend/e2e:/e2e:ro" --volume "$(ROOT)/docs/tools:/tools:ro" \
		--volume "$(ROOT)/output/web/dist:/dist:ro" "$(PLAYWRIGHT_IMAGE)" sh /e2e/in-container.sh

# The app alone, for POST /api/soundtapd (tools/hil/install-app.sh): a minute instead of
# a Core build when only soundtapd changed.
app: web
	$(DOCKER) run --rm --user "$$(id -u):$$(id -g)" --env HOME=/tmp \
		--env GOCACHE=/work/.cache/go-build --env GOMODCACHE=/work/.cache/gomod \
		--env CGO_ENABLED=0 --env GOOS=linux --env GOARCH=arm --env GOARM=7 \
		--volume "$(ROOT):/work" --volume "$(ROOT)/output/web/dist:/work/app/internal/web/dist:ro" \
		--workdir /work/app $(GO_IMAGE) \
		go build -trimpath -ldflags "-s -w -X main.version=dev-$$(git -C "$(ROOT)" rev-parse --short=7 HEAD)" \
		-o /work/output/app/soundtapd ./cmd/soundtapd

# The developer loop (CONTRIBUTING.md): soundtapd -dev on the simulated speaker at
# 127.0.0.1:8080, home in output/dev-home, and Vite with hot reload at localhost:5173. The
# host's Go and Node when they are new enough, else the pinned images below. No audio, no
# hardware, nothing announced on the LAN. Ctrl-C stops both.
dev:
	@MODEL="$(MODEL)" GO_IMAGE="$(GO_IMAGE)" NODE_IMAGE="$(NODE_IMAGE)" DOCKER="$(DOCKER)" "$(ROOT)/tools/dev/dev.sh"

# Without these, mkimage and the rootfs fall back to the clock and the image to no commit.
require-build-ids:
	@case "$(SOURCE_DATE_EPOCH)" in ''|*[!0-9]*) \
		echo "SOURCE_DATE_EPOCH must be seconds since 1970 (got '$(SOURCE_DATE_EPOCH)'); outside git, set it" >&2; \
		exit 2 ;; esac
	@test -n "$(SOUNDTAP_COMMIT)" || { echo "SOUNDTAP_COMMIT is empty; outside git, set it" >&2; exit 2; }

os: require-build-ids builder web
	$(IN_BUILDER) make os-build

# One image for every speaker; os/board/image/README.md.
os-build: require-build-ids configure
	@"$(ROOT)/os/board/rootfs/prepare-linux.sh" "$(ROOT)" "$(OUTPUT)" prepare
	@$(BUILDROOT_MAKE) -j$(BR_JOBS)
	@$(MAKE) image-check
	@"$(ROOT)/os/board/rootfs/prepare-linux.sh" "$(ROOT)" "$(OUTPUT)" record

# Reproducibility (ADR 0011): two builds of this tree, each from an empty output/os, with the
# same SOURCE_DATE_EPOCH and SOUNDTAP_COMMIT, must give the same SHA256SUMS and STU. Takes two
# full builds. REPRO_FRESH_CCACHE=1 gives the second build an empty compiler cache, so a
# cached object cannot hide a compiler that differs. Saved under output/repro/{a,b}.
repro-check: require-build-ids
	@SOURCE_DATE_EPOCH="$(SOURCE_DATE_EPOCH)" SOUNDTAP_COMMIT="$(SOUNDTAP_COMMIT)" \
		sh "$(ROOT)/os/support/repro-check.sh" "$(ROOT)"

# Two builds saved elsewhere: make repro-compare A=dir B=dir (repro_compare.py save).
repro-compare:
	@python3 "$(ROOT)/os/support/repro_compare.py" compare "$(A)" "$(B)"

# What a release owes its users besides the image (GPL and friends): every package's
# license texts and exact source, and a CycloneDX SBOM. Run after `make os`.
legal-info: builder
	$(IN_BUILDER) make legal-info-build

legal-info-build: configure
	@$(BUILDROOT_MAKE) legal-info
	@rm -rf "$(OUTPUT)/legal-info/toolchain" && cp -a "$(SOUNDTAP_TOOLCHAIN)/legal-info" "$(OUTPUT)/legal-info/toolchain"
	@cd "$(OUTPUT)/legal-info" && for d in host-sources/soundtap-bco-app-*/soundtap-inputs/bco/wild/fw/src \
		host-sources/soundtap-bootloaders-*/soundtap-inputs/os/bootloader/uboot \
		sources/linux-*/soundtap-inputs/os/drivers \
		sources/soundtapd-*/soundtap-inputs/app/cmd/soundtapd \
		sources/soundtapd-*/soundtap-inputs/app/frontend/src \
		sources/soundtap-runtime-*/soundtap-inputs/os/overlay \
		sources/soundtap-aic3256-loader-*/soundtap-inputs/os/drivers; do \
		test -d $$d || { echo "legal-info lacks $$d" >&2; exit 1; }; done
	@# NOTICE.md names every target package; this project's own ones are covered there by path.
	@cut -d, -f1 "$(OUTPUT)/legal-info/manifest.csv" | tr -d '"' | sed 1d \
		| grep -v -x -e 'soundtapd' -e 'soundtap-runtime' -e 'soundtap-aic3256-loader' -e 'skeleton.*' -e 'toolchain.*' \
		| while read -r p; do grep -q "^| $$p |" NOTICE.md || { echo "NOTICE.md lacks $$p" >&2; exit 1; }; done
	@$(MAKE) --no-print-directory sbom-build

# The SBOM alone, from the configured tree, for `make os-cves` on every build: legal-info also
# copies every source, which only a release needs.
sbom: builder
	$(IN_BUILDER) make sbom-build

sbom-build: configure
	@$(BUILDROOT_MAKE) -s --no-print-directory show-info | "$(BUILDROOT)/utils/generate-cyclonedx" >"$(OUTPUT)/sbom.cdx.json"

# The SBOM's packages against NVD; fails on an open CVE scoring 7.0 or more.
# SBOM= takes another one, such as a release candidate's SoundTap-Core-X.Y.Z.cdx.json.
SBOM ?= $(OUTPUT)/sbom.cdx.json
os-cves:
	@$(DOCKER) run --rm --env NVD_API_KEY -v "$(ROOT)/tools/release:/r:ro" -v "$(abspath $(SBOM)):/sbom.json:ro" \
		$(PYTHON_IMAGE) python3 -I /r/os_cves.py /sbom.json

# The universal package from the image, through the packager and its install dry runs.
stu:
	SOUNDTAP_RHINO_KERNEL="$(IMAGES)/uImage-st10" \
	SOUNDTAP_RHINO_ROOTFS="$(IMAGES)/rootfs.ubi" \
	SOUNDTAP_SPOTTY_KERNEL="$(IMAGES)/uImage-st20" \
	SOUNDTAP_SPOTTY_MLO="$(IMAGES)/st20-spl/MLO" \
	SOUNDTAP_BINKY_MLO="$(IMAGES)/wla-spl/MLO" \
	SOUNDTAP_BINKY_UBOOT="$(IMAGES)/wla-uboot/u-boot.img" \
	SOUNDTAP_STU_OUT="$(ROOT)/dist/Update-soundtap-buildroot.stu" \
	SOUNDTAP_STU_TARGET=soundtap-tri \
	sh "$(ROOT)/os/installer/build-universal-stu.sh"

image-check:
	@cd "$(IMAGES)" && sha256sum -c SHA256SUMS
	@python3 "$(ROOT)/os/support/kernel_config_check.py" "$(IMAGES)/kernel.config"
	@python3 "$(ROOT)/os/support/ignore_cves.py" --kernel-config "$(IMAGES)/kernel.config"
	@python3 "$(ROOT)/os/support/rootfs_check.py" --profile "$(SOUNDTAP_PROFILE)" --accounts "$(IMAGES)/rootfs-tree" "$(OUTPUT)/target"
	@python3 "$(ROOT)/os/support/tool_commands.py" "$(OUTPUT)/target"
	@python3 "$(ROOT)/os/support/check_provenance.py" --rootfs "$(OUTPUT)/target"

LINT_FAST := lint-shell lint-go lint-python lint-c lint-actions lint-dockerfiles lint-npm-licenses lint-go-licenses lint-go-vendor lint-license-hashes lint-image-pins lint-reuse kernel-config-check
lint: $(LINT_FAST) external-tree-check

# Everything lint runs except the checks that need the Buildroot submodule, so a fresh
# worktree can run it before a merge.
lint-fast: $(LINT_FAST)

kernel-config-check:
	@python3 "$(ROOT)/os/support/kernel_config_check.py" --fragment "$(ROOT)/os/board/image/linux.fragment"

# The tests that need no speaker and no built image. tools/ci/run-ci.sh runs each as one CI
# step (docs/contributors/testing/README.md); CI's jobs call the same script. Images are overridable for a
# machine that lacks the pinned ones.
GO_IMAGE := golang:$(SOUNDTAP_GO_VERSION)-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0
GOVULNCHECK_VERSION := v1.9.0
test: test-go test-go-race test-web test-e2e test-shell test-bco

# The whole repository is mounted: Go tests read fixtures outside app/. The embedded UI
# needs one file in dist to compile; the real one comes from `make web`.
GO_RUN = docker run --rm -e GOFLAGS=-buildvcs=false -v "$(ROOT):/w" -w /w/app -v soundtap-gomod:/go/pkg/mod -v soundtap-gocache:/root/.cache $(GO_IMAGE)
go-dist:
	@mkdir -p "$(ROOT)/app/internal/web/dist" && touch "$(ROOT)/app/internal/web/dist/.keep"

test-go: go-dist
	@$(GO_RUN) sh -c 'unformatted=$$(gofmt -l ./cmd ./internal ./oled) && \
		{ test -z "$$unformatted" || { echo "gofmt needed: $$unformatted"; exit 1; }; } && \
		GOOS=linux GOARCH=arm GOARM=7 go build ./... && GOOS=darwin GOARCH=arm64 go build ./... && \
		go vet ./... && go test -shuffle=on -timeout 300s ./...'

# internal/web alone needs 130-290 s under -race on a shared runner.
test-go-race: go-dist
	@$(GO_RUN) go test -race -shuffle=on -timeout 600s ./...

# In a copy of app/frontend, as `make web` does, so node_modules never lands in the tree.
# typecheck regenerates the API types from ../internal/web/openapi.json, so that comes along.
test-web:
	@$(DOCKER) run --rm --user "$$(id -u):$$(id -g)" --env HOME=/tmp --env npm_config_cache=/work/.cache/npm \
		--env npm_config_update_notifier=false --volume "$(ROOT):/work" --workdir /tmp "$(NODE_IMAGE)" sh -euc '\
		cp -a /work/app/frontend /tmp/frontend && cd /tmp/frontend && rm -rf node_modules && \
		mkdir -p /tmp/internal/web && cp /work/app/internal/web/openapi.json /tmp/internal/web/ && \
		npm ci --ignore-scripts --no-audit --no-fund && npm run lint && npm run format:check && \
		npm run typecheck && npm test && npm run build'

test-shell: test-python test-firewall

# unittest finds each test_*.py under os/tests. The Go image because it already carries
# python3, git and cc, which the tests call; the git directory is mounted so a worktree works.
# --init reaps the stubs a test leaves behind, or they keep the captured pipes open.
test-python:
	@docker run --rm --init --user "$$(id -u):$$(id -g)" --env HOME=/tmp -v "$(ROOT):$(ROOT)" -v "$(GIT_COMMON):$(GIT_COMMON):ro" \
		-e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0='*' \
		-w "$(ROOT)" $(GO_IMAGE) python3 -B -m unittest discover -s os/tests -p 'test_*.py'

# The production input filter, loaded in a network namespace (hence --privileged): the setup
# AP's DHCP and DNS must pass, a closed port must not, and SSDP only from the speaker's own
# subnet. A static read of firewall.nft cannot show what the kernel actually accepts.
test-firewall:
	@docker run --rm --privileged -v "$(ROOT):/w" $(ALPINE_IMAGE) sh /w/os/tests/wifi/firewall_setup_ap_test.sh

# The BCO (ADR 0031): every bco/app/tests/*_test.c and every bco/wild/fw/tests/*_test.c (our
# WiLD core, under ASan and UBSan, with its air-frame fuzz), then our first stage built with the
# pinned arm-none-eabi toolchain and run under Unicorn, which must bring up the clock and the
# SDRAM exactly as bco/app/first-init-trace.json records. No QEMU, no Bose bytes, no hardware.
test-bco:
	@sh "$(ROOT)/bco/app/tests/run-all.sh"
	@sh "$(ROOT)/bco/wild/fw/tests/run-all.sh"
	@$(DOCKER) build -q -t soundtap-bco-toolchain "$(ROOT)/bco/app" >/dev/null
	@$(DOCKER) run --rm --user "$$(id -u):$$(id -g)" -v "$(ROOT)/bco/app:/src:ro" soundtap-bco-toolchain sh -euc '\
		flags="-mcpu=arm926ej-s -marm -Os -ffreestanding -fno-unwind-tables -fno-asynchronous-unwind-tables -nostdlib" && \
		arm-none-eabi-gcc $$flags -Wl,-T,/src/first.ld /src/src/start.S /src/src/first.c -o /tmp/first.elf && \
		arm-none-eabi-objcopy -O binary /tmp/first.elf /tmp/first.bin && \
		python3 /src/check-first-init.py /tmp/first.bin'

# Known vulnerabilities in what ships, against today's databases, so a commit that passed
# yesterday can fail today: govulncheck reports the vulnerable code soundtapd actually calls,
# npm audit the UI's runtime dependencies (build tools such as vite never reach a speaker).
vuln: go-dist
	@$(GO_RUN) go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
	@$(DOCKER) run --rm --user "$$(id -u):$$(id -g)" --env HOME=/tmp --env npm_config_cache=/tmp/npm \
		--env npm_config_update_notifier=false --volume "$(ROOT)/app/frontend:/src:ro" --workdir /tmp "$(NODE_IMAGE)" sh -euc '\
		cp /src/package.json /src/package-lock.json /tmp/ && npm audit --omit=dev --audit-level=high'

# Lint tools run in containers, pinned: the runners have neither installed.
SHELLCHECK_IMAGE := koalaman/shellcheck:v0.11.0@sha256:bb596a0d169b85ddd81d8b6d3a2ff6d5baf5fca10b97f575ebc647c3dff62b3d
PYTHON_IMAGE := python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
GOLANGCI_IMAGE := golangci/golangci-lint:v2.15.0@sha256:fbfcfd12a6f8d7f1b97b3362ca80fc2a244d46863148c73cb88e7793af96c481
GIT_COMMON = $(shell git -C "$(ROOT)" rev-parse --path-format=absolute --git-common-dir)
RUFF_VERSION := 0.13.3
ZIZMOR_VERSION := 1.32.0
ALPINE_IMAGE := alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
CPPCHECK_VERSION := 2.14.0-r0
ACTIONLINT_IMAGE := rhysd/actionlint:1.7.7@sha256:887a259a5a534f3c4f36cb02dca341673c6089431057242cdc931e9f133147e9
HADOLINT_IMAGE := hadolint/hadolint:v2.12.0@sha256:30a8fd2e785ab6176eed53f74769e04f125afb2f74a6c52aef7d463583b6d45e
REUSE_IMAGE := fsfe/reuse:6.3.0@sha256:2748d04c2adeb747c9a44831bc99a9cbfd7bf7ff6fc371a4b63204944e3e47b6

# A syntax error in os/overlay/etc/init.d/soundtap does not fail a build -- it ends the
# boot, on the speaker, with no console. That has happened; it should not need to happen
# twice. tools/ci/shell_scripts.py lists every tracked shell script (*.sh, plus any
# file with a sh shebang: /init, init.d and udhcpc hooks carry no extension), NUL-separated.
# bash -n runs in the pinned Go image, so a macOS host's bash 3.2 does not judge the scripts.
SHELL_SCRIPTS = python3 "$(ROOT)/tools/ci/shell_scripts.py" "$(ROOT)"

lint-shell:
	@$(SHELL_SCRIPTS) | docker run --rm -i -v "$(ROOT):/w:ro" -w /w $(GO_IMAGE) xargs -0 -n1 bash -n
	@# The speaker runs these under BusyBox ash, so shellcheck reads each by its shebang
	@# (sh = POSIX, which catches bashisms bash -n lets through). Every check is on, the
	@# optional ones too, down to style; it follows sourced files (-x) next to the script.
	@# SC1007 is the deliberate `CDPATH= cd` idiom; any other exception is an inline
	@# directive with its reason. gcc format: the image has no locale to print the
	@# scripts' non-ASCII comments in.
	@$(SHELL_SCRIPTS) | xargs -0 docker run --rm -v "$(ROOT):/mnt" -w /mnt \
		$(SHELLCHECK_IMAGE) -S style --enable=all -x -P SCRIPTDIR -e SC1007 -f gcc

# lint-go judges the whole Go module with every linter of app/.golangci.yml, not only what
# changed. The repository sits at its own path so the config's paths resolve as on the host.
lint-go:
	@mkdir -p "$(ROOT)/app/internal/web/dist" && touch "$(ROOT)/app/internal/web/dist/.lint"
	@status=0; docker run --rm -v "$(ROOT):$(ROOT)" -w "$(ROOT)/app" \
		-v soundtap-gomod:/go/pkg/mod -v soundtap-golangci-cache:/root/.cache \
		$(GOLANGCI_IMAGE) golangci-lint run ./... || status=$$?; \
		rm -f "$(ROOT)/app/internal/web/dist/.lint"; exit $$status

lint-python:
	@docker run --rm -v "$(ROOT):/w" -w /w $(PYTHON_IMAGE) \
		sh -c 'pip install -q --disable-pip-version-check --root-user-action=ignore ruff==$(RUFF_VERSION) && ruff check -q --no-cache .'

# Firmware and host tools, every check on: style, performance, portability, information and
# unusedFunction, inconclusive ones too, at the exhaustive check level, and the BCO headers on
# the include path. A finding the code is right about carries an inline cppcheck-suppress with
# its reason. Suppressed here on purpose: asserts with side effects (the tests never build with
# NDEBUG); subtractPointers on the linker's start and end symbols of a blob; system headers,
# which the container does not have; the active-checker count, which is no finding; in the
# U-Boot sources, TweetNaCl as fetched (variable scope) and soundtap-sig.c, whose functions
# U-Boot's patched code calls and whose bytes are the bootloader an update writes to mtd0-mtd4;
# and four style notes in the codec loader, whose sources os/package/soundtap-aic3256-loader
# pins by hash.
lint-c:
	@git -C "$(ROOT)" ls-files -z '*.c' ':(exclude)app/vendor/**' | xargs -0 docker run --rm -v "$(ROOT):/w" -w /w $(ALPINE_IMAGE) \
		sh -c 'apk add -q --no-progress cppcheck=$(CPPCHECK_VERSION) >/dev/null && \
		cppcheck -q --enable=all --inconclusive --check-level=exhaustive --inline-suppr --error-exitcode=1 \
		-I bco/app/src -I bco/wild/fw/src \
		--suppress=assertWithSideEffect --suppress=subtractPointers \
		--suppress=missingIncludeSystem --suppress=checkersReport \
		--suppress=variableScope:os/bootloader/uboot/tweetnacl.c \
		--suppress=variableScope:os/bootloader/uboot/soundtap-sig.c \
		--suppress=unusedFunction:os/bootloader/uboot/soundtap-sig.c \
		--suppress=constVariablePointer:os/drivers/aic3256-bitbang.c:22 \
		--suppress=constParameterPointer:os/drivers/aic3256-bitbang.c:98 \
		--suppress=variableScope:os/drivers/aic3256-loader.c:40 \
		--suppress=variableScope:os/drivers/aic3256-loader.c:61 "$$@"' cppcheck

# actionlint checks the workflows' syntax, expressions and run scripts (SC2086 and SC2016
# fire on deliberately empty arguments and on sed patterns); zizmor their security: pinned
# actions (.github/zizmor.yml), credentials left behind, injection, permissions. Offline:
# no GitHub token, the same result everywhere.
lint-actions:
	@docker run --rm -v "$(ROOT):/w" -w /w $(ACTIONLINT_IMAGE) -color=false -ignore SC2086 -ignore SC2016
	@docker run --rm -v "$(ROOT):/w:ro" -w /w $(PYTHON_IMAGE) \
		sh -c 'pip install -q --disable-pip-version-check --root-user-action=ignore zizmor==$(ZIZMOR_VERSION) && \
		zizmor -q --offline --no-progress --config .github/zizmor.yml .github'

lint-dockerfiles:
	@git -C "$(ROOT)" ls-files -z '*Dockerfile*' | xargs -0 docker run --rm -v "$(ROOT):/w" -w /w \
		--entrypoint hadolint $(HADOLINT_IMAGE) --no-color

lint-npm-licenses:
	@python3 "$(ROOT)/tools/ci/npm_licenses.py" --check \
		--lock "$(ROOT)/app/frontend/package-lock.json" \
		--licenses "$(ROOT)/app/frontend/THIRD_PARTY_LICENSES.txt"

# ADR 0124: every Go module's licence file against NOTICE.md. The download fills the shared
# module cache (go list -m all fetches the go.mod files of the whole module graph, which the
# check lists); the check itself runs offline.
lint-go-licenses:
	@docker run --rm -v "$(ROOT):$(ROOT)" -w "$(ROOT)/app" -v soundtap-gomod:/go/pkg/mod \
		$(GO_IMAGE) sh -c 'go mod download && go list -mod=readonly -m all >/dev/null && \
		python3 "$(ROOT)/tools/ci/go_licenses.py" --module-dir . --notice "$(ROOT)/NOTICE.md"'

# app/vendor is what `go mod vendor` makes of go.mod and go.sum, byte for byte (ADR 0124): the
# Buildroot build compiles it offline (-mod=vendor), so an edit there would ship unnoticed.
# go mod verify holds the module cache to go.sum's hashes first.
lint-go-vendor:
	@$(GO_RUN) sh -c 'go mod verify && rm -rf /tmp/vendor && go mod vendor -o /tmp/vendor && diff -r /tmp/vendor vendor'

# REUSE (https://reuse.software, ADR 0124): every file names its licence and copyright, in an
# SPDX header or in REUSE.toml, and every licence named has its text in LICENSES/. reuse asks
# git which files are ignored, so the git directory is mounted where a worktree's .git points.
lint-reuse:
	@docker run --rm -v "$(ROOT):$(ROOT):ro" -v "$(GIT_COMMON):$(GIT_COMMON):ro" -w "$(ROOT)" \
		-e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory -e GIT_CONFIG_VALUE_0='*' \
		$(REUSE_IMAGE) --root "$(ROOT)" lint --lines

lint-license-hashes:
	@python3 "$(ROOT)/tools/ci/license_hashes.py"

# Every image a target, script or workflow pulls is pinned by digest (ADR 0011).
lint-image-pins:
	@python3 "$(ROOT)/tools/ci/image_pins.py" "$(ROOT)"

lint-buildroot:
	@test -f "$(BUILDROOT)/Makefile" || { \
		printf '%s\n' 'Buildroot is missing; run git submodule update --init --recursive' >&2; \
		exit 1; \
	}
	@test "$$(git -C "$(BUILDROOT)" rev-parse HEAD)" = "$(BUILDROOT_COMMIT)" || { \
		printf 'Buildroot must be pinned to %s\n' "$(BUILDROOT_COMMIT)" >&2; \
		exit 1; \
	}
	@$(MAKE) -s -C "$(BUILDROOT)" BR2_EXTERNAL="$(BR2_EXTERNAL)" help >/dev/null

check-package: lint-buildroot require-builder
	@$(DOCKER) run --rm --network none --volume "$(ROOT):/work:ro" \
		--workdir /work "$(BUILDER_IMAGE)" \
		/bin/bash -euc 'cp -a os/package/soundtap-librespot \
				os/package/soundtap-shairport-sync /tmp/; \
			chmod -R a-x /tmp/soundtap-librespot /tmp/soundtap-shairport-sync; \
			buildroot/utils/check-package --br2-external \
			/tmp/soundtap-librespot/Config.in \
			/tmp/soundtap-librespot/soundtap-librespot.mk \
			/tmp/soundtap-librespot/soundtap-librespot.hash \
			/tmp/soundtap-librespot/*.patch \
			/tmp/soundtap-shairport-sync/Config.in \
			/tmp/soundtap-shairport-sync/soundtap-shairport-sync.mk \
			/tmp/soundtap-shairport-sync/soundtap-shairport-sync.hash'

# The files that make os/ a Buildroot external tree are there, and the pinned Buildroot
# submodule carries no local changes.
external-tree-check: lint-buildroot
	@for file in $(EXTERNAL_TREE_FILES); do \
		test -f "$(ROOT)/$$file" || { printf 'missing Buildroot external tree file: %s\n' "$$file" >&2; exit 1; }; \
	done
	@test "$$(git -C "$(BUILDROOT)" status --short)" = '' || { \
		printf '%s\n' 'Buildroot submodule has local modifications' >&2; \
		exit 1; \
	}

