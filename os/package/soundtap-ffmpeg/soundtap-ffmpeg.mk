################################################################################
#
# soundtap-ffmpeg
#
################################################################################

# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Buildroot master's ffmpeg version; drop this package for Buildroot's once
# BUILDROOT_COMMIT carries 9.x.
SOUNDTAP_FFMPEG_VERSION = 9.0.2
SOUNDTAP_FFMPEG_SOURCE = ffmpeg-$(SOUNDTAP_FFMPEG_VERSION).tar.xz
SOUNDTAP_FFMPEG_SITE = https://ffmpeg.org/releases
SOUNDTAP_FFMPEG_INSTALL_STAGING = YES
SOUNDTAP_FFMPEG_LICENSE = LGPL-2.1+, libjpeg license
SOUNDTAP_FFMPEG_LICENSE_FILES = LICENSE.md COPYING.LGPLv2.1
SOUNDTAP_FFMPEG_CPE_ID_VENDOR = ffmpeg
SOUNDTAP_FFMPEG_CPE_ID_PRODUCT = ffmpeg
SOUNDTAP_FFMPEG_DEPENDENCIES = host-pkgconf libsoxr openssl zlib

# Audio only (ADR 0041): AirPlay's AAC/ALAC, radio's MP3/AAC/Ogg, and what DLNA
# controllers push; Music Assistant streams FLAC to a DLNA renderer by default, so FLAC
# and WAV are in. Every other component, video included, stays out through
# --disable-everything. soundtapd's tests read the filter and protocol lists here.
SOUNDTAP_FFMPEG_ENCODERS = pcm_s16le
SOUNDTAP_FFMPEG_DECODERS = aac aac_latm aac_fixed alac mp3 flac vorbis opus pcm_s16le pcm_s16be pcm_s24le wmav1 wmav2
SOUNDTAP_FFMPEG_MUXERS = pcm_s16le
SOUNDTAP_FFMPEG_DEMUXERS = aac asf mov mp3 flac ogg wav pcm_s16le pcm_s16be hls mpegts
SOUNDTAP_FFMPEG_PARSERS = aac aac_latm mpegaudio flac vorbis opus
SOUNDTAP_FFMPEG_PROTOCOLS = file http https pipe tcp tls udp crypto
SOUNDTAP_FFMPEG_FILTERS = aresample volume bass aformat afade

SOUNDTAP_FFMPEG_CONF_OPTS = \
	--prefix=/usr \
	--cpu=$(GCC_TARGET_CPU) \
	--enable-armv6 \
	--enable-vfp \
	--enable-neon \
	--enable-pic \
	--extra-libs=-latomic \
	--enable-small \
	--disable-doc \
	--disable-symver \
	--disable-ffplay \
	--disable-ffprobe \
	--disable-avdevice \
	--disable-swscale \
	--disable-bzlib \
	--disable-iconv \
	--enable-libsoxr \
	--enable-openssl \
	--enable-zlib \
	--disable-everything \
	$(foreach c,$(SOUNDTAP_FFMPEG_ENCODERS),--enable-encoder=$(c)) \
	$(foreach c,$(SOUNDTAP_FFMPEG_DECODERS),--enable-decoder=$(c)) \
	$(foreach c,$(SOUNDTAP_FFMPEG_MUXERS),--enable-muxer=$(c)) \
	$(foreach c,$(SOUNDTAP_FFMPEG_DEMUXERS),--enable-demuxer=$(c)) \
	$(foreach c,$(SOUNDTAP_FFMPEG_PARSERS),--enable-parser=$(c)) \
	$(foreach c,$(SOUNDTAP_FFMPEG_PROTOCOLS),--enable-protocol=$(c)) \
	$(foreach c,$(SOUNDTAP_FFMPEG_FILTERS),--enable-filter=$(c))

# FFmpeg's configure takes no --target or --host, as in Buildroot's ffmpeg.mk.
define SOUNDTAP_FFMPEG_CONFIGURE_CMDS
	(cd $(@D) && rm -rf config.cache && \
	$(TARGET_CONFIGURE_OPTS) \
	$(TARGET_CONFIGURE_ARGS) \
	CFLAGS="$(TARGET_CFLAGS)" LDFLAGS="$(TARGET_LDFLAGS)" \
	./configure \
		--enable-cross-compile \
		--cross-prefix=$(TARGET_CROSS) \
		--sysroot=$(STAGING_DIR) \
		--host-cc="$(HOSTCC)" \
		--arch=$(BR2_ARCH) \
		--target-os=linux \
		--disable-stripping \
		--pkg-config="$(PKG_CONFIG_HOST_BINARY)" \
		$(SHARED_STATIC_LIBS_OPTS) \
		$(SOUNDTAP_FFMPEG_CONF_OPTS) \
	)
endef

define SOUNDTAP_FFMPEG_REMOVE_EXAMPLE_SRC_FILES
	rm -rf $(TARGET_DIR)/usr/share/ffmpeg/examples
endef
SOUNDTAP_FFMPEG_POST_INSTALL_TARGET_HOOKS += SOUNDTAP_FFMPEG_REMOVE_EXAMPLE_SRC_FILES

$(eval $(autotools-package))
