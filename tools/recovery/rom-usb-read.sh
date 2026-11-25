#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Read one NAND range of an sm2 board (WLA; the ST10 shares its SPL, unproven there) into a
# new directory over mask-ROM USB; never writes NAND. The ST20 needs its own SPL: not here.
#
#   tools/recovery/rom-usb-read.sh <name> <offset> <length> <output-dir>
#   tools/recovery/rom-usb-read.sh mtd7 0x780000 0x500000 ./mtd7
#
# It builds a RAM U-Boot that reads <length> bytes (at most 16 MiB) at <offset> and sends
# them back, boots it through the ROM with tools/recovery/rom-usb-loader, and checks the
# file against the CRC the board printed. <output-dir> must not exist yet.
#
# Environment, all optional:
#   ROM_USB_POWER_CYCLE       a command run as `CMD off` and `CMD on` to power-cycle the
#                             board (as rom-usb-restore.py --power-cycle), split on spaces.
#                             Unset: the script asks you to power the board on and waits
#                             60 s for the ROM.
#   ROM_USB_PROTECTED_BACKUP  a directory the output may never land in, such as the board's
#                             nand-backup-ssh.sh backup. Unset: no directory is protected
#                             beyond the rule that <output-dir> is new.
#   ROM_USB_REPO              the SoundTap checkout to build from. Unset: the one holding
#                             this script.
#
# Linux only: it needs lsusb, Docker with /dev/bus/usb, and GNU stat.
set -euo pipefail

usage() { echo "usage: $0 name offset length output-dir" >&2; exit 2; }
[[ "$#" = 4 ]] || usage
NAME=$1 OFF=$2 LEN=$3 OUT=$4
case "${NAME}" in *[!A-Za-z0-9._-]*|'') usage;; *) ;; esac
case "${OFF}:${LEN}" in 0x[0-9a-fA-F]*:0x[0-9a-fA-F]*) ;; *) usage;; esac
LEN_DEC=$((LEN))
[[ "${LEN_DEC}" -gt 0 ]] && [[ "${LEN_DEC}" -le $((0x1000000)) ]] || { echo "length must be 1..0x1000000" >&2; exit 1; }
REPO=${ROM_USB_REPO:-$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)}
POWER_CYCLE=${ROM_USB_POWER_CYCLE:-}
[[ -f "${REPO}/tools/recovery/rom-usb-loader/build.sh" ]] || REPO=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
test -f "${REPO}/tools/recovery/rom-usb-loader/build.sh" || { echo "SoundTap repo missing: ${REPO}" >&2; exit 1; }
test ! -e "${OUT}" || { echo "output already exists: ${OUT}" >&2; exit 1; }
PARENT=$(CDPATH= cd -P -- "$(dirname -- "${OUT}")" && pwd)
PROTECTED=${ROM_USB_PROTECTED_BACKUP:-}
if [[ -n "${PROTECTED}" ]]; then
  [[ ! -d "${PROTECTED}" ]] || PROTECTED=$(CDPATH= cd -P -- "${PROTECTED}" && pwd)
  case "${PARENT}/${OUT##*/}" in "${PROTECTED}"|"${PROTECTED}"/*) echo "refusing protected backup directory" >&2; exit 1;; *) ;; esac
fi
mkdir "${OUT}"
trap 'rm -rf "$OUT" "$ART"' EXIT
ART=$(mktemp -d)

WLA_UBOOT_OUT="${ART}" WLA_UBOOT_NAND_DUMP="${NAME}:${OFF}:${LEN}" "${REPO}/os/bootloader/build-wla-uboot.sh"
"${REPO}/tools/recovery/rom-usb-loader/build.sh" all
MLO_SHA=$(sha256sum "${ART}/MLO" | cut -d' ' -f1)
UBOOT_SHA=$(sha256sum "${ART}/u-boot.img" | cut -d' ' -f1)
FILE="${OUT}/${NAME}"
if [[ -n "${POWER_CYCLE}" ]]; then
  set -f
  # shellcheck disable=SC2086 # a command with its arguments, split on spaces
  ${POWER_CYCLE} off >/dev/null 2>&1
  sleep 6
  # shellcheck disable=SC2086 # a command with its arguments, split on spaces
  ${POWER_CYCLE} on >/dev/null 2>&1
  set +f
else
  echo "Power the board on into ROM USB now; waiting 60 s for 0451:6141." >&2
fi
for _ in $(seq 60); do lsusb | grep -q "0451:6141" && break; sleep 1; done
lsusb | grep -q "0451:6141" || { echo "board did not come back on bus" >&2; exit 1; }
timeout 900 docker run --rm --privileged -v /dev/bus/usb:/dev/bus/usb -v "${REPO}:/repo" \
  -v "${ART}:/art:ro" -v "${OUT}:/dump" -w /repo/tools/recovery/rom-usb-loader soundtap-rom-usb-loader:bookworm \
  ./rom-usb-loader --usb-spl --mlo-sha256 "${MLO_SHA}" --uboot-sha256 "${UBOOT_SHA}" \
  /art/MLO /art/u-boot.img --watch 900 --upload-dir /dump >"${OUT}/loader.log" 2>&1
test -f "${FILE}" && test ! -L "${FILE}" || { echo "dump file missing: ${FILE}" >&2; exit 1; }
test "$(stat -c%s "${FILE}" || true)" = "${LEN_DEC}" || { echo "dump length mismatch" >&2; exit 1; }
CRC=$(python3 -c "import zlib; print('%08x' % (zlib.crc32(open('${FILE}','rb').read()) & 0xffffffff))")
grep -a -F "uboot: DP-${NAME}-crc" "${OUT}/loader.log" >/dev/null || { echo "board dump CRC marker missing" >&2; exit 1; }
python3 - "${OUT}/loader.log" "${NAME}" "${CRC}" <<'PY' || { echo "board dump CRC mismatch" >&2; exit 1; }
import re
import sys

log, name, crc = sys.argv[1:]
data = open(log, encoding="utf-8", errors="replace").read()
before, marker, _ = data.rpartition(f"uboot: DP-{name}-crc")
start = before.rfind("uboot: crc32 for ")
if not marker or start < 0 or "==>" not in before[start:]:
    raise SystemExit(1)
output = "".join(line.partition("uboot: ")[2] for line in before[start:].splitlines()).split("==>", 1)[1]
raise SystemExit(not re.fullmatch(r"\s*" + r"\s*".join(crc) + r"\s*", output))
PY
trap - EXIT
echo "${FILE}: ${LEN_DEC} bytes, crc32 ${CRC}"
