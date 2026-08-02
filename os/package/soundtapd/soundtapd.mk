################################################################################
#
# soundtapd
#
################################################################################

# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

SOUNDTAPD_VERSION = 1
SOUNDTAPD_SITE = $(BR2_EXTERNAL_SOUNDTAP_PATH)/../app
SOUNDTAPD_SITE_METHOD = local
# The Go modules soundtapd imports are vendored in app/vendor (ADR 0124): Buildroot's Go
# builds with -mod=vendor and GOPROXY=off, and a local package has no download step that
# could fetch them. `make lint-go-vendor` holds app/vendor to go.mod and go.sum, and their
# licences are NOTICE.md's Go module table.
SOUNDTAPD_LICENSE = GPL-3.0-only, EPL-2.0 or BSD-3-Clause (Paho), BSD-2-Clause, BSD-3-Clause
SOUNDTAPD_LICENSE_FILES = LICENSE frontend/THIRD_PARTY_LICENSES.txt \
	vendor/github.com/eclipse/paho.mqtt.golang/LICENSE \
	vendor/github.com/eclipse/paho.mqtt.golang/edl-v10 \
	vendor/github.com/godbus/dbus/v5/LICENSE \
	vendor/github.com/gorilla/websocket/LICENSE \
	vendor/golang.org/x/net/LICENSE \
	vendor/golang.org/x/sync/LICENSE
# Each target names its binary after itself, so no BIN_NAME: golang-package refuses one
# alongside a second target. soundtap-root is the root helper init starts beside
# soundtapd, which runs as its own user (ADR 0066).
SOUNDTAPD_BUILD_TARGETS = ./cmd/soundtapd ./cmd/soundtap-root $(if $(BR2_PACKAGE_SOUNDTAPD_BCOFLASH),./cmd/bcoflash)
# golang-package installs only the binary named after the package unless told otherwise.
SOUNDTAPD_INSTALL_BINS = soundtapd soundtap-root
SOUNDTAPD_GOMOD = github.com/stein155/soundtap/app
SOUNDTAPD_GO_ENV = CGO_ENABLED=0
# The app in an OS image is the app release of the same commit (ADR 0035), and says so.
SOUNDTAPD_LDFLAGS = -X main.version=app-v$(strip $(file < $(BR2_EXTERNAL_SOUNDTAP_PATH)/../app/VERSION))
SOUNDTAPD_OVERRIDE_SRCDIR_RSYNC_EXCLUSIONS = \
	--delete \
	--exclude .stamp_* \
	--include frontend/THIRD_PARTY_LICENSES.txt \
	--exclude 'frontend/*' \
	--exclude internal/web/dist \
	--exclude node_modules

# The frontend comes built from the app lane (ADR 0011): `make web` runs npm ci and the
# build in a pinned Node image and leaves it in SOUNDTAP_WEB_DIST, outside Buildroot.
SOUNDTAP_WEB_DIST ?= $(BR2_EXTERNAL_SOUNDTAP_PATH)/../output/web/dist
define SOUNDTAPD_INSTALL_WEB
	test -f $(SOUNDTAP_WEB_DIST)/index.html || { \
		echo "soundtapd: no frontend in $(SOUNDTAP_WEB_DIST); run make web" >&2; exit 1; }
	rm -rf $(@D)/internal/web/dist
	mkdir -p $(@D)/internal/web/dist
	cp -a $(SOUNDTAP_WEB_DIST)/. $(@D)/internal/web/dist/
endef
SOUNDTAPD_PRE_BUILD_HOOKS += SOUNDTAPD_INSTALL_WEB

# ADR 0035: the level of what this OS provides to the app, from the same file the app reads.
define SOUNDTAPD_INSTALL_OS_API
	$(INSTALL) -D -m 0644 $(@D)/internal/osapi/level $(TARGET_DIR)/etc/soundtap/os-api
endef
SOUNDTAPD_POST_INSTALL_TARGET_HOOKS += SOUNDTAPD_INSTALL_OS_API

$(eval $(golang-package))

SOUNDTAPD_SOURCE_DIR = $(if $(strip $(SOUNDTAPD_OVERRIDE_SRCDIR)),$(strip $(SOUNDTAPD_OVERRIDE_SRCDIR)),$(SOUNDTAPD_SITE))
SOUNDTAPD_SOURCE_STAMP = $(BUILD_DIR)/.soundtap-source-digests/soundtapd
SOUNDTAPD_DIGEST_CMD = python3 \
	$(BR2_EXTERNAL_SOUNDTAP_PATH)/support/content_digest.py \
	"$(SOUNDTAPD_SOURCE_DIR)" \
	--exclude frontend --exclude internal/web/dist --exclude node_modules
SOUNDTAPD_SOURCE_DIGEST := $(shell $(SOUNDTAPD_DIGEST_CMD) \
	--stamp "$(SOUNDTAPD_SOURCE_STAMP)" \
	|| printf '%s' __SOUNDTAPD_DIGEST_ERROR__)
ifneq ($(filter __SOUNDTAPD_DIGEST_ERROR__,$(SOUNDTAPD_SOURCE_DIGEST)),)
$(error failed to calculate soundtapd source digest)
endif

$(SOUNDTAPD_SOURCE_STAMP):
	$(SOUNDTAPD_DIGEST_CMD) --stamp "$@" >/dev/null

$(SOUNDTAPD_TARGET_RSYNC): $(SOUNDTAPD_SOURCE_STAMP)

# A new frontend rebuilds soundtapd, which embeds it. Not an error here when the frontend
# is missing: `make source` and defconfig do not need it, and the build hook above says so.
SOUNDTAPD_WEB_STAMP = $(BUILD_DIR)/.soundtap-source-digests/web-dist
SOUNDTAPD_WEB_DIGEST_CMD = python3 \
	$(BR2_EXTERNAL_SOUNDTAP_PATH)/support/content_digest.py "$(SOUNDTAP_WEB_DIST)"
SOUNDTAPD_WEB_DIGEST := $(shell $(SOUNDTAPD_WEB_DIGEST_CMD) --stamp "$(SOUNDTAPD_WEB_STAMP)" 2>/dev/null)

$(SOUNDTAPD_WEB_STAMP):
	$(SOUNDTAPD_WEB_DIGEST_CMD) --stamp "$@" >/dev/null

$(SOUNDTAPD_TARGET_BUILD): $(SOUNDTAPD_SOURCE_STAMP) $(SOUNDTAPD_WEB_STAMP)
