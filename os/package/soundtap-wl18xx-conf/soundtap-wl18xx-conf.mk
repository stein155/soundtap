################################################################################
#
# soundtap-wl18xx-conf
#
################################################################################

# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# TI's wlconf builds the WiLink8 board configuration from documented text. The
# source is Buildroot's own 18xx-ti-utils download, so the tarball, its hash and the
# backup-site copy are shared; that package itself is not enabled, as it would also put
# TI's calibrator on the target.
SOUNDTAP_WL18XX_CONF_VERSION = R8.8
SOUNDTAP_WL18XX_CONF_SITE = https://git.ti.com/git/wilink8-wlan/18xx-ti-utils.git
SOUNDTAP_WL18XX_CONF_SITE_METHOD = git
SOUNDTAP_WL18XX_CONF_DL_SUBDIR = 18xx-ti-utils
SOUNDTAP_WL18XX_CONF_SOURCE = 18xx-ti-utils-$(SOUNDTAP_WL18XX_CONF_VERSION)-git4.tar.gz
# wlconf/README says GPL-2.0; only its output, generated from our text, ships.
SOUNDTAP_WL18XX_CONF_LICENSE = BSD-3-Clause
SOUNDTAP_WL18XX_CONF_LICENSE_FILES = COPYING

SOUNDTAP_WL18XX_CONF_REGIONS = DF JP KR US

# wlconf reads its struct.bin from the working directory. -X skips the checksum check of
# TI's default, which R8.8 ships stale; every write recomputes it.
define SOUNDTAP_WL18XX_CONF_BUILD_CMDS
	$(HOST_MAKE_ENV) $(MAKE) -C $(@D)/wlconf $(HOST_CONFIGURE_OPTS)
	for r in $(SOUNDTAP_WL18XX_CONF_REGIONS); do \
		cat $(SOUNDTAP_WL18XX_CONF_PKGDIR)/board.conf \
			$(SOUNDTAP_WL18XX_CONF_PKGDIR)/$$r.conf > $(@D)/$$r.txt && \
		(cd $(@D)/wlconf && ./wlconf -X -i wl18xx-conf-default.bin \
			-o $(@D)/wl18xx-conf.bin$$r -C $(@D)/$$r.txt) || exit 1; \
	done
	# The bytes the speakers ran under Bose: regenerated, not changed.
	cd $(@D) && sha256sum -c $(SOUNDTAP_WL18XX_CONF_PKGDIR)/wl18xx-conf.sha256
endef

# The driver asks for wl18xx-conf.bin; DF until soundtapd points it at the speaker's region.
define SOUNDTAP_WL18XX_CONF_INSTALL_TARGET_CMDS
	mkdir -p $(TARGET_DIR)/lib/firmware/ti-connectivity
	for r in $(SOUNDTAP_WL18XX_CONF_REGIONS); do \
		$(INSTALL) -m 0644 $(@D)/wl18xx-conf.bin$$r \
			$(TARGET_DIR)/lib/firmware/ti-connectivity/ || exit 1; \
	done
	ln -sf wl18xx-conf.binDF $(TARGET_DIR)/lib/firmware/ti-connectivity/wl18xx-conf.bin
endef

$(eval $(generic-package))
