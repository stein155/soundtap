################################################################################
#
# soundtap-librespot
#
################################################################################

# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

SOUNDTAP_LIBRESPOT_VERSION = 0.8.0
SOUNDTAP_LIBRESPOT_SITE = $(call github,librespot-org,librespot,v$(SOUNDTAP_LIBRESPOT_VERSION))
SOUNDTAP_LIBRESPOT_LICENSE = MIT
SOUNDTAP_LIBRESPOT_LICENSE_FILES = LICENSE
SOUNDTAP_LIBRESPOT_DEPENDENCIES = alsa-lib avahi dbus host-pkgconf

SOUNDTAP_LIBRESPOT_CARGO_BUILD_OPTS = \
	--no-default-features \
	--features "rustls-tls-webpki-roots alsa-backend with-avahi"
SOUNDTAP_LIBRESPOT_CARGO_INSTALL_OPTS = $(SOUNDTAP_LIBRESPOT_CARGO_BUILD_OPTS)

# Fat LTO with one codegen unit: the stripped binary drops from 16.1 to 10.4 MB
# (0.8.0, same features, measured on an arm64 build). It is copied into RAM
# (/run/audio-bin) at boot, and that is RAM on a 113 MB speaker. opt-level stays 3:
# "s" saves another 2.6 MB, but Vorbis decoding is on the multiroom leader's critical
# CPU path.
#
# Rust's musl targets default to crt-static, but the ALSA backend links the
# shared libasound installed in the image.
#
# SOURCE_DATE_EPOCH is named here although the build exports it: librespot-core's build.rs
# takes its build ID from it and draws eight random characters without it, and vergen takes
# the build date from it, so the binary is the same from build to build (ADR 0011).
SOUNDTAP_LIBRESPOT_CARGO_ENV = \
	CARGO_TARGET_$(call UPPERCASE,$(RUSTC_TARGET_NAME))_RUSTFLAGS="-Ctarget-feature=-crt-static -Clink-arg=-Wl,--allow-multiple-definition" \
	CARGO_PROFILE_RELEASE_LTO=fat \
	CARGO_PROFILE_RELEASE_CODEGEN_UNITS=1 \
	SOURCE_DATE_EPOCH=$(SOURCE_DATE_EPOCH)


$(eval $(cargo-package))
