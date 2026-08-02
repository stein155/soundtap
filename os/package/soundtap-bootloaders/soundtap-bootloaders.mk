################################################################################
#
# host-soundtap-bootloaders
#
################################################################################

# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# The SPL and U-Boot images the STUs carry, built from os/bootloader's source, patches
# and final .config (build-variant.sh). A host package rather than BR2_TARGET_UBOOT: that
# builds one U-Boot per profile with the target toolchain, and each profile here needs two
# builds with different patch sets, compiled by the exact compiler the mainline images were
# proven with -- Debian bookworm's gcc 12 cross compiler, not the profile's gcc 13 -- because
# only the same compiler gives the same bytes.
HOST_SOUNDTAP_BOOTLOADERS_VERSION = 2026.07
HOST_SOUNDTAP_BOOTLOADERS_SOURCE = u-boot-$(HOST_SOUNDTAP_BOOTLOADERS_VERSION).tar.bz2
HOST_SOUNDTAP_BOOTLOADERS_SITE = https://ftp.denx.de/pub/u-boot
# Buildroot's own U-Boot directory, so its mirror (sources.buildroot.net/uboot) has the
# archive when ftp.denx.de does not answer. The hash below still decides what is accepted.
HOST_SOUNDTAP_BOOTLOADERS_DL_SUBDIR = uboot
HOST_SOUNDTAP_BOOTLOADERS_LICENSE = GPL-2.0+
HOST_SOUNDTAP_BOOTLOADERS_LICENSE_FILES = Licenses/gpl-2.0.txt
# Buildroot's own U-Boot CPE, so tools/release/os_cves.py asks NVD about what ships.
HOST_SOUNDTAP_BOOTLOADERS_CPE_ID_VENDOR = denx
HOST_SOUNDTAP_BOOTLOADERS_CPE_ID_PRODUCT = u-boot
HOST_SOUNDTAP_BOOTLOADERS_DEPENDENCIES = \
	host-bison \
	host-flex \
	host-gnutls \
	host-openssl \
	host-pkgconf \
	host-util-linux

# Debian bookworm's arm-linux-gnueabihf gcc 12.2.0-14cross1 and binutils 2.40-2, the
# versions Dockerfile.wla-uboot installs, pinned by snapshot date and hash.
HOST_SOUNDTAP_BOOTLOADERS_DEBIAN = https://snapshot.debian.org/archive/debian/20260801T000000Z/pool/main
HOST_SOUNDTAP_BOOTLOADERS_DEB_ARCH = $(if $(filter aarch64,$(HOSTARCH)),arm64,amd64)
HOST_SOUNDTAP_BOOTLOADERS_DEBS = \
	$(HOST_SOUNDTAP_BOOTLOADERS_DEBIAN)/b/binutils/binutils-arm-linux-gnueabihf_2.40-2_$(HOST_SOUNDTAP_BOOTLOADERS_DEB_ARCH).deb \
	$(HOST_SOUNDTAP_BOOTLOADERS_DEBIAN)/g/gcc-12-cross/cpp-12-arm-linux-gnueabihf_12.2.0-14cross1_$(HOST_SOUNDTAP_BOOTLOADERS_DEB_ARCH).deb \
	$(HOST_SOUNDTAP_BOOTLOADERS_DEBIAN)/g/gcc-12-cross/gcc-12-arm-linux-gnueabihf_12.2.0-14cross1_$(HOST_SOUNDTAP_BOOTLOADERS_DEB_ARCH).deb \
	$(HOST_SOUNDTAP_BOOTLOADERS_DEBIAN)/g/gcc-12-cross/libgcc-12-dev-armhf-cross_12.2.0-14cross1_all.deb
HOST_SOUNDTAP_BOOTLOADERS_EXTRA_DOWNLOADS = $(HOST_SOUNDTAP_BOOTLOADERS_DEBS)

HOST_SOUNDTAP_BOOTLOADERS_VARIANTS = \
	$(if $(BR2_PACKAGE_HOST_SOUNDTAP_BOOTLOADERS_ST20),st20-spl) \
	$(if $(BR2_PACKAGE_HOST_SOUNDTAP_BOOTLOADERS_WLA),wla-uboot wla-spl)
HOST_SOUNDTAP_BOOTLOADERS_SCRIPT = $(BR2_EXTERNAL_SOUNDTAP_PATH)/package/soundtap-bootloaders/build-variant.sh
# The profile the USB-stick recovery holds a signed package to (ADR 0048), by the rule
# post-build.sh writes /etc/soundtap/profile with; post-image.sh checks the two agree.
HOST_SOUNDTAP_BOOTLOADERS_PROFILE = $(if $(BR2_PACKAGE_DROPBEAR),alpha,production)
HOST_SOUNDTAP_BOOTLOADERS_GCC = $(@D)/gcc/usr/bin/arm-linux-gnueabihf-

