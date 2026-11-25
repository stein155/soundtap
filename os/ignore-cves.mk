# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Known CVEs that do not reach SoundTap Core. Buildroot lists <PKG>_IGNORE_CVES in the
# SBOM's vulnerabilities and `make os-cves` clears them. NVD matches on the version alone, so
# most of these are code we do not build, or kernel fixes NVD has not recorded for 6.12.
#
# One CVE per line, and each line ends in its reason, in a form
# os/support/ignore_cves.py understands and checks (test_ignore_cves.py; `make
# image-check` also checks the CONFIG_ claims against the built kernel.config):
#   fixed in 6.12.N        the kernel CNA's 6.12 fix release, at or below the pinned kernel
#   CONFIG_X off           code behind a symbol our kernel config leaves off or unreachable
#   CONFIG_X=m             code built only as a module the image does not carry
#   bug came in X.Y        introduced after 6.12
#   fixed by our patch     a patch of ours for the package names the CVE
#   fixed in/before V      the package version we ship already has the fix
#   not built for Linux    a package's file only another platform builds (the kernel names
#                          its CONFIG_ symbol instead)
#   ffmpeg filter X not built  a filter, muxer, demuxer, protocol, encoder or decoder
#                          soundtap-ffmpeg.mk leaves out (also 'ffmpeg muxer X', 'ffmpeg
#                          demuxer X', 'ffmpeg protocol X', 'ffmpeg encoder X', 'ffmpeg decoder X')
#   maintainer accepted    open, with the maintainer's recorded acceptance and why

