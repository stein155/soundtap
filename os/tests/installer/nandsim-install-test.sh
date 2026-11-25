#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Install a .stu into a simulated NAND and prove what a speaker would be left with.
# This is layer 1 of the install gate (docs/contributors/testing/install-gate.md).
#
# The install path is the one thing about SoundTap that cannot be checked by
# reading code: unit tests can prove which commands run in which order, but not
# that the bytes those commands write add up to a slot a speaker could boot. This
# harness answers that without a spare speaker.
#
# The flash side of the install is not ARM-specific. nandsim gives a real MTD device
# with the NAND geometry of a speaker -- 256 MiB, 2048-byte pages, 128 KiB
# eraseblocks, 64 bytes of OOB -- carved into the stock partition table, and
# mtd-utils and libubootenv then do the same work they do on the speaker. What is
# emulated is the flash chip, not the installer: internal/stu streams into
# internal/flash exactly as it does behind POST /api/firmware?install=1.
#
# nandsim is a kernel module, so this runs in a QEMU guest rather than a container:
# the host kernel has no MTD stack, and loading modules into it would be the wrong
# thing to do even if it did. The packages, the kernel and QEMU are fetched and the
# initramfs is built once per run; then one guest per scenario boots from it, all at
# the same time.
#
# Per scenario the guest proves:
#   - a truncated package is refused and leaves the alternate kernel erased;
#   - optionally a power cut in the middle of the rootfs write (soundtapd and the
#     flash tools SIGKILLed): the slot is not bootable, nothing else moved, and the
#     next good install repairs it;
#   - after the install: the U-Boot environment (read with fw_printenv) selects the
#     installed slot, falls back to the running one and changes
#     nothing else; the kernel partition holds the image's kernel bytes; the new
#     UBI volume is byte-identical to the one in the image; it mounts and holds
#     what slot_boot loads; every other partition, the running slot and
#     NAND.Manufacturing-Data included, is byte-identical to before.
#
# Usage: os/tests/installer/nandsim-install-test.sh <package.stu> [more.stu ...]
#
# Env:
#   SCENARIOS   space-separated model/slot/badblocks[/flag...] (default: st10/8/none)
#                 model      st10 | wla | st20 | st300
#                 slot       8 or 9: the slot the guest pretends to be running from
#                            (8/PING installs into PONG; 9 is the mirror image)
#                 badblocks  none | some | eraseblock numbers joined with + . "some"
#                            is a fixed set in the target slot and next to the
#                            environment: real NAND in a five-year-old speaker has
#                            bad blocks, and an installer that cannot write around
#                            them works only on a clean chip.
#                 cut        literal word "cut": add the power-cut case
#   MODEL, SLOT, BADBLOCKS   one scenario in the old spelling (BADBLOCKS comma-separated)
#   NODE_IMAGE  image that builds the web UI (default node:24)
# Works from any directory; scratch files go to output/nandsim.* in this checkout.
set -eu

test "$#" -ge 1 || { echo "usage: nandsim-install-test.sh <package.stu> [more.stu ...]" >&2; exit 1; }
if [ -z "${SCENARIOS:-}" ]; then
  SCENARIOS="${MODEL:-st10}/${SLOT:-8}/$(printf '%s' "${BADBLOCKS:-none}" | tr , +)"
fi
for f in "$@"; do
  test -f "${f}" || { echo "no such package: ${f}" >&2; exit 1; }
done
REPO=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)
# The work directory is bind-mounted into containers, so it sits in the checkout, which the
# docker daemon sees wherever it runs. A mktemp in the host's /tmp worked only where the
# daemon shares that /tmp, so each caller had to set TMPDIR into the checkout itself.
mkdir -p "${REPO}/output"
# shellcheck disable=SC2250 # os/tests/installer/test_nandsim_install.py matches this text as written
WORK=$(mktemp -d "$REPO/output/nandsim.XXXXXX")
# A step timeout kills the docker client, not the guest container, so qemu kept running and
# slowed every later job on the shared host. One name per runner: a runner runs one job at a
# time, so a container with its name is always a leftover of its own previous job.
# A hosted runner is called "GitHub Actions 12"; docker takes no space in a name.
GUEST=soundtap-nandsim-guest-$(printf %s "${RUNNER_NAME:-local-$$}" | tr -c 'a-zA-Z0-9_.-' -)
docker rm -f "${GUEST}" >/dev/null 2>&1 || true
trap 'docker rm -f "$GUEST" >/dev/null 2>&1; rm -rf "$WORK"' EXIT
GO_IMAGE=golang:1.26@sha256:37cfcca63222ec499bec5e8ad7aad52dbf7529db36d5812b8eae2f0621aa92d6
NODE_IMAGE=${NODE_IMAGE:-node:24@sha256:9d266751ca3d7fdbe651c878ffd5ad0a42fed49a18c283509267c97f025b7496}
# Ubuntu, not Debian: Debian's arm64 kernel builds no nandsim, Ubuntu's ships it in
# linux-modules-extra on both architectures. The guest runs the runner's own arch.
RUNNER_IMAGE=ubuntu:24.04@sha256:68ee22004a9109b44973313b02ff7b1de250f1de0f5ecb628d73f579aaff33ac
# Every package the guest installs comes from this frozen state of the Ubuntu archive
# (https://snapshot.ubuntu.com), so the kernel, QEMU and mtd-utils are the same bytes
# on every run until someone moves the date.
APT_SNAPSHOT=20261020T000000Z
case $(docker info --format '{{.Architecture}}') in
	x86_64|amd64) GUEST_ARCH=amd64; LIB_ARCH=x86_64; GOARCH=amd64 ;;
	aarch64|arm64) GUEST_ARCH=arm64; LIB_ARCH=aarch64; GOARCH=arm64 ;;
	*) echo "nandsim gate needs an amd64 or arm64 runner" >&2; exit 2 ;;
