################################################################################
#
# soundtap-runtime
#
################################################################################

# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

SOUNDTAP_RUNTIME_VERSION = 1
SOUNDTAP_RUNTIME_SITE = $(BR2_EXTERNAL_SOUNDTAP_PATH)/overlay
SOUNDTAP_RUNTIME_SITE_METHOD = local
SOUNDTAP_RUNTIME_LICENSE = GPL-3.0-only
SOUNDTAP_RUNTIME_LICENSE_FILES = COPYING
SOUNDTAP_RUNTIME_DEPENDENCIES = \
	alsa-utils \
	bluez5_utils \
	soundtap-ffmpeg \
	soundtap-librespot \
	soundtap-shairport-sync \
	soundtap-snapcast \
	soundtapd

SOUNDTAP_RUNTIME_SOURCE_FILES = \
	$(shell find $(BR2_EXTERNAL_SOUNDTAP_PATH)/overlay -type f)

define SOUNDTAP_RUNTIME_INSTALL_TARGET_CMDS
	mkdir -p $(TARGET_DIR)/opt/soundtap $(TARGET_DIR)/etc/soundtap
	rsync -a $(@D)/opt/soundtap/ $(TARGET_DIR)/opt/soundtap/
	chmod 0755 $(TARGET_DIR)/opt/soundtap/*.sh
	$(INSTALL) -D -m 0755 $(@D)/etc/init.d/soundtap \
		$(TARGET_DIR)/etc/init.d/soundtap
	$(INSTALL) -D -m 0644 $(@D)/etc/soundtap/ir.map \
		$(TARGET_DIR)/etc/soundtap/ir.map
	# A hook of busybox's udhcpc default.script (Buildroot installs the script and its .d).
	$(INSTALL) -D -m 0755 $(@D)/usr/share/udhcpc/default.script.d/soundtap-log \
		$(TARGET_DIR)/usr/share/udhcpc/default.script.d/soundtap-log
	ln -sf /usr/bin/soundtapd $(TARGET_DIR)/opt/soundtap/soundtapd
	# soundtapd's own user may ask BlueZ and bluealsa over the system bus (ADR 0066).
	$(INSTALL) -D -m 0644 $(@D)/etc/dbus-1/system.d/soundtapd.conf \
		$(TARGET_DIR)/etc/dbus-1/system.d/soundtapd.conf
	# bluez builds btmgmt but lists it under noinst_PROGRAMS, so --enable-tools does not
	# install it and there is no Buildroot option that does. bt.sh needs it: the adapter's
	# Class of Device can only be set over the kernel's management socket, and without a
	# class a phone leaves the speaker out of its Bluetooth list entirely. 160 kB.
	$(INSTALL) -D -m 0755 $(BLUEZ5_UTILS_DIR)/tools/btmgmt $(TARGET_DIR)/usr/bin/btmgmt
endef

# Config files that another package also installs go in at target-finalize. With per-package
# directories the final target is the merge of every package's own target in alphabetical
# order, so soundtap-shairport-sync, soundtap-snapcast and soundtapd each brought back the
# stock avahi-daemon.conf and shairport-sync.conf over ours: every speaker ran the stock
# files (found 2026-10-09).
SOUNDTAP_RUNTIME_CONFIG_FILES = \
	etc/avahi/avahi-daemon.conf \
	etc/bluetooth/main.conf \
	etc/shairport-sync.conf \
	etc/snapserver.conf

define SOUNDTAP_RUNTIME_INSTALL_CONFIG_FILES
	$(foreach f,$(SOUNDTAP_RUNTIME_CONFIG_FILES),\
		$(INSTALL) -D -m 0644 $(SOUNDTAP_RUNTIME_SITE)/$(f) $(TARGET_DIR)/$(f)$(sep))
endef
SOUNDTAP_RUNTIME_TARGET_FINALIZE_HOOKS += SOUNDTAP_RUNTIME_INSTALL_CONFIG_FILES

# TIInit_11.8.32.bts is installed by os/board/rootfs/post-build.sh and built into the kernel,
# not by a package, so its file, provenance and licence go into legal-info from here.
define SOUNDTAP_RUNTIME_LEGAL_INPUTS
	mkdir -p $(SOUNDTAP_RUNTIME_REDIST_SOURCES_DIR)/soundtap-inputs
	cd $(BR2_EXTERNAL_SOUNDTAP_PATH)/.. && cp -a --parents \
		os/upstream/ti-connectivity/TIInit_11.8.32.bts \
		os/upstream/ti-connectivity/PROVENANCE.md os/upstream/ti-connectivity/LICENCE \
		$(SOUNDTAP_RUNTIME_REDIST_SOURCES_DIR)/soundtap-inputs/
endef
SOUNDTAP_RUNTIME_POST_LEGAL_INFO_HOOKS += SOUNDTAP_RUNTIME_LEGAL_INPUTS

$(eval $(generic-package))

$(SOUNDTAP_RUNTIME_TARGET_RSYNC): $(SOUNDTAP_RUNTIME_SOURCE_FILES)
$(SOUNDTAP_RUNTIME_TARGET_INSTALL_TARGET): $(SOUNDTAP_RUNTIME_SOURCE_FILES)