# Linux 6.12.117 (BR2_LINUX_KERNEL_CUSTOM_VERSION_VALUE in os/configs/soundtap_defconfig).
#
# Fixed in a 6.12 stable release at or below 6.12.117. The fix release is from the kernel CNA
# (git.kernel.org vulns.git, cve/published/<year>/<id>.json); NVD has not recorded the 6.12
# backport, so it still matches the version.
LINUX_IGNORE_CVES += CVE-2024-52560 # fixed in 6.12.110
LINUX_IGNORE_CVES += CVE-2024-57995 # fixed in 6.12.57
LINUX_IGNORE_CVES += CVE-2024-58093 # fixed in 6.12.23
LINUX_IGNORE_CVES += CVE-2024-58094 # fixed in 6.12.108
LINUX_IGNORE_CVES += CVE-2024-58096 # fixed in 6.12.69
LINUX_IGNORE_CVES += CVE-2025-21709 # fixed in 6.12.83
LINUX_IGNORE_CVES += CVE-2025-21751 # fixed in 6.12.48
LINUX_IGNORE_CVES += CVE-2025-22104 # fixed in 6.12.108
LINUX_IGNORE_CVES += CVE-2025-22107 # fixed in 6.12.59
LINUX_IGNORE_CVES += CVE-2025-22108 # fixed in 6.12.109
LINUX_IGNORE_CVES += CVE-2025-22109 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-22117 # fixed in 6.12.80
LINUX_IGNORE_CVES += CVE-2025-22121 # fixed in 6.12.59
LINUX_IGNORE_CVES += CVE-2025-23132 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-37803 # fixed in 6.12.57
LINUX_IGNORE_CVES += CVE-2025-37906 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-38064 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-38137 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-38187 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-38204 # fixed in 6.12.113
LINUX_IGNORE_CVES += CVE-2025-38206 # fixed in 6.12.108
LINUX_IGNORE_CVES += CVE-2025-38234 # fixed in 6.12.64
LINUX_IGNORE_CVES += CVE-2025-38248 # fixed in 6.12.67
LINUX_IGNORE_CVES += CVE-2025-38261 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-38421 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-38584 # fixed in 6.12.86
LINUX_IGNORE_CVES += CVE-2025-38627 # fixed in 6.12.78
LINUX_IGNORE_CVES += CVE-2025-38636 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-38656 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-39677 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-39789 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-39859 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-39862 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-39933 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-39958 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-71074 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2025-71152 # fixed in 6.12.78
LINUX_IGNORE_CVES += CVE-2025-71161 # fixed in 6.12.78
LINUX_IGNORE_CVES += CVE-2025-71221 # fixed in 6.12.78
LINUX_IGNORE_CVES += CVE-2025-71289 # fixed in 6.12.92
LINUX_IGNORE_CVES += CVE-2026-23066 # fixed in 6.12.78
LINUX_IGNORE_CVES += CVE-2026-23171 # fixed in 6.12.90
LINUX_IGNORE_CVES += CVE-2026-23208 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-23226 # fixed in 6.12.77
LINUX_IGNORE_CVES += CVE-2026-23272 # fixed in 6.12.91
LINUX_IGNORE_CVES += CVE-2026-23327 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-23444 # fixed in 6.12.84
LINUX_IGNORE_CVES += CVE-2026-31486 # fixed in 6.12.92
LINUX_IGNORE_CVES += CVE-2026-31493 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-31613 # fixed in 6.12.91
LINUX_IGNORE_CVES += CVE-2026-31630 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-31663 # fixed in 6.12.94
LINUX_IGNORE_CVES += CVE-2026-31709 # fixed in 6.12.86
LINUX_IGNORE_CVES += CVE-2026-31715 # fixed in 6.12.86
LINUX_IGNORE_CVES += CVE-2026-31717 # fixed in 6.12.92
LINUX_IGNORE_CVES += CVE-2026-31771 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43009 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43042 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43048 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43083 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43101 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43116 # fixed in 6.12.94
LINUX_IGNORE_CVES += CVE-2026-43172 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43185 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43197 # fixed in 6.12.103
LINUX_IGNORE_CVES += CVE-2026-43198 # fixed in 6.12.110
LINUX_IGNORE_CVES += CVE-2026-43213 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43245 # fixed in 6.12.91
LINUX_IGNORE_CVES += CVE-2026-43263 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43303 # fixed in 6.12.93
LINUX_IGNORE_CVES += CVE-2026-43352 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43353 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43414 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43464 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43465 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-43500 # fixed in 6.12.88
LINUX_IGNORE_CVES += CVE-2026-45932 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-45944 # fixed in 6.12.97
LINUX_IGNORE_CVES += CVE-2026-45956 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-46054 # fixed in 6.12.95
LINUX_IGNORE_CVES += CVE-2026-46093 # fixed in 6.12.96
LINUX_IGNORE_CVES += CVE-2026-46130 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-46175 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-46181 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-46203 # fixed in 6.12.94
LINUX_IGNORE_CVES += CVE-2026-46242 # fixed in 6.12.95
LINUX_IGNORE_CVES += CVE-2026-46324 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-46325 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-46330 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-52956 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-52983 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-52988 # fixed in 6.12.112
LINUX_IGNORE_CVES += CVE-2026-52991 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53000 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53005 # fixed in 6.12.97
LINUX_IGNORE_CVES += CVE-2026-53009 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53010 # fixed in 6.12.110
LINUX_IGNORE_CVES += CVE-2026-53024 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53025 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53078 # fixed in 6.12.101
LINUX_IGNORE_CVES += CVE-2026-53089 # fixed in 6.12.108
LINUX_IGNORE_CVES += CVE-2026-53090 # fixed in 6.12.101
LINUX_IGNORE_CVES += CVE-2026-53091 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53092 # fixed in 6.12.105
LINUX_IGNORE_CVES += CVE-2026-53109 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53118 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53178 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53179 # fixed in 6.12.95
LINUX_IGNORE_CVES += CVE-2026-53224 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53232 # fixed in 6.12.94
LINUX_IGNORE_CVES += CVE-2026-53250 # fixed in 6.12.110
LINUX_IGNORE_CVES += CVE-2026-53260 # fixed in 6.12.97
LINUX_IGNORE_CVES += CVE-2026-53277 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53281 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53284 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53330 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53368 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-53401 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-64036 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-64058 # fixed in 6.12.110
LINUX_IGNORE_CVES += CVE-2026-64068 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-64076 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-64077 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-64082 # fixed in 6.12.108
LINUX_IGNORE_CVES += CVE-2026-64117 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-64210 # fixed in 6.12.110
LINUX_IGNORE_CVES += CVE-2026-64216 # fixed in 6.12.106
LINUX_IGNORE_CVES += CVE-2026-64283 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-64388 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-64389 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-64400 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-98156 # fixed in 6.12.114
LINUX_IGNORE_CVES += CVE-2026-100072 # fixed in 6.12.114