esac
# KVM when the runner has it, plain emulation otherwise: slower, same result. Asked of
# docker itself, because the daemon may be allowed a device this user is not.
KVM=
# Rootless docker maps container root to us already; a chown to our uid would map
# to a subuid we cannot delete.
HOST_UID=$(id -u || true) HOST_GID=$(id -g)
docker info --format '{{.SecurityOptions}}' | grep -q rootless && HOST_UID=0 HOST_GID=0
if [ -c /dev/kvm ] && docker run --rm --device /dev/kvm "${RUNNER_IMAGE}" true >/dev/null 2>&1; then
  KVM="--device /dev/kvm"
fi

echo "== build soundtapd (production HTTP install path) and derive what the install must leave"
# In a copy, as `make web` does: no node_modules or root-owned dist in the checkout, and a
# test run beside this one keeps the embed directory it compiled against.
docker run --rm --user "${HOST_UID}:${HOST_GID}" -e HOME=/tmp -v "${REPO}/app":/src:ro -v "${WORK}":/out -w /tmp "${NODE_IMAGE}" \
  sh -euc 'mkdir -p /tmp/internal/web && cp /src/internal/web/openapi.json /tmp/internal/web/ &&
    cp -a /src/frontend /tmp/frontend && cd /tmp/frontend && rm -rf node_modules &&
    npm ci --silent && SOUNDTAP_WEB_OUT_DIR=/out/web npm run build'
n=0
PKGS=""
for f in "$@"; do
  n=$((n + 1))
  cp "${f}" "${WORK}/update${n}.stu"
  PKGS="${PKGS} /update${n}.stu"
done
# The expectation comes from the last package (what ends up in the slot); the first
# one only tells the power-cut case where its rootfs starts.
# Docker cannot create the dist mountpoint inside the read-only /src; a fresh checkout lacks it.
mkdir -p "${REPO}/app/internal/web/dist"
docker run --rm --user "${HOST_UID}:${HOST_GID}" -e HOME=/tmp -e GOPATH=/tmp/go -e GOCACHE=/tmp/gocache \
  -e GOARCH="${GOARCH}" -v "${REPO}/app":/src:ro -v "${WORK}/web":/src/internal/web/dist:ro -v "${WORK}":/out \
  -v "${REPO}/os/support":/repo/os/support:ro -v "${REPO}/os/board":/repo/os/board:ro -w /src "${GO_IMAGE}" \
  sh -euc 'CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/soundtapd ./cmd/soundtapd
    python3 /repo/os/support/stu_expect.py /out/update1.stu rhino binky spotty ginger >/out/first.env
    grep -E "^EXP_(ROOTFS_OFF|ROOTFS_LEN)" /out/first.env | sed "s/^EXP_/FIRST_/" >/out/expect.env
    python3 /repo/os/support/stu_expect.py /out/update'"${n}"'.stu rhino binky spotty ginger >>/out/expect.env
    env -u GOARCH go run ./internal/ubootenv/envfixture/gen >/out/factory-env.bin'

