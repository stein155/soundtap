################################################################################
#
# host-soundtap-bco-app
#
################################################################################

# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Our own BCO (BridgeCo DM870, ARM926) application, bco/app. Bare metal, so not a target
# package: Debian bookworm's arm-none-eabi gcc 12.2.1 (the one bco/app/Dockerfile uses),
# pinned by snapshot date and hash. Output goes to $(BINARIES_DIR)/bco-app/<variant>/.
# No Bose bytes: link-own carries our own WiLD core (bco/wild/fw) instead of the vendor blob,
# and every variant starts from our own first stage (ADR 0031). link-own/app.bcd is the image
# the ST20 rootfs ships and soundtapd flashes.
HOST_SOUNDTAP_BCO_APP_VERSION = 1
HOST_SOUNDTAP_BCO_APP_LICENSE = GPL-3.0-only
HOST_SOUNDTAP_BCO_APP_LICENSE_FILES = LICENSE

HOST_SOUNDTAP_BCO_APP_DEBIAN = https://snapshot.debian.org/archive/debian/20260801T000000Z/pool/main
HOST_SOUNDTAP_BCO_APP_DEB_ARCH = $(if $(filter aarch64,$(HOSTARCH)),arm64,amd64)
HOST_SOUNDTAP_BCO_APP_SOURCE = gcc-arm-none-eabi_12.2.rel1-1_$(HOST_SOUNDTAP_BCO_APP_DEB_ARCH).deb
HOST_SOUNDTAP_BCO_APP_SITE = $(HOST_SOUNDTAP_BCO_APP_DEBIAN)/g/gcc-arm-none-eabi
HOST_SOUNDTAP_BCO_APP_DEBS = \
	$(HOST_SOUNDTAP_BCO_APP_SITE)/$(HOST_SOUNDTAP_BCO_APP_SOURCE) \
	$(HOST_SOUNDTAP_BCO_APP_DEBIAN)/b/binutils-arm-none-eabi/binutils-arm-none-eabi_2.40-2+18+b1_$(HOST_SOUNDTAP_BCO_APP_DEB_ARCH).deb \
	$(HOST_SOUNDTAP_BCO_APP_DEBIAN)/n/newlib/libnewlib-arm-none-eabi_3.3.0-1.3+deb12u1_all.deb \
	$(HOST_SOUNDTAP_BCO_APP_DEBIAN)/n/newlib/libnewlib-dev_3.3.0-1.3+deb12u1_all.deb
HOST_SOUNDTAP_BCO_APP_EXTRA_DOWNLOADS = $(filter-out %/$(HOST_SOUNDTAP_BCO_APP_SOURCE),$(HOST_SOUNDTAP_BCO_APP_DEBS))
HOST_SOUNDTAP_BCO_APP_SRC = $(BR2_EXTERNAL_SOUNDTAP_PATH)/../bco/app

HOST_SOUNDTAP_BCO_APP_VARIANTS = hello usb usb-connect mon link-own
HOST_SOUNDTAP_BCO_APP_CROSS = $(@D)/gcc/usr/bin/arm-none-eabi-

# The package source is the toolchain; our firmware is read from the external tree, and the
# repository's LICENSE (the firmware's) is copied in for legal-info. Debian's
# binutils have no rpath to their private libbfd, hence LD_LIBRARY_PATH (as in
# host-soundtap-bootloaders).
define HOST_SOUNDTAP_BCO_APP_EXTRACT_CMDS
	cp $(BR2_EXTERNAL_SOUNDTAP_PATH)/../LICENSE $(@D)/LICENSE
	mkdir -p $(@D)/gcc
	$(foreach d,$(notdir $(HOST_SOUNDTAP_BCO_APP_DEBS)),\
		cd $(@D) && ar x $(HOST_SOUNDTAP_BCO_APP_DL_DIR)/$(d) data.tar.xz && \
		tar -xJf data.tar.xz -C $(@D)/gcc && rm data.tar.xz$(sep))
endef

define HOST_SOUNDTAP_BCO_APP_BUILD_CMDS
	LD_LIBRARY_PATH=$(@D)/gcc/usr/lib/$(HOSTARCH)-linux-gnu CROSS=$(HOST_SOUNDTAP_BCO_APP_CROSS) \
		$(SHELL) $(BR2_EXTERNAL_SOUNDTAP_PATH)/../bco/wild/fw/build.sh $(@D)/wild
	$(foreach v,$(HOST_SOUNDTAP_BCO_APP_VARIANTS),\
		LD_LIBRARY_PATH=$(@D)/gcc/usr/lib/$(HOSTARCH)-linux-gnu \
		CROSS=$(HOST_SOUNDTAP_BCO_APP_CROSS) WILD_BIN=$(@D)/wild/wild.bin \
		BCO_PACK=$(BR2_EXTERNAL_SOUNDTAP_PATH)/../bco/tools/bco-pack \
		$(SHELL) $(HOST_SOUNDTAP_BCO_APP_SRC)/build-app.sh $(v) $(HOST_SOUNDTAP_BCO_APP_SRC) $(@D)/out$(sep))
endef

define HOST_SOUNDTAP_BCO_APP_INSTALL_CMDS
	rm -rf $(BINARIES_DIR)/bco-app && mkdir -p $(BINARIES_DIR)
	cp -a $(@D)/out $(BINARIES_DIR)/bco-app
endef

# legal-info archives the package source, which is only the toolchain; the firmware's own
# sources, packer and this recipe go next to it at their repository paths.
define HOST_SOUNDTAP_BCO_APP_LEGAL_INPUTS
	mkdir -p $(HOST_SOUNDTAP_BCO_APP_REDIST_SOURCES_DIR)/soundtap-inputs
	cd $(BR2_EXTERNAL_SOUNDTAP_PATH)/.. && cp -a --parents LICENSE \
		bco/app/build-app.sh bco/app/first.ld bco/app/app.ld bco/app/src \
		bco/wild/fw/build.sh bco/wild/fw/wild.ld bco/wild/fw/src \
		bco/tools/bco-pack bco/tools/copy-records.py os/package/soundtap-bco-app \
		$(HOST_SOUNDTAP_BCO_APP_REDIST_SOURCES_DIR)/soundtap-inputs/
endef
HOST_SOUNDTAP_BCO_APP_POST_LEGAL_INFO_HOOKS += HOST_SOUNDTAP_BCO_APP_LEGAL_INPUTS

$(eval $(host-generic-package))

# The inputs live outside the package's source tree; rebuild when any of them changes.
$(HOST_SOUNDTAP_BCO_APP_TARGET_BUILD): \
	$(wildcard $(HOST_SOUNDTAP_BCO_APP_SRC)/*.sh $(HOST_SOUNDTAP_BCO_APP_SRC)/*.ld $(HOST_SOUNDTAP_BCO_APP_SRC)/src/*) \
	$(wildcard $(BR2_EXTERNAL_SOUNDTAP_PATH)/../bco/wild/fw/*.sh $(BR2_EXTERNAL_SOUNDTAP_PATH)/../bco/wild/fw/*.ld \
		$(BR2_EXTERNAL_SOUNDTAP_PATH)/../bco/wild/fw/src/*) \
	$(BR2_EXTERNAL_SOUNDTAP_PATH)/../bco/tools/bco-pack \
	$(BR2_EXTERNAL_SOUNDTAP_PATH)/../bco/tools/copy-records.py
