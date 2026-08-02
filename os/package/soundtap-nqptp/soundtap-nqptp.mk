################################################################################
#
# soundtap-nqptp
#
################################################################################

# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Upstream nqptp, followed by the version watch; Buildroot's
# own package is 1.2.4, from before the 1.2.6 synchronisation changes.
SOUNDTAP_NQPTP_VERSION = 1.2.9
SOUNDTAP_NQPTP_SITE = $(call github,mikebrady,nqptp,$(SOUNDTAP_NQPTP_VERSION))
SOUNDTAP_NQPTP_LICENSE = GPL-2.0
SOUNDTAP_NQPTP_LICENSE_FILES = LICENSE
SOUNDTAP_NQPTP_DEPENDENCIES = host-pkgconf
# GitHub release archives do not contain a generated configure script.
SOUNDTAP_NQPTP_AUTORECONF = YES

define SOUNDTAP_NQPTP_INSTALL_TARGET_CMDS
	$(INSTALL) -D -m 0755 $(@D)/nqptp $(TARGET_DIR)/usr/bin/nqptp
endef

$(eval $(autotools-package))