echo "== assemble the guest (scenarios: ${SCENARIOS})"
{
  echo "PKGS=\"${PKGS}\""
  echo "SCENARIOS=\"${SCENARIOS}\""
  cat "${WORK}/expect.env"
} >"${WORK}/guest.env"
cat >"${WORK}/init" <<'GUEST'
#!/bin/sh
# PID 1 in the guest. Everything it needs is in the initramfs; there is no disk.
set -eu
mount -t proc none /proc
mount -t sysfs none /sys
mount -t devtmpfs none /dev
# soundtapd maps the RTC through /dev/mem on a speaker; here that would be guest RAM.
rm -f /dev/mem
mkdir -p /tmp /mnt /mnt/new
mount -t tmpfs none /tmp   # the installer stages the 3 MB kernel here
mount -t tmpfs none /run
ip link set lo up
. /guest.env
# One guest per scenario: the host names it on the kernel command line.
for a in $(cat /proc/cmdline); do case $a in sc=*) SCENARIOS=${a#sc=} ;; esac; done
modprobe ubi
for m in zstd lzo deflate; do modprobe "$m" 2>/dev/null || true; done
modprobe ubifs

SC=boot
fail() {
  echo "FAIL [$SC]: $*" >&2
  tail -40 /tmp/soundtapd.log 2>/dev/null || true
  exit 1
}
trap 'rc=$?; [ "$rc" = 0 ] || echo "guest stopped with status $rc in [$SC]" >&2' EXIT

# The environment as libubootenv sees it, with the lines soundtapd writes for it
# (internal/flash envConfig): our U-Boot's redundant environment (ADR 0010), two 32 KiB
# copies, the primary at the start of NAND.U-Boot-Env (mtd5 on every model) and the other
# in the last eraseblock of NAND.U-Boot (mtd4). The seed is the single 8 KiB copy a
# speaker had before, which the first write converts; reading falls back to it.
CFG=/run/fw_env.config
CFG1=/run/fw_env1.config
printf '/dev/mtd5 0x0 0x8000 0x20000 1\n/dev/mtd4 0x1c0000 0x8000 0x20000 1\n' >"$CFG"
echo "/dev/mtd5 0x0 0x2000 0x20000 1" >"$CFG1"
# libubootenv refuses both copies when one sits on a bad eraseblock; U-Boot boots the other
# (ADR 0010). UCFG reads the copies as U-Boot does: dumped to files, a bad block as 0xff,
# which checks out as no copy.
UCFG=/run/fw_env.uboot.config
printf '/run/env0.bin 0x0 0x8000 0x8000 1\n/run/env1.bin 0x0 0x8000 0x8000 1\n' >"$UCFG"
ucopies() {
  nanddump --omitoob --bb=padbad -l $((0x8000)) -f /run/env0.bin /dev/mtd5 2>/dev/null
  nanddump --omitoob --bb=padbad -s $((0x1c0000)) -l $((0x8000)) -f /run/env1.bin /dev/mtd4 2>/dev/null
}
envcfg() {  # no redundant copy checks out: the single one
  if fw_printenv -c "$CFG" >/dev/null 2>&1; then echo "$CFG"; return; fi
  ucopies
  if fw_printenv -c "$UCFG" >/dev/null 2>&1; then echo "$UCFG"; else echo "$CFG1"; fi
}
penv() { fw_printenv -c "$(envcfg)" | sort; }
eget() { fw_printenv -c "$(envcfg)" -n "$1" 2>/dev/null || true; }

# Partition table in eraseblocks (128 KiB) of the stock table up to
# Manufacturing-Data, as the device trees carry it (os/board/common/dts, shared by
# all three models); nandsim gives the remainder to a final unnamed partition.
PARTS=1,1,1,1,15,1,40,40,768,768,304,92,2
# 0-3 SPL copies, 4 U-Boot (blocks 4-18), 5 env (19), 6 Kernel-Ping (20-59),
# 7 Kernel-Pong (60-99), 8 FS-Ping (100-867), 9 FS-Pong (868-1635), 10 Persistent,
# 11 Update-Staging, 12 Manufacturing-Data.

model_setup() {
  case $1 in
    # Micron MT29F2G08, 2 KiB pages, 128 KiB blocks, 64 B OOB. Geometry per the
    # stock table; the ID bytes are the 3.3 V part the harness has always used.
    st10) VARIANT=rhino; MTYPE=sm2; DTB=rhino_sm2.dtb
      ID="first_id_byte=0x2c second_id_byte=0xda third_id_byte=0x90 fourth_id_byte=0x95" ;;
    # READID 2c aa 90 15 06 read off the WLA:
    # the 1.8 V part. nandsim takes four ID bytes.
    wla) VARIANT=binky; MTYPE=sm2; DTB=binky_sm2.dtb
      ID="first_id_byte=0x2c second_id_byte=0xaa third_id_byte=0x90 fourth_id_byte=0x15" ;;
    # The repo documents the ST20 as the same 256 MiB chip and table as the ST10
    # (os/tests/fixtures/st20-*/hardware-facts.md) but holds no READID for it.
    st20) VARIANT=spotty; MTYPE=scm; DTB=spotty_scm.dtb
      ID="first_id_byte=0x2c second_id_byte=0xda third_id_byte=0x90 fourth_id_byte=0x95" ;;
    # READID 2c aa read off the ST300 (dmesg, 2026-11-07), with the WLA's partition table.
    st300) VARIANT=ginger; MTYPE=sm2; DTB=ginger_sm2.dtb
      ID="first_id_byte=0x2c second_id_byte=0xaa third_id_byte=0x90 fourth_id_byte=0x15" ;;
    *) fail "unknown model $1" ;;
  esac
}

# mtd4 without its last eraseblock: that one holds an environment copy, which penv checks.
nhash() {
  if [ "$1" = 4 ]; then set -- 4 -l $((0x1c0000)); fi
  m=$1; shift
  nanddump --omitoob --bb=skipbad "$@" "/dev/mtd$m" 2>/dev/null | sha256sum | cut -c1-64
}
# Everything an install must not touch, by content.
snap() {
  for m in 0 1 2 3 4 "$RUNK" "$RUNR" 10 11 12; do echo "mtd$m $(nhash "$m")"; done
}
rand() { dd if=/dev/urandom of="$2" bs=1k count="$1" 2>/dev/null; }

post() {
  size=$(wc -c <"$1")
  # The install waits for a key press unless it carries the one-time token.
  tok=$(cat /run/soundtap/confirm-token 2>/dev/null || true)
  { printf 'POST /api/firmware?install=1 HTTP/1.1\r\nHost: localhost\r\nX-SoundTap-Device-Token: %s\r\nX-SoundTap-Confirm: install\r\nContent-Type: application/octet-stream\r\nContent-Length: %s\r\nConnection: close\r\n\r\n' "$tok" "$size"; cat "$1"; } \
    | nc 127.0.0.1 8080 >"$2"
}

