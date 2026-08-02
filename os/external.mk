# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

include $(sort $(wildcard $(BR2_EXTERNAL_SOUNDTAP_PATH)/package/*/*.mk))
include $(BR2_EXTERNAL_SOUNDTAP_PATH)/ignore-cves.mk

ifeq ($(BR2_PACKAGE_SOUNDTAP_RUNTIME),y)
define SOUNDTAP_RUNTIME_REMOVE_UNUSED_ALSA_UTILS
	rm -f $(TARGET_DIR)/usr/bin/alsamixer
endef
ALSA_UTILS_POST_INSTALL_TARGET_HOOKS += SOUNDTAP_RUNTIME_REMOVE_UNUSED_ALSA_UTILS

define SOUNDTAP_RUNTIME_REMOVE_UNUSED_BLUEZ_UTILS
	rm -f \
		$(TARGET_DIR)/usr/bin/btattach \
		$(TARGET_DIR)/usr/bin/gatttool \
		$(TARGET_DIR)/usr/bin/hciconfig \
		$(TARGET_DIR)/usr/bin/hcidump \
		$(TARGET_DIR)/usr/bin/hcitool \
		$(TARGET_DIR)/usr/bin/hex2hcd \
		$(TARGET_DIR)/usr/bin/l2ping \
		$(TARGET_DIR)/usr/bin/sdptool
endef
BLUEZ5_UTILS_POST_INSTALL_TARGET_HOOKS += SOUNDTAP_RUNTIME_REMOVE_UNUSED_BLUEZ_UTILS
endif

# The one-image kernel builds the WL1835's Bluetooth init script in
# (os/board/image/linux.fragment: EXTRA_FIRMWARE_DIR="soundtap-firmware"), because
# hci_ll probes before UBIFS is mounted. Other profiles do not name the directory.
define SOUNDTAP_LINUX_BUILTIN_FIRMWARE
	$(INSTALL) -D -m 0644 \
		$(BR2_EXTERNAL_SOUNDTAP_PATH)/upstream/ti-connectivity/TIInit_11.8.32.bts \
		$(LINUX_DIR)/soundtap-firmware/ti-connectivity/TIInit_11.8.32.bts
endef
LINUX_POST_PATCH_HOOKS += SOUNDTAP_LINUX_BUILTIN_FIRMWARE

# The McASP machine driver has one source, os/drivers/soundtap-st10-audio.c; linux patch
# 0002 only adds it to sound/soc/ti/Makefile.
define SOUNDTAP_LINUX_MCASP_DRIVER
	$(INSTALL) -D -m 0644 \
		$(BR2_EXTERNAL_SOUNDTAP_PATH)/drivers/soundtap-st10-audio.c \
		$(LINUX_DIR)/sound/soc/ti/soundtap-st10-audio.c
endef
LINUX_POST_PATCH_HOOKS += SOUNDTAP_LINUX_MCASP_DRIVER

# legal-info archives the kernel tarball and its patches, not the driver the hook above
# copies in.
define SOUNDTAP_LINUX_LEGAL_INPUTS
	mkdir -p $(LINUX_REDIST_SOURCES_DIR)/soundtap-inputs
	cd $(BR2_EXTERNAL_SOUNDTAP_PATH)/.. && cp -a --parents os/drivers/soundtap-st10-audio.c \
		$(LINUX_REDIST_SOURCES_DIR)/soundtap-inputs/
endef
LINUX_POST_LEGAL_INFO_HOOKS += SOUNDTAP_LINUX_LEGAL_INPUTS

# Buildroot's legal-info saves no source for a local package, only a warning. These
# three build from the repository, so the files they read go into their sources directory at
# their repository paths, like the inputs above. soundtapd's include the frontend: the
# image embeds its build.
define SOUNDTAP_LOCAL_LEGAL_INPUTS
	mkdir -p $($(1)_REDIST_SOURCES_DIR)/soundtap-inputs
	cd $(BR2_EXTERNAL_SOUNDTAP_PATH)/.. && tar -cf - --exclude=node_modules \
		--exclude=internal/web/dist --exclude='.stamp_*' $(2) \
		| tar -xf - -C $($(1)_REDIST_SOURCES_DIR)/soundtap-inputs
endef
SOUNDTAPD_LEGAL_LOCAL = $(call SOUNDTAP_LOCAL_LEGAL_INPUTS,SOUNDTAPD,app)
SOUNDTAPD_POST_LEGAL_INFO_HOOKS += SOUNDTAPD_LEGAL_LOCAL
SOUNDTAP_RUNTIME_LEGAL_LOCAL = $(call SOUNDTAP_LOCAL_LEGAL_INPUTS,SOUNDTAP_RUNTIME,os/overlay os/package/soundtap-runtime)
SOUNDTAP_RUNTIME_POST_LEGAL_INFO_HOOKS += SOUNDTAP_RUNTIME_LEGAL_LOCAL
SOUNDTAP_AIC3256_LOADER_LEGAL_LOCAL = $(call SOUNDTAP_LOCAL_LEGAL_INPUTS,SOUNDTAP_AIC3256_LOADER,os/drivers/aic3256-loader.c os/drivers/aic3256-bitbang.c os/package/soundtap-aic3256-loader)
SOUNDTAP_AIC3256_LOADER_POST_LEGAL_INFO_HOOKS += SOUNDTAP_AIC3256_LOADER_LEGAL_LOCAL
