################################################################################
#
# host-soundtap-plistutil
#
################################################################################

# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

SOUNDTAP_PLISTUTIL_VERSION = 2.7.0
SOUNDTAP_PLISTUTIL_SOURCE = libplist-$(SOUNDTAP_PLISTUTIL_VERSION).tar.bz2
SOUNDTAP_PLISTUTIL_SITE = https://github.com/libimobiledevice/libplist/releases/download/$(SOUNDTAP_PLISTUTIL_VERSION)
SOUNDTAP_PLISTUTIL_LICENSE = LGPL-2.1+
SOUNDTAP_PLISTUTIL_LICENSE_FILES = COPYING

HOST_SOUNDTAP_PLISTUTIL_CONF_OPTS = --without-cython

$(eval $(host-autotools-package))