start_daemon() {
  # Fresh home means no local password. The client is loopback, as it is on-speaker.
  # Unsigned on purpose: the package under test is the build's own, not a release. It
  # installs on the press, which the one-time token stands in for, on any profile (ADR 0048).
  mkdir -p /tmp/soundtap /etc/soundtap /run/soundtap
  echo alpha >/etc/soundtap/profile # the image under test is an alpha build
  # The OS level the image would provide (ADR 0035): exactly what this app needs.
  /soundtapd -os-api >/etc/soundtap/os-api
  # Own session: a power cut takes the whole group.
  SOUNDTAP_FIRMWARE=1 setsid /soundtapd -home /tmp/soundtap -listen 127.0.0.1:8080 -no-audio >>/tmp/soundtapd.log 2>&1 &
  DPID=$!
  ready=0
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    wget -q -O /dev/null http://127.0.0.1:8080/api/now && { ready=1; break; }
    sleep 1
  done
  [ "$ready" = 1 ] || fail "soundtapd did not come up"
}
stop_daemon() {
  kill -9 -- "-$DPID" 2>/dev/null || true
  killall -9 ubiformat flash_erase nandwrite 2>/dev/null || true
  sleep 1
}

# The alternate kernel must not be a kernel any more: the signature field at offset 32
# of the uImage is what a stock U-Boot reads, and an erased block is all 0xff.
assert_slot_unbootable() {
  sig=$(nanddump --omitoob --bb=skipbad -l 2048 "/dev/mtd$ALTK" 2>/dev/null | dd bs=1 skip=32 count=5 2>/dev/null | od -An -tx1)
  [ "$sig" = " ff ff ff ff ff" ] || fail "$1: alternate kernel is still a kernel (signature bytes:$sig)"
  nanddump --omitoob --bb=skipbad -l 64 "/dev/mtd$ALTK" 2>/dev/null | grep -Eq 'Linux|Linub' && fail "$1: alternate kernel is bootable"
  penv >/tmp/env.now
  cmp -s /tmp/env.base /tmp/env.now || { diff /tmp/env.base /tmp/env.now >&2 || true; fail "$1: the environment changed, the slot could be selected"; }
  snap >/tmp/snap.now
  cmp -s /tmp/snap.base /tmp/snap.now || { diff /tmp/snap.base /tmp/snap.now >&2 || true; fail "$1: a partition that must not move changed"; }
  return 0
}

# A power cut while the rootfs is being written. The upload stops half way through the
# rootfs module with the connection left open, so ubiformat has written exactly what it
# was given and is waiting for the rest; then everything is SIGKILLed at once.
power_cut() {
  cut=$((FIRST_ROOTFS_OFF + FIRST_ROOTFS_LEN / 2))
  size=$(wc -c <"$FIRST")
  tok=$(cat /run/soundtap/confirm-token 2>/dev/null || true)
  hdr="POST /api/firmware?install=1 HTTP/1.1\r\nHost: localhost\r\nX-SoundTap-Device-Token: $tok\r\nX-SoundTap-Confirm: install\r\nContent-Type: application/octet-stream\r\nContent-Length: $size\r\nConnection: close\r\n\r\n"
  setsid sh -c "{ printf '$hdr'; head -c $cut $FIRST; sleep 3600; } | nc 127.0.0.1 8080 >/tmp/cut.http" &
  UPID=$!
  # Wait for the write to have got well into the image: an eraseblock a bit short of the
  # half way mark carries a UBI erase-counter header only once ubiformat reached it.
  probe=$((FIRST_ROOTFS_LEN / 2 / 131072 - 20))
  seen=0
  for _ in $(seq 1 300); do
    magic=$(nanddump --omitoob --bb=skipbad -s $((probe * 131072)) -l 2048 "/dev/mtd$ALTR" 2>/dev/null | head -c 4)
    [ "$magic" = "UBI#" ] && { seen=1; break; }
    sleep 1
  done
  [ "$seen" = 1 ] || fail "power cut: the rootfs write never got half way"
  sleep 2
  kill -9 -- "-$UPID" 2>/dev/null || true
  stop_daemon
  echo "power cut: soundtapd and the flash tools killed with the rootfs half written"
  assert_slot_unbootable "after the power cut"
  # What a speaker does after power returns: soundtapd starts again.
  start_daemon
}