# Code our kernel does not build: omap2plus_defconfig plus os/board/image/linux.fragment
# leaves the symbol off, or its dependencies make it unreachable. Checked against the CNA's
# program files and a kernel config generated from that defconfig and fragment.
LINUX_IGNORE_CVES += CVE-2020-25672 # NFC LLCP, fixed in 5.12: CONFIG_NFC off
LINUX_IGNORE_CVES += CVE-2020-27815 # JFS, fixed in 5.10.4: CONFIG_JFS_FS off
LINUX_IGNORE_CVES += CVE-2021-20194 # BPF getsockopt hook, fixed in 5.11: CONFIG_CGROUPS off, CONFIG_BPF_SYSCALL off
LINUX_IGNORE_CVES += CVE-2024-58015 # drivers/net/wireless/ath/ath12k: CONFIG_ATH12K off
LINUX_IGNORE_CVES += CVE-2025-71202 # drivers/iommu/iommu-sva.c: CONFIG_IOMMU_SVA off
LINUX_IGNORE_CVES += CVE-2025-71302 # drivers/gpu/drm/panthor: CONFIG_DRM off, CONFIG_DRM_PANTHOR off
LINUX_IGNORE_CVES += CVE-2026-31435 # fs/netfs: CONFIG_NETFS_SUPPORT off
LINUX_IGNORE_CVES += CVE-2026-31536 # fs/smb/server: CONFIG_SMB_SERVER off
LINUX_IGNORE_CVES += CVE-2026-46279 # memory allocation profiling: CONFIG_MEM_ALLOC_PROFILING off
LINUX_IGNORE_CVES += CVE-2026-52953 # drivers/iommu/intel: CONFIG_INTEL_IOMMU off
LINUX_IGNORE_CVES += CVE-2026-53246 # net/sctp: CONFIG_IP_SCTP off
LINUX_IGNORE_CVES += CVE-2026-63805 # drivers/crypto/nx: CONFIG_CRYPTO_DEV_NX off
LINUX_IGNORE_CVES += CVE-2026-63819 # fs/f2fs: CONFIG_F2FS_FS off
LINUX_IGNORE_CVES += CVE-2026-64066 # fs/netfs: CONFIG_NETFS_SUPPORT off
LINUX_IGNORE_CVES += CVE-2026-64067 # fs/netfs: CONFIG_NETFS_SUPPORT off
LINUX_IGNORE_CVES += CVE-2026-64160 # fs/9p, fs/afs: CONFIG_9P_FS off, CONFIG_AFS_FS off
LINUX_IGNORE_CVES += CVE-2026-64255 # drivers/net/wireless/intel/iwlwifi/mld: CONFIG_IWLWIFI off
LINUX_IGNORE_CVES += CVE-2026-64520 # drivers/firmware/arm_ffa: CONFIG_ARM_FFA_TRANSPORT off, CONFIG_ARM64 off
LINUX_IGNORE_CVES += CVE-2026-98115 # fs/smb/server: CONFIG_SMB_SERVER off
LINUX_IGNORE_CVES += CVE-2026-98118 # fs/netfs: CONFIG_NETFS_SUPPORT off

# Another architecture: the kernel is built for ARCH=arm (BR2_arm), never these.
LINUX_IGNORE_CVES += CVE-2025-21949 # arch/loongarch/mm: CONFIG_LOONGARCH off
LINUX_IGNORE_CVES += CVE-2026-23102 # arch/arm64/kernel: CONFIG_ARM64 off
LINUX_IGNORE_CVES += CVE-2026-31568 # arch/s390/mm: CONFIG_S390 off
LINUX_IGNORE_CVES += CVE-2026-46317 # arch/arm64/kvm: CONFIG_ARM64 off
LINUX_IGNORE_CVES += CVE-2026-63941 # arch/arm64/kvm: CONFIG_ARM64 off

# Built only as modules, and os/board/rootfs/post-build.sh deletes every module but the
# ST300's CPSW switch, its MDIO bus and its GMII selector.
LINUX_IGNORE_CVES += CVE-2026-64078 # x_tables, ip_tables, iptable_nat: CONFIG_NETFILTER_XTABLES=m, CONFIG_IP_NF_IPTABLES=m

# Introduced after 6.12 (the CNA's first affected release), so 6.12 never had the bug.
LINUX_IGNORE_CVES += CVE-2025-39905 # phylink lock: bug came in 6.14
LINUX_IGNORE_CVES += CVE-2026-31608 # ksmbd SMB Direct: bug came in 7.0, and CONFIG_SMB_SERVER off
LINUX_IGNORE_CVES += CVE-2026-31609 # SMB client SMB Direct: bug came in 7.0, and CONFIG_CIFS off

