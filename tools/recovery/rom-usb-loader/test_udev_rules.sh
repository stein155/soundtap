#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

set -eu

rules=$(dirname "$0")/70-soundtap-rom-usb.rules
grep -Fx 'ACTION=="add", SUBSYSTEM=="usb", ATTR{idVendor}=="0451", ATTR{idProduct}=="6141", MODE:="0660", GROUP:="soundtap-usb"' "${rules}" >/dev/null
grep -Fx 'ACTION=="add", SUBSYSTEM=="usb", ATTR{idVendor}=="0451", ATTR{idProduct}=="d022", MODE:="0660", GROUP:="soundtap-usb"' "${rules}" >/dev/null
printf '%s\n' 'udev rules are exact and least-privilege'