scenario() {
  model=$1 slot=$2 bb=$3; shift 3
  SC="$model/$slot/$bb" cut=
  for f in "$@"; do
    case $f in cut) cut=1 ;; *) fail "unknown scenario flag $f" ;; esac
    SC="$SC/$f"
  done
  t0=$(date +%s)
  echo "== scenario $SC"
  model_setup "$model"
  # The block map of the two slots, in eraseblocks (see PARTS).
  if [ "$slot" = 8 ]; then
    RUNK=6 RUNR=8 ALTK=7 ALTR=9 TARGET=PONG RUNNING=PING TCMD="run nand_boot2;" RCMD="run nand_boot;" TARGS=nand_args2 OARGS=nand_args
    SOME="18 20 61 870 1000 1500"
  elif [ "$slot" = 9 ]; then
    RUNK=7 RUNR=9 ALTK=6 ALTR=8 TARGET=PING RUNNING=PONG TCMD="run nand_boot;" RCMD="run nand_boot2;" TARGS=nand_args OARGS=nand_args2
    SOME="18 60 21 102 500 860"
  else
    fail "slot must be 8 or 9"
  fi
  case $bb in
    none) BBARG= ;;
    some) BBARG="badblocks=$(echo $SOME | tr ' ' ,)" ;;
    *) BBARG="badblocks=$(echo "$bb" | tr + ,)" ;;
  esac
  # A bad block 18 or 19 holds an environment copy: the other one carries the environment.
  ENVCFG=$CFG
  case ",${BBARG#badblocks=}," in *,18,*|*,19,*) ENVCFG=$UCFG ;; esac

  umount /proc/mtd 2>/dev/null || true
  umount /proc/cmdline 2>/dev/null || true
  rmmod nandsim 2>/dev/null || true
  # shellcheck disable=SC2086
  modprobe nandsim $ID parts=$PARTS $BBARG
  # The partition names come from the device tree on a speaker; nandsim numbers them
  # "NAND simulator partition N". soundtapd matches by name, so give it the names the
  # real table has -- this is the mapping under test, not something to be lenient about.
  awk 'BEGIN {
    split("NAND.SPL NAND.SPL.backup1 NAND.SPL.backup2 NAND.SPL.backup3 NAND.U-Boot " \
          "NAND.U-Boot-Env NAND.Kernel-Ping NAND.Kernel-Pong NAND.File-System-Ping " \
          "NAND.File-System-Pong NAND.Persistent-Storage NAND.Update-Staging " \
          "NAND.Manufacturing-Data", name, " ")
    i = 0
  }
  /^dev:/ { print; next }
  /^mtd/ { i++; if (i <= 13) { sub(/".*"/, "\"" name[i] "\""); } print }' /proc/mtd >/run/mtd
  mount --bind /run/mtd /proc/mtd
  cat /proc/mtd
  # ubi.mtd= is the lie that makes the test real: soundtapd believes it booted from that
  # slot, so a correct installer must write the other one -- and never touch the
  # partitions behind the one it claims to be running. variant and module_type name the model.
  # soundtap.env=redundant is what our U-Boot adds: it reads the redundant environment.
  echo "console=ttyS0 root=ubi0:rootfs ubi.mtd=$slot,2048 variant=$VARIANT module_type=$MTYPE panic=1 soundtap.env=redundant" >/run/cmdline
  mount --bind /run/cmdline /proc/cmdline

  # A speaker that has been used: bootloaders, a kernel and root filesystem in the
  # running slot, persistent data and manufacturing data are not blank, so a stray
  # erase or write anywhere shows. The environment is the pre-install fixture
  # (internal/ubootenv/envfixture), pointing at the running slot.
  for m in 0 1 2 3 4 10 12; do
    # at most 256 KiB, and never more than the partition holds (mtd0-3 are one eraseblock)
    k=$(($(cat "/sys/class/mtd/mtd$m/size") / 1024)); [ "$k" -le 256 ] || k=256
    rand "$k" /tmp/seed; nandwrite -p "/dev/mtd$m" /tmp/seed >/dev/null
  done
  rand 1024 /tmp/seed; nandwrite -p "/dev/mtd$RUNK" /tmp/seed >/dev/null
  rand 4096 /tmp/seed; nandwrite -p "/dev/mtd$RUNR" /tmp/seed >/dev/null
  nandwrite -p /dev/mtd5 /factory-env.bin >/dev/null
  echo "bootcmd=$RCMD" >/tmp/envset
  fw_setenv -c "$CFG1" -s /tmp/envset
  penv >/tmp/env.base
  [ "$(eget bootcmd)" = "$RCMD" ] || fail "seeded environment does not select the running slot"
  snap >/tmp/snap.base

  rm -rf /tmp/soundtap
  : >/tmp/soundtapd.log
  start_daemon
  FIRST=${PKGS# }; FIRST=${FIRST%% *}

  # A truncated upload reaches rootfs then fails. HTTP must reject it before its
  # erased alternate kernel can make the half-written slot bootable.
  dd if="$FIRST" of=/broken.stu bs=1M count=20 2>/dev/null
  post /broken.stu /tmp/broken.http
  if grep -q '^HTTP/1.1 2' /tmp/broken.http; then fail "truncated package was accepted"; fi
  assert_slot_unbootable "after the truncated package"

  [ -z "$cut" ] || power_cut

  # Every package in turn, into the same slot: the second install also proves an
  # install over a slot that already holds one, which is what an update is.
  for p in $PKGS; do
    post "$p" /tmp/install.http
    grep -q '^HTTP/1.1 200' /tmp/install.http || { cat /tmp/install.http >&2; fail "install refused"; }
    grep -q '"installed":true' /tmp/install.http || fail "install not reported as installed"
    grep -q "\"slot\":\"$TARGET\"" /tmp/install.http || { cat /tmp/install.http >&2; fail "installed into the wrong slot"; }
    grep -q "\"runningSlot\":\"$RUNNING\"" /tmp/install.http || { cat /tmp/install.http >&2; fail "wrong running slot reported"; }
  done
  stop_daemon

  # --- the environment: slot switch and boot count, as the installer leaves them, in the
  # redundant format the U-Boot that booted reads
  [ "$(envcfg)" = "$ENVCFG" ] || fail "the environment is not the redundant one our U-Boot reads"
  [ "$(eget bootcmd)" = "$TCMD" ] || fail "bootcmd is '$(eget bootcmd)', want '$TCMD'"
  case $(eget "$TARGS") in
    *" soundtap.nandroot=1 ubi.mtd=NAND.Persistent-Storage,2048") ;;
    *) fail "$TARGS carries no SoundTap markers: $(eget "$TARGS")" ;;
  esac
  case $(eget "$OARGS") in *soundtap.*) fail "$OARGS of the running slot carries SoundTap markers" ;; esac
  # U-Boot's own rollback, the same on every model (ADR 0010): past bootlimit unconfirmed
  # boots it runs altbootcmd, which must boot the slot that was running. (The count itself
  # lives in the RTC and, until the confirm, the environment; U-Boot does not run here:
  # os/tests/boot/test_uboot_bootcount_env.py, then layer 3.)
  [ "$(eget altbootcmd)" = "$RCMD" ] || fail "altbootcmd is '$(eget altbootcmd)', want '$RCMD'"
  [ "$(eget bootlimit)" = 4 ] || fail "bootlimit is '$(eget bootlimit)', want 4"
  # Unconfirmed: U-Boot keeps the count in the environment too, which a power cut does not
  # clear, until soundtapd's boot confirm takes upgrade_available out again.
  [ "$(eget upgrade_available)" = 1 ] || fail "upgrade_available is '$(eget upgrade_available)', want 1"
  want="altbootcmd bootcmd bootlimit upgrade_available $TARGS"
  # The ST300's U-Boot console is the soundbar processor's link, so autoboot must not wait
  # for a key there (flash.InstallMainlineRecipes).
  if [ "$VARIANT" = ginger ]; then
    [ "$(eget bootdelay)" = -2 ] || fail "bootdelay is '$(eget bootdelay)', want -2"
    want="$want bootdelay"
  fi
  penv >/tmp/env.after
  # Entries that exist on one side only or differ: what appears once in the merged list.
  changed=$({ sort /tmp/env.base; sort /tmp/env.after; } | sort | uniq -u | sed 's/=.*//' | sort -u | tr '\n' ' ' | sed 's/ $//')
  # Our U-Boot has no nboot.e: the slot recipes must be ours (internal/flash/recipes.go),
  # naming this model's kernel and device tree. The factory env already holds some of
  # them, so the changed set is the slot switch plus whichever recipes differed.
  case $(eget nand_boot) in *"run slot_boot") ;; *) fail "nand_boot is still Bose's: $(eget nand_boot)" ;; esac
  case $(eget nand_boot2) in *"run slot_boot") ;; *) fail "nand_boot2 is still Bose's: $(eget nand_boot2)" ;; esac
  case $(eget slot_boot) in *"/boot/$DTB"*"/boot/uImage-$VARIANT"*) ;; *) fail "slot_boot does not load this model: $(eget slot_boot)" ;; esac
  case $(eget soundtap_args) in *"variant=$VARIANT module_type=$MTYPE "*) ;; *) fail "soundtap_args names the wrong model: $(eget soundtap_args)" ;; esac
  for k in $want; do case " $changed " in *" $k "*) ;; *) fail "the install did not change $k" ;; esac; done
  for k in $changed; do
    case " $want nand_boot nand_boot2 slot_boot soundtap_args stock_args mtdids mtdparts " in
      *" $k "*) ;; *) fail "the install changed environment entry $k" ;; esac
  done

  # --- the new slot holds the image
  eval "klen=\$EXP_KERNEL_LEN_$VARIANT ksha=\$EXP_KERNEL_SHA_$VARIANT"
  got=$(nanddump --omitoob --bb=skipbad -l "$klen" "/dev/mtd$ALTK" 2>/dev/null | sha256sum | cut -c1-64)
  [ "$got" = "$ksha" ] || fail "kernel partition differs from the image's kernel"
  ubiattach /dev/ubi_ctrl -m "$ALTR" -O 2048 -d 3 >/dev/null
  [ "$(cat /sys/class/ubi/ubi3_0/data_bytes)" = "$EXP_VOL_LEN" ] || fail "rootfs volume is $(cat /sys/class/ubi/ubi3_0/data_bytes) bytes, want $EXP_VOL_LEN"
  got=$(head -c "$EXP_VOL_LEN" /dev/ubi3_0 | sha256sum | cut -c1-64)
  [ "$got" = "$EXP_VOL_SHA" ] || fail "rootfs volume differs from the image's UBI volume"
  mount -t ubifs -o ro ubi3:rootfs /mnt/new
  test -x /mnt/new/init || fail "no /init in the new slot"
  test -s "/mnt/new/boot/uImage-$VARIANT" || fail "no /boot/uImage-$VARIANT in the new slot"
  test -s "/mnt/new/boot/$DTB" || fail "no /boot/$DTB in the new slot"
  umount /mnt/new
  ubidetach /dev/ubi_ctrl -d 3

  snap >/tmp/snap.now
  # --- the chain was written: four identical SPL copies and a new U-Boot. soundtapd
  # reads each one back itself; this checks it wrote all of them, and the same SPL.
  for m in 0 1 2 3 4; do
    if grep -qx "mtd$m $(nhash "$m")" /tmp/snap.base; then fail "mtd$m still holds what was there before"; fi
  done
  for m in 1 2 3; do
    [ "$(nhash "$m")" = "$(nhash 0)" ] || fail "SPL copy mtd$m differs from mtd0"
  done
  # The spare U-Boot behind the first copy is the same image (ADR 0009).
  ulen=$(nanddump --omitoob --bb=skipbad -l 64 /dev/mtd4 2>/dev/null | dd bs=1 skip=12 count=4 2>/dev/null \
    | od -An -tu1 | while read -r a b c d; do echo $(( (a << 24) + (b << 16) + (c << 8) + d + 64 )); done)
  first=$(nanddump --omitoob --bb=skipbad -l "$ulen" /dev/mtd4 2>/dev/null | sha256sum | cut -c1-64)
  spare=$(nanddump --omitoob --bb=skipbad -l "$ulen" -s $((0x100000)) /dev/mtd4 2>/dev/null | sha256sum | cut -c1-64)
  [ "$first" = "$spare" ] || fail "the spare U-Boot differs from the first copy"
  # An update over the chain just written must not touch it. A chain that was written
  # wrongly differs from the package, and would be rewritten here.
  sed -n 1,5p /tmp/snap.now >/tmp/snap.chain
  start_daemon
  post "$FIRST" /tmp/again.http
  stop_daemon
  grep -q '^HTTP/1.1 200' /tmp/again.http || { cat /tmp/again.http >&2; fail "update over the new chain refused"; }
  snap >/tmp/snap.now
  sed -n 1,5p /tmp/snap.now | cmp -s - /tmp/snap.chain || fail "an update over the same chain rewrote it"
  # --- nothing else moved: the running slot, persistent and manufacturing data are
  # byte-identical to before the install
  sed 1,5d /tmp/snap.base >/tmp/snap.base.rest
  sed 1,5d /tmp/snap.now >/tmp/snap.now.rest
  cmp -s /tmp/snap.base.rest /tmp/snap.now.rest || { diff /tmp/snap.base.rest /tmp/snap.now.rest >&2 || true; fail "a partition that must not move changed"; }
  echo "scenario $SC passed in $(( $(date +%s) - t0 )) s"
}