# build-variant.sh unpacks a fresh U-Boot tree per variant, so the tarball is not unpacked
# here (only its licence text, for legal-info); the toolchain is. gcc finds cc1 and as relative to itself, so it runs from here, but
# Debian's cross binutils have no rpath to their private libbfd: without LD_LIBRARY_PATH as
# fails, Kconfig's compiler probes quietly come out n, and the .config check trips.
define HOST_SOUNDTAP_BOOTLOADERS_EXTRACT_CMDS
	tar -xjf $(HOST_SOUNDTAP_BOOTLOADERS_DL_DIR)/$(HOST_SOUNDTAP_BOOTLOADERS_SOURCE) \
		--strip-components=1 -C $(@D) u-boot-$(HOST_SOUNDTAP_BOOTLOADERS_VERSION)/Licenses/gpl-2.0.txt
	mkdir -p $(@D)/gcc
	$(foreach d,$(notdir $(HOST_SOUNDTAP_BOOTLOADERS_DEBS)),\
		cd $(@D) && ar x $(HOST_SOUNDTAP_BOOTLOADERS_DL_DIR)/$(d) data.tar.xz && \
		tar -xJf data.tar.xz -C $(@D)/gcc && rm data.tar.xz$(sep))
	ln -sf arm-linux-gnueabihf-gcc-12 $(HOST_SOUNDTAP_BOOTLOADERS_GCC)gcc
	ln -sf arm-linux-gnueabihf-cpp-12 $(HOST_SOUNDTAP_BOOTLOADERS_GCC)cpp
endef

# host-gnutls is built --without-p11-kit, and U-Boot's mkeficapsule calls pkcs11
# unconditionally. Nothing ships or runs mkeficapsule, and TOOLS_MKEFICAPSULE has to stay on
# for the .config to match mainline, so its pkcs11 references are left unresolved. Make
# appends the Makefile's -lgnutls to this environment value. Build host-p11-kit into
# host-gnutls if mkeficapsule is ever needed.
define HOST_SOUNDTAP_BOOTLOADERS_BUILD_CMDS
	$(foreach v,$(HOST_SOUNDTAP_BOOTLOADERS_VARIANTS),\
		$(HOST_MAKE_ENV) \
		LD_LIBRARY_PATH=$(@D)/gcc/usr/lib/$(HOSTARCH)-linux-gnu \
		HOSTLDLIBS_mkeficapsule=-Wl,--unresolved-symbols=ignore-in-object-files \
		HOSTCFLAGS="$(HOST_CFLAGS)" HOSTLDFLAGS="$(HOST_LDFLAGS)" \
		CROSS_COMPILE=$(HOST_SOUNDTAP_BOOTLOADERS_GCC) \
		SOURCE_DATE_EPOCH=$$(cat $(BR2_EXTERNAL_SOUNDTAP_PATH)/bootloader/source-date-epoch) \
		SOUNDTAP_PROFILE=$(HOST_SOUNDTAP_BOOTLOADERS_PROFILE) \
		JOBS=$(PARALLEL_JOBS) \
		$(SHELL) $(HOST_SOUNDTAP_BOOTLOADERS_SCRIPT) $(v) \
			$(HOST_SOUNDTAP_BOOTLOADERS_DL_DIR)/$(HOST_SOUNDTAP_BOOTLOADERS_SOURCE) \
			$(@D)/build-$(v) $(@D)/out/$(v)$(sep))
endef

# Target images, not host tools: they go where the profile's post scripts look for them.
define HOST_SOUNDTAP_BOOTLOADERS_INSTALL_CMDS
	$(foreach v,$(HOST_SOUNDTAP_BOOTLOADERS_VARIANTS),\
		rm -rf $(BINARIES_DIR)/$(v) && mkdir -p $(BINARIES_DIR) && \
		cp -a $(@D)/out/$(v) $(BINARIES_DIR)/$(v)$(sep))
endef

# legal-info archives the U-Boot tarball and the toolchain, not what build-variant.sh applies
# to it: the patches, DTS, splash header, final configs and the script go next to them at
# their repository paths.
define HOST_SOUNDTAP_BOOTLOADERS_LEGAL_INPUTS
	mkdir -p $(HOST_SOUNDTAP_BOOTLOADERS_REDIST_SOURCES_DIR)/soundtap-inputs
	cd $(BR2_EXTERNAL_SOUNDTAP_PATH)/.. && cp -a --parents LICENSE \
		os/bootloader/uboot os/bootloader/source-date-epoch os/package/soundtap-bootloaders \
		os/support/check_wla_uboot_dts.py \
		$(HOST_SOUNDTAP_BOOTLOADERS_REDIST_SOURCES_DIR)/soundtap-inputs/
endef
HOST_SOUNDTAP_BOOTLOADERS_POST_LEGAL_INFO_HOOKS += HOST_SOUNDTAP_BOOTLOADERS_LEGAL_INPUTS

$(eval $(host-generic-package))

# The inputs live outside the package's source tree; rebuild when any of them changes.
$(HOST_SOUNDTAP_BOOTLOADERS_TARGET_BUILD): \
	$(HOST_SOUNDTAP_BOOTLOADERS_SCRIPT) \
	$(wildcard $(BR2_EXTERNAL_SOUNDTAP_PATH)/package/soundtap-bootloaders/configs/*.config) \
	$(wildcard $(BR2_EXTERNAL_SOUNDTAP_PATH)/bootloader/uboot/*) \
	$(BR2_EXTERNAL_SOUNDTAP_PATH)/bootloader/source-date-epoch \
	$(BR2_EXTERNAL_SOUNDTAP_PATH)/support/check_wla_uboot_dts.py
