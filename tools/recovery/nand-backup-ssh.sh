#!/bin/bash
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Take a full, OOB-inclusive backup of a speaker's NAND over ssh.
#
# Needs root ssh, so an alpha SoundTap image: a production image has no SSH (ADR 0119).
# All thirteen partitions are byte-identical across the three models, so one table serves
# all of them.
#
# Run this BEFORE anything is written to a speaker that has never been backed up.
# Mask-ROM USB boot gets a bricked board running again on all three models (the ST20 only
# with its boot-select pad held, docs/hardware/st20/rom-usb-recovery.md; CONTRIBUTING.md), but that only helps
# if there is a backup of this board's own NAND to write back.
#
#   tools/recovery/nand-backup-ssh.sh <ssh-target> <out-dir>
#   tools/recovery/nand-backup-ssh.sh root@192.0.2.135 ~/nand-backups/st10-rhino-20260919
#
# Keep every board's backup in one place (the example uses ~/nand-backups) so a restore
# never depends on remembering which working directory a dump was taken in.
#
# SAFETY: every command this script runs on the speaker reads. `nanddump -o
# --bb=padbad` includes the OOB area so a restore can put back bad-block markers and
# ECC exactly as they were. Never use flash_erase, including for a bad-block audit.
set -euo pipefail

TARGET=${1:?usage: nand-backup-ssh.sh <ssh-target> <out-dir>}
OUT=${2:?usage: nand-backup-ssh.sh <ssh-target> <out-dir>}
SSH=${SSH:-ssh}
PAGE=2048
OOB=64

# The stock partition table, measured identical on all three models. Sizes are the
# partition sizes in bytes; a dump is (PAGE+OOB)/PAGE of that because of the OOB.
SIZES="131072 131072 131072 131072 1966080 131072 5242880 5242880 100663296 100663296 39845888 12058624 262144"
NAMES="SPL SPL.backup1 SPL.backup2 SPL.backup3 U-Boot U-Boot-Env Kernel-Ping Kernel-Pong File-System-Ping File-System-Pong Persistent-Storage Update-Staging Manufacturing-Data"

mkdir -p "${OUT}"

# Read the speaker's own table first and keep it. If this speaker disagrees with the
# table above, that is the discovery, and dumping against the wrong sizes would hide
# it -- so stop rather than truncate a partition into a file that looks complete.
${SSH} "${TARGET}" cat /proc/mtd >"${OUT}/proc-mtd.txt"
cat "${OUT}/proc-mtd.txt"
count=$(grep -c '^mtd' "${OUT}/proc-mtd.txt" || true)
expected=$(echo "${SIZES}" | wc -w | tr -d " ")
if [[ "${count}" != "${expected}" ]]; then
	echo "speaker reports ${count} partitions, table has ${expected}; refusing to guess" >&2
	exit 1
fi

# Check every size before reading anything. A mismatch part-way through would leave a
# directory that looks like a backup and is not one.
index=0
for size in ${SIZES}; do
	name=$(echo "${NAMES}" | cut -d' ' -f $((index + 1)))
	want=$(printf '%08x' "${size}")
	have=$(awk -v n="mtd${index}:" '$1==n {print $2}' "${OUT}/proc-mtd.txt")
	if [[ "${have}" != "${want}" ]]; then
		echo "mtd${index} ${name}: speaker says size ${have}, table says ${want}" >&2
		exit 1
	fi
	index=$((index + 1))
done

index=0
for size in ${SIZES}; do
	name=$(echo "${NAMES}" | cut -d' ' -f $((index + 1)))
	file=${OUT}/mtd${index}-${name}.oob
	# shellcheck disable=SC2017 # whole pages times the page size with its OOB; the truncation is the point
	expect=$((size / PAGE * (PAGE + OOB)))
	if [[ -f "${file}" ]] && [[ "$(wc -c <"${file}" | tr -d " " || true)" = "${expect}" ]]; then
		# The right size is not proof of a good dump: reuse it only when it still
		# matches the checksum recorded when it was read. A mismatch is evidence that
		# something changed it; keep it and stop rather than certify it.
		base=./$(basename "${file}")
		recorded=$(cat "${OUT}/SHA256SUMS" "${OUT}/.sums-in-progress" 2>/dev/null |
			awk -v f="${base}" '$2==f {print $1; exit}' || true)
		if [[ -n "${recorded}" ]]; then
			if [[ "$(sha256sum "${file}" | cut -d' ' -f1 || true)" != "${recorded}" ]]; then
				echo "mtd${index} ${name}: ${file} no longer matches its recorded checksum; move it aside and run again" >&2
				exit 1
			fi
			echo "mtd${index} ${name}: already have ${expect} bytes, checksum ok"
			index=$((index + 1))
			continue
		fi
		echo "mtd${index} ${name}: no recorded checksum for the existing file; reading again"
	fi
	echo "mtd${index} ${name}: reading ${expect} bytes"
	${SSH} "${TARGET}" "nanddump -o --bb=padbad /dev/mtd${index}" >"${file}"
	got=$(wc -c <"${file}" | tr -d " ")
	if [[ "${got}" != "${expect}" ]]; then
		echo "mtd${index} ${name}: got ${got}, expected ${expect}" >&2
		exit 1
	fi
	# Recorded now, so an interrupted run can resume without trusting sizes.
	(cd "${OUT}" && sha256sum "./$(basename "${file}")" >>.sums-in-progress)
	index=$((index + 1))
done

(cd "${OUT}" && sha256sum ./*.oob proc-mtd.txt >SHA256SUMS && rm -f .sums-in-progress)
echo
echo "backup complete in ${OUT}"
cat "${OUT}/SHA256SUMS"