for sc in $SCENARIOS; do
  oldifs=$IFS; IFS=/; set -- $sc; IFS=$oldifs
  scenario "$@"
done
SC="done"
echo GUEST-RESULT=0
sync
poweroff -f
GUEST
chmod +x "${WORK}/init"

# shellcheck disable=SC2250 # os/tests/installer/test_nandsim_install.py matches this text as written
cat >"$WORK/container.sh" <<'CONTAINER'
#!/bin/sh
set -eu
trap 'chown -R "$HOST_UID:$HOST_GID" /work 2>/dev/null || true' EXIT
if [ "$GUEST_ARCH" = amd64 ]; then QEMU=qemu-system-x86; else QEMU=qemu-system-arm; fi
KVER=$(ls /lib/modules | head -1)
dpkg-query -W linux-image-generic mtd-utils libubootenv-tool "$QEMU" | sed 's/^/pinned: /'
root=/work/guest; rm -rf "$root"
# No brace expansion: this shell is dash, and {bin,sbin} would be one directory.
for d in bin sbin proc sys dev run mnt lib/modules lib/$LIB_ARCH-linux-gnu lib64; do
  mkdir -p "$root/$d"
done
cp /bin/busybox "$root/bin/"
# Every applet: soundtapd shells out to more of them (test, ...) than the init script does.
for a in $(/bin/busybox --list | grep -vx busybox); do
  ln -sf /bin/busybox "$root/bin/$a"
