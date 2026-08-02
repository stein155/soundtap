################################################################################
#
# soundtap-snapcast
#
################################################################################

# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

SOUNDTAP_SNAPCAST_VERSION = 0.35.0
SOUNDTAP_SNAPCAST_SITE = $(call github,badaix,snapcast,v$(SOUNDTAP_SNAPCAST_VERSION))
SOUNDTAP_SNAPCAST_LICENSE = GPL-3.0-or-later, MIT (bundled headers)
SOUNDTAP_SNAPCAST_LICENSE_FILES = LICENSE common/aixlog.hpp common/json.hpp common/popl.hpp
SOUNDTAP_SNAPCAST_DEPENDENCIES = \
	alsa-lib \
	avahi \
	boost \
	expat \
	host-pkgconf \
	openssl \
	libsoxr

SOUNDTAP_SNAPCAST_CONF_OPTS = \
	-DBUILD_CLIENT=ON \
	-DBUILD_SERVER=ON \
	-DBUILD_TESTS=OFF \
	-DBUILD_WITH_ALSA=ON \
	-DBUILD_WITH_AVAHI=ON \
	-DBUILD_WITH_EXPAT=ON \
	-DBUILD_WITH_FLAC=OFF \
	-DBUILD_WITH_JACK=OFF \
	-DBUILD_WITH_OPUS=OFF \
	-DBUILD_WITH_PIPEWIRE=OFF \
	-DBUILD_WITH_PULSE=OFF \
	-DBUILD_WITH_SDL2=OFF \
	-DBUILD_WITH_SSL=ON \
	-DBUILD_WITH_TREMOR=OFF \
	-DBUILD_WITH_VORBIS=OFF \
	-DBUILD_SHARED_LIBS=ON \
	-DBUILD_STATIC_LIBS=OFF \
	-DWERROR=OFF

define SOUNDTAP_SNAPCAST_REMOVE_UNUSED_WEB_UI
	rm -rf $(TARGET_DIR)/usr/share/snapserver/snapweb
	rm -rf $(TARGET_DIR)/usr/etc/snapserver $(TARGET_DIR)/usr/etc/snapserver.conf
endef
SOUNDTAP_SNAPCAST_POST_INSTALL_TARGET_HOOKS += SOUNDTAP_SNAPCAST_REMOVE_UNUSED_WEB_UI

$(eval $(cmake-package))
