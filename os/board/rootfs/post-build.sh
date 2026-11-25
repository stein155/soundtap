#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

set -eu

target=$1

# The WiLink Wi-Fi drivers and the Bluetooth transport are built in (linux.fragment, with
# TIInit_11.8.32.bts embedded). The only modules kept are the ST300's CPSW switch, its MDIO
# bus and GMII selector, which its init script loads; the rest of modules_install goes.
for kdir in "${target}"/lib/modules/*; do
	[ -d "${kdir}" ] || continue
	find "${kdir}" -name '*.ko*' ! -name 'ti_cpsw_new.ko*' ! -name 'cpsw-common.ko*' \
		! -name 'davinci_mdio.ko*' ! -name 'phy-gmii-sel.ko*' -delete
	find "${kdir}" -depth -type d -empty -delete
done

# The init script sets the clock to at least the build time when the speaker has neither a
# saved clock nor NTP yet, so TLS does not start in 1970.
printf '%s\n' "${SOURCE_DATE_EPOCH:?the build sets SOURCE_DATE_EPOCH}" >"${target}/etc/soundtap-build-epoch"

# TI's own WiLink8 init script, the one hci_ll asks for by name after it reads the chip
# version. linux-firmware carries only WiLink6/7 versions, so this comes from the pinned
# copy in this repository; a wrong or missing file means the adapter never appears.
bts="${BR2_EXTERNAL_SOUNDTAP_PATH:?}/upstream/ti-connectivity/TIInit_11.8.32.bts"
bts_sha=f7dfa9d8072eacf492414fabd35fb6287b18eb0c40bad8022d4aa0802dedfb78
if [ "$(sha256sum "${bts}" | cut -d ' ' -f 1 || true)" != "${bts_sha}" ]; then
	echo "post-build: TIInit_11.8.32.bts is missing or has the wrong digest" >&2
	exit 1
fi
install -D -m 0644 "${bts}" "${target}/lib/firmware/ti-connectivity/TIInit_11.8.32.bts"

# The ST10's UART3 is not brought out anywhere reachable on this hardware, so a boot that
# fails before Wi-Fi comes up would leave nothing to read; the WLA is the same. The script
# itself is model-aware and brings up RNDIS
# on 192.168.7.2 plus an ACM console, without ever blocking init when no host is attached.
install -D -m 0755 "${BR2_EXTERNAL_SOUNDTAP_PATH:?}/overlay/etc/init.d/gadget-console" \
	"${target}/etc/init.d/gadget-console"

# The ALSA configs the init script picks from: the source bus every mainline boot selects
# (AUDIO_MIX=1), and the WLA's fallback when its card is missing.
for conf in asound.conf.bus asound.conf.wla; do
	install -D -m 0644 "${BR2_EXTERNAL_SOUNDTAP_PATH:?}/overlay/etc/${conf}" "${target}/etc/${conf}"
done

# Both trees, in every image. Stock U-Boot loads /boot/${variant}_${module_type}.dtb out of
# the slot it selected, so the file it asks for is what decides the model -- and an image
# carrying both is an image either speaker can boot. That, with a kernel that has no
# appended tree and no built-in command line, is what one package needs.
install -D -m 0644 "${BINARIES_DIR:?}/am335x-soundtap-st10.dtb" "${target}/boot/rhino_sm2.dtb"
install -D -m 0644 "${BINARIES_DIR:?}/am335x-soundtap-wla.dtb" "${target}/boot/binky_sm2.dtb"

# Release profile (ADR 0011). os/configs/production.fragment deselects dropbear and root
# login; what else separates the profiles is taken out of the tree here, so the image
# says which one it is in /etc/soundtap/profile and rootfs_check.py --profile can hold it
# to that.
if grep -qx 'BR2_PACKAGE_DROPBEAR=y' "${BR2_CONFIG:?}"; then
	profile=alpha
else
	profile=production
	# No SSH and nothing that names it (ADR 0119), avahi's default ssh adverts included.
	rm -f "${target}/opt/soundtap/ssh.sh" "${target}/etc/default/dropbear" "${target}"/etc/avahi/services/*ssh.service
	sed -i '/^ttyGS0:.*\/bin\/sh/d' "${target}/etc/inittab"
	# Default-deny input (ADR 0066), before gadget-console brings up the first interface.
	install -D -m 0644 "${BR2_EXTERNAL_SOUNDTAP_PATH:?}/board/rootfs/firewall.nft" \
		"${target}/etc/soundtap/firewall.nft"
	sed -i '1i ::sysinit:/usr/sbin/nft -f /etc/soundtap/firewall.nft' "${target}/etc/inittab"
fi
install -d "${target}/etc/soundtap"
echo "${profile}" >"${target}/etc/soundtap/profile"

# The ST20's BCO image, built from our code alone (ADR 0031); soundtapd flashes it once per
# image. Every image carries it: the same rootfs boots every model.
install -D -m 0644 "${BINARIES_DIR:?}/bco-app/link-own/app.bcd" "${target}/usr/share/soundtap/bco-app.bcd"

# / is mounted read-only. The files the boot writes are links into
# /run, filled by init.d/soundtap, and BlueZ's and bluealsa's state are links into /mnt/nv
# (bluealsa keeps each device's A2DP volume there).
install -d "${target}/mnt/nv"
ln -sfn /run/soundtap/asound.conf "${target}/etc/asound.conf"
for state in bluetooth bluealsa; do
	rm -rf "${target}/var/lib/${state}"
	ln -s "/mnt/nv/${state}" "${target}/var/lib/${state}"
done