done
# mtd-utils and libubootenv and their libraries: the guest runs the same binaries the image has.
for b in flash_erase nandwrite ubiformat ubiattach ubidetach ubinfo nanddump fw_printenv fw_setenv; do
  cp "$(command -v $b)" "$root/sbin/"
done
for b in "$root"/sbin/*; do
  ldd "$b" 2>/dev/null | awk '/=>/ {print $3} /ld-linux/ {print $1}' | grep -E "^/" || true
done | sort -u | while read -r so; do cp -L "$so" "$root/lib/$LIB_ARCH-linux-gnu/" 2>/dev/null || true; done
if [ "$GUEST_ARCH" = amd64 ]; then cp -L /lib64/ld-linux-x86-64.so.2 "$root/lib64/"; else cp -L /lib/ld-linux-aarch64.so.1 "$root/lib/"; fi
# Exactly the modules the three modprobes need, dependencies included -- the
# kernel decides that, not a guess: a missing nandcore is a guest that panics.
# ubifs asks the crypto layer for its compressors by name at mount time, which
# --show-depends cannot see: without zstd and lzo it loads and then refuses
# every volume with "cannot initialize compressor".
for m in nandsim ubi ubifs zstd lzo deflate crc32c_generic; do
  modprobe --show-depends -S "$KVER" "$m" 2>/dev/null || true
done \
  | awk '/^insmod/ {print $2}' | sort -u >/work/modlist
wc -l </work/modlist
# busybox modprobe cannot read zstd-compressed modules: store them plain.
while read -r ko; do
  mkdir -p "$root$(dirname "$ko")"
  case $ko in
    *.zst) zstd -qdc "$ko" >"$root${ko%.zst}" ;;
    *) cp "$ko" "$root$ko" ;;
  esac
done </work/modlist
cp "/lib/modules/$KVER/modules.builtin" "/lib/modules/$KVER/modules.order" "$root/lib/modules/$KVER/" 2>/dev/null || true
depmod -b "$root" "$KVER"
cp /work/init "$root/init"; cp /work/soundtapd "$root/soundtapd"; cp /work/guest.env "$root/guest.env"
cp /work/factory-env.bin "$root/factory-env.bin"
for f in /work/update*.stu; do cp "$f" "$root/$(basename "$f")"; done
( cd "$root" && find . | cpio -H newc -o --quiet ) | gzip -1 >/work/initramfs.gz
cp "/boot/vmlinuz-$KVER" /work/vmlinuz
rm -rf "$root"
chmod 0644 /work/initramfs.gz /work/vmlinuz
ls -l /work/initramfs.gz /work/vmlinuz

echo "== boot one guest per scenario, side by side"
accel=tcg
[ -c /dev/kvm ] && accel=kvm
echo "qemu accelerator: $accel"
# Each guest is one vCPU and the scenarios share nothing, so they run at the same time:
# without KVM a scenario costs 12 minutes, and in turn they took half the job.
boot() {
  if [ "$GUEST_ARCH" = amd64 ]; then
    timeout "$GUEST_TIMEOUT" qemu-system-x86_64 -accel "$accel" -nographic -no-reboot -nic none -audiodev none,id=noaudio -m 1024 \
      -kernel /work/vmlinuz -initrd /work/initramfs.gz \
      -append "console=ttyS0 panic=1 quiet sc=$1"
  else
    cpu=cortex-a72
    [ "$accel" = kvm ] && cpu=host
    timeout "$GUEST_TIMEOUT" qemu-system-aarch64 -M virt -accel "$accel" -cpu "$cpu" -nographic -no-reboot -nic none -audiodev none,id=noaudio -m 1024 \
      -kernel /work/vmlinuz -initrd /work/initramfs.gz \
      -append "console=ttyAMA0 panic=1 quiet sc=$1"
  fi
}
for sc in $SCENARIOS; do
  boot "$sc" </dev/null 2>&1 | sed -u "s|^|[$sc] |" &
done
wait
CONTAINER

# The guest's packages come from a dated snapshot that can take half an hour to fetch
# (28 minutes once on the build host, which ran the gate out of time). They are installed once
# into a local image named after this setup script and reused by every later run.
cat >"${WORK}/setup.sh" <<'SETUP'
set -eu
export DEBIAN_FRONTEND=noninteractive
# A stalled download fails and is tried again instead of holding the job until its timeout
# (2026-11-25: three gates sat 25 minutes on one fetch from the snapshot archive).
cat >/etc/apt/apt.conf.d/80soundtap-retries <<'APTCONF'
Acquire::Retries "5";
Acquire::http::Timeout "60";
Acquire::https::Timeout "60";
APTCONF
# https needs certificates, which this image lacks; bootstrap them from the default
# source (the archive signs its own metadata, so this only supplies trust anchors),
# then freeze everything else.
apt-get -qq update >/dev/null
apt-get -qq install -y --no-install-recommends ca-certificates >/dev/null
rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*
cat >/etc/apt/sources.list.d/snapshot.sources <<EOF
Types: deb
URIs: https://snapshot.ubuntu.com/ubuntu/$APT_SNAPSHOT/
Suites: noble noble-updates
Components: main universe
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
EOF
rm -rf /var/lib/apt/lists/*
apt-get -qq update >/dev/null
if [ "$GUEST_ARCH" = amd64 ]; then QEMU=qemu-system-x86; else QEMU=qemu-system-arm; fi
apt-get -qq install -y --no-install-recommends linux-image-generic busybox-static mtd-utils \
  libubootenv-tool cpio zstd kmod "$QEMU" >/dev/null
KVER=$(ls /lib/modules | head -1)
apt-get -qq install -y --no-install-recommends "linux-modules-extra-$KVER" >/dev/null
apt-get clean
rm -rf /var/lib/apt/lists/*
SETUP
# shellcheck disable=SC2250 # os/tests/installer/test_nandsim_install.py matches this text as written
GUEST_IMAGE=soundtap-nandsim-guest:$APT_SNAPSHOT-$GUEST_ARCH-$(sha256sum <"$WORK/setup.sh" | cut -c1-12)
# shellcheck disable=SC2250 # os/tests/installer/test_nandsim_install.py matches this text as written
if ! docker image inspect "$GUEST_IMAGE" >/dev/null 2>&1; then
  c=soundtap-nandsim-setup-$$
  docker run -i --name "${c}" -e GUEST_ARCH="${GUEST_ARCH}" -e APT_SNAPSHOT="${APT_SNAPSHOT}" \
    "${RUNNER_IMAGE}" sh -s <"${WORK}/setup.sh" || { docker rm -f "${c}" >/dev/null; exit 1; }
  docker commit "${c}" "${GUEST_IMAGE}" >/dev/null
  docker rm "${c}" >/dev/null
fi

# ubi.mtd= is not on the kernel command line any more: nandsim is a module, so no kernel
# parameter could attach it at boot anyway. What soundtapd reads is the /proc/cmdline the
# guest builds per scenario.
# shellcheck disable=SC2086,SC2250 # KVM is empty or the --device option for /dev/kvm; os/tests/installer/test_nandsim_install.py matches this text as written
docker run --rm --name "${GUEST}" ${KVM} -e QEMU_AUDIO_DRV=none -e GUEST_ARCH="${GUEST_ARCH}" -e LIB_ARCH="${LIB_ARCH}" \
  -e HOST_UID="${HOST_UID}" -e HOST_GID="${HOST_GID}" -e APT_SNAPSHOT="${APT_SNAPSHOT}" \
  -e GUEST_TIMEOUT="${GUEST_TIMEOUT:-2400}" -e SCENARIOS="${SCENARIOS}" \
  -v "$WORK":/work "$GUEST_IMAGE" sh /work/container.sh | tee "$WORK/guest.log"

for sc in ${SCENARIOS}; do
  grep -qF "[${sc}] GUEST-RESULT=0" "${WORK}/guest.log" || { echo "NANDSIM INSTALL TEST FAILED in ${sc}" >&2; exit 1; }
done
echo "NANDSIM INSTALL TEST PASSED"
