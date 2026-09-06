#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

set -eu

source=$1

grep -Fqx '#define SPL_USB_PID AM335X_USB_PID' "$source"
grep -Fqx '#define UBOOT_USB_PID 0xd022' "$source"
grep -Fq 'stage_two = number == 2;' "$source"
grep -Fq 'expected_usb_pid = stage_usb_pid(number);' "$source"
grep -Fq 'needs_rom_departure(usb_spl)' "$source"
grep -Fqx '#define SPL_HANDOFF_WAIT_MS 60000' "$source"
grep -A 8 -F 'wait_for_stage_disconnect(struct rndis_device *rndis)' "$source" |
    grep -Fq 'sizeof(buffer), &actual, SPL_HANDOFF_WAIT_MS);'
grep -Fq 'device_wait_ms = SPL_HANDOFF_WAIT_MS;' "$source"
printf '%s\n' 'loader SPL handoff timing test passed'