# Open in 6.12, with the maintainer's acceptance.
LINUX_IGNORE_CVES += CVE-2026-63858 # nf_tables, no 6.12 fix yet; needs CAP_NET_ADMIN, and there are no user namespaces (CONFIG_NAMESPACES off): maintainer accepted
LINUX_IGNORE_CVES += CVE-2026-62840 # local privilege escalation, Microsoft's record names no component or fix; production has no shell, login or SSH, so it needs code running on the speaker first: maintainer accepted

# libplist 2.4.0: 2017 records with no version range; both were fixed before libplist 2.0.0
# (upstream issues #86 and #88).
LIBPLIST_IGNORE_CVES += CVE-2017-5835 # fixed before 2.0.0 (upstream #86, #88)
LIBPLIST_IGNORE_CVES += CVE-2017-5836 # fixed before 2.0.0 (upstream #86, #88)

# GLib 2.82.5, the Buildroot LTS's, which patches and ignores CVE-2025-6052, CVE-2025-7039 and
# CVE-2025-14087 itself. Upstream fixed these three in 2.86.3 (CVE-2025-13601), 2.88.1
# (CVE-2026-58014) and 2.89 (CVE-2026-58016); the LTS's recipe and patches are for 2.82, so
# os/board/common/patches/libglib2 carries each upstream fix instead of a newer GLib.
LIBGLIB2_IGNORE_CVES += CVE-2025-13601 # fixed by our patch
LIBGLIB2_IGNORE_CVES += CVE-2026-58014 # fixed by our patch
LIBGLIB2_IGNORE_CVES += CVE-2026-58016 # fixed by our patch
# Integer overflow in protect_argv_string(), GLib's Windows spawn code (upstream #3668).
LIBGLIB2_IGNORE_CVES += CVE-2025-4056 # glib/gspawn-win32.c, Windows only (glib/meson.build): not built for Linux

# ffmpeg 9.0.2 (os/package/soundtap-ffmpeg): the RTSP redirect loop is still open upstream
# in 9.0.2; the package carries the upstream fix.
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-107696 # fixed by our patch
# NVD ends this one's range at the date "2025-01-13", not a version; its fix (d5873be,
# January 2025) is in 9.0.2.
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2025-25469 # fixed in 9.0.2
# libavfilter filters our ffmpeg leaves out (SOUNDTAP_FFMPEG_FILTERS: aresample volume bass
# aformat afade, built --disable-everything); all eight are fixed upstream in 9.1, published
# 2026-11-24 against every 9.0.
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-135734 # ffmpeg filter deflicker not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-135736 # ffmpeg filter deband not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-135737 # ffmpeg filter tmidequalizer not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-135738 # ffmpeg filter signalstats not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-135739 # ffmpeg filter scale not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-135740 # ffmpeg filter readeia608 not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-135742 # ffmpeg filters zmq, azmq not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-135743 # ffmpeg filter dynaudnorm not built
# libavformat code our ffmpeg leaves out, from the same 2026-11-24 batch, fixed upstream in
# 9.1: wavenc.c is the WAV and W64 muxers (we mux pcm_s16le only; the WAV demuxer is
# wavdec.c), rtmppkt.c only the RTMP protocols, imf_cpl.c only the IMF demuxer.
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-135722 # ffmpeg muxers wav, w64 not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-135723 # ffmpeg demuxer imf not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-135725 # ffmpeg protocols rtmp, rtmpe, rtmps, rtmpt, rtmpte, rtmpts not built
# libavcodec video code our ffmpeg leaves out: it decodes audio only and encodes pcm_s16le
# (SOUNDTAP_FFMPEG_DECODERS, _ENCODERS), and builds no bitstream filter. lcevcdec.c and
# cbs_sei.c are reached only through the H.264 and HEVC decoders.
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-125259 # ffmpeg encoder prores_ks not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-125262 # ffmpeg decoders h264, hevc not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-125181 # ffmpeg decoder flic not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-125196 # ffmpeg encoder liboapv not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-125260 # ffmpeg encoder exr not built
SOUNDTAP_FFMPEG_IGNORE_CVES += CVE-2026-125269 # ffmpeg decoders h264, hevc not built
