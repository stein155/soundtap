################################################################################
#
# soundtap-shairport-sync
#
################################################################################

# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

SOUNDTAP_SHAIRPORT_SYNC_VERSION = 5.6
SOUNDTAP_SHAIRPORT_SYNC_SITE = $(call github,mikebrady,shairport-sync,$(SOUNDTAP_SHAIRPORT_SYNC_VERSION))
SOUNDTAP_SHAIRPORT_SYNC_LICENSE = MIT, BSD-3-Clause
SOUNDTAP_SHAIRPORT_SYNC_LICENSE_FILES = LICENSES
SOUNDTAP_SHAIRPORT_SYNC_DEPENDENCIES = \
	alsa-lib \
	avahi \
	soundtap-ffmpeg \
	host-pkgconf \
	host-soundtap-plistutil \
	host-vim \
	libconfig \
	libgcrypt \
	libplist \
	libsodium \
	libsoxr \
	soundtap-nqptp \
	openssl \
	popt \
	util-linux

# GitHub release archives do not contain a generated configure script.
SOUNDTAP_SHAIRPORT_SYNC_AUTORECONF = YES
SOUNDTAP_SHAIRPORT_SYNC_CONF_OPTS = \
	--sysconfdir=/etc \
	--with-airplay-2 \
	--with-alsa \
	--with-avahi \
	--with-metadata \
	--with-metadata-pipe \
	--with-soxr \
	--with-ssl=openssl \
	--with-stdout \
	--without-convolution \
	--without-dbus-interface \
	--without-libdaemon \
	--without-mpris-interface \
	--without-mqtt-client \
	--without-tinysvcmdns

define SOUNDTAP_SHAIRPORT_SYNC_INSTALL_TARGET_CMDS
	$(INSTALL) -D -m 0755 $(@D)/shairport-sync \
		$(TARGET_DIR)/usr/bin/shairport-sync
	$(INSTALL) -D -m 0644 $(@D)/scripts/shairport-sync.conf \
		$(TARGET_DIR)/etc/shairport-sync.conf
endef

$(eval $(autotools-package))
