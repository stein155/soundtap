# 0066. Production hardening: read-only root, inbound filter, unprivileged daemons, bounded logs

Status: accepted — 2026-10-21

## Context

A speaker sits on a home LAN next to phones, TVs and whatever else is there, and everything it
listens on is reachable by all of them. The daemons that parse what the LAN sends are the most
exposed code we ship, and none of them is ours: shairport-sync, librespot, snapclient and
snapserver, nqptp, and ffmpeg (radio and announcement URLs anyone on the LAN can set). Running
them as root makes a bug in any of them root on the speaker, with write access to `/mnt/nv` and
the boot slots. The same holds for soundtapd, which parses every request on port 80. A writable
root filesystem lets any root process change the image that boots next time.

The speaker has little RAM and a small `/run` and `/tmp`, so a log that grows without a bound,
or a child that floods its output, pushes everything else out. Persistent NAND wears with every
write.

## Decision

### `/` stays read-only; boot-time files live under `/run`

Every kernel command line mounts the root UBIFS `ro`, and the init script never remounts it.
Files the boot generates go to `/run/soundtap`: the image ships `/etc/asound.conf` as a link to
`/run/soundtap/asound.conf`, and snapserver reads its configuration from there. `/var/lib/bluetooth`
is a link to `/mnt/nv/bluetooth` made at image build time. The regional wl18xx configuration is
picked by a link under `/run/soundtap/firmware`, which the kernel's firmware loader searches
first; the image's own `wl18xx-conf.bin` stays a valid default.

### A production image filters inbound traffic, default deny

A production image loads `os/board/rootfs/firewall.nft` with nftables as its first sysinit step,
before the USB gadget brings up the first interface. Policy drop on input; accepted are
established and related traffic, loopback, basic ICMP, and, from hosts on the speaker's own
subnets only, as on stock: TCP 80 (soundtapd), 7000 (shairport-sync), 1704 (snapserver, to
followers soundtapd lets in), UDP 5353 (avahi), 1900 (SSDP), 319 and 320 (nqptp), and the kernel's
ephemeral range 32768–60999, where AirPlay 2 and librespot's zeroconf pick their ports. On the
setup interface `wlan1` only, DHCP and DNS also answer. Outbound traffic is not filtered. Alpha
loads no ruleset.

The own subnets are the set `lan_nets`, kept by `/opt/soundtap/lan-allow.sh`: every way an
interface gets an address (the DHCP hook, the setup access point, the ST20's static `eth0`) adds
its subnet and removes it again. The one fixed exception is the ST20 BCO bootloader's TFTP
replies from 192.168.1.100, on a link soundtapd addresses itself during a BCO flash. The kernel
builds nf_tables and connection tracking in, because the image loads no modules; without a
ruleset no hook is registered.

A failing `nft -f` does not stop the boot: the speaker comes up unfiltered rather than not at
all, and says so. soundtapd lists the input chain with a bounded, read-only `nft` run at start and
at most once a minute; on a production image without the chain or its default drop it logs an
error and `GET /api/health` says `"firewall": "missing"`.

### Media daemons run as their own users

`os/board/rootfs/users.txt` creates `airplay`, `spotify`, `snapcast`, `radio` and `ptp`, with no
password and no shell. The first four have `audio` as their only group, the one group that may
write the source bus FIFO; `ptp` has no audio access. soundtapd starts shairport-sync as
`airplay`, librespot as `spotify`, snapclient and snapserver as `snapcast`, every ffmpeg as
`radio`, and nqptp as `ptp` with one ambient capability, `CAP_NET_BIND_SERVICE`, for UDP 319 and
320. None of them opens the sound card: they write the source bus, and soundtapd's Mixer is the
card's only writer ([ADR 0012](0012-single-dac-writer.md)); the card's nodes are group
`soundtapd`'s, not `audio`'s. soundtapd hands each daemon the state it must write before
starting it. When a user is missing the daemon does not
start: falling back to root would silently undo this decision. bluetoothd, bluealsa and avahi
stay root: they parse less from the LAN and are not supervised by soundtapd.

### soundtapd runs as its own user; what needs root goes through soundtap-root

`users.txt` creates `soundtapd` (group `soundtapd`, member of `audio` for the source bus).
`init.d/soundtap` starts `soundtap-root` (`app/cmd/soundtap-root`, in the same Go module) as root, then
soundtapd through `soundtap-root exec-soundtapd`: it drops every capability but six from the
bounding set, switches to the user, raises the six as ambient capabilities (the only kind a binary
without file capabilities keeps across exec as a user other than root) and execs soundtapd in
place, so `soundtapd_loop`'s pid is soundtapd's. soundtapd clears its ambient set on every thread
first thing, so no child inherits a capability, and gives one back per command where a tool needs it.

What soundtapd did as root, and where it went:

| Class | What | How now |
| --- | --- | --- |
| (a) permissions, set by init every boot | `/dev/watchdog*`, keypad and buttons (`/dev/input/event*`, `/dev/shelby-keypad`), IR (`/dev/lirc0`, `/dev/ir`), SPI (`/dev/spidev*`: the ST20 panels, the codec loader), `/dev/i2c-*`, `/dev/ttyS0` (the ST20 BCO's and the ST300 processor's serial link), ALSA `/dev/snd/*`, the stock lamps and panel | group `soundtapd`, 0660 |
| | `/dev/mtd*` (readback, `fw_printenv`, the serial, the Wi-Fi calibration) | group `soundtapd`, 0640: read only |
| | the lamps' `brightness` and `trigger`, GPIO `export`/`unexport`, the attributes of every line init exported (amplifier, mute), pstore's records | group or owner `soundtapd` |
| | `/run`, `/run/soundtap`, `/mnt/nv` | root's, group `soundtapd`, 1775: soundtapd adds its own entries (pid files, wpa_supplicant's and hostapd's control directories, `wifi.conf*`) and cannot touch root's. `ntp-servers`, `lan-net.{eth0,wlan0,wlan1}`, `/tmp/resolv.conf` and libubootenv's lock are root's and group-writable, since root's and soundtapd's DHCP both write them; `lan-allow.sh` empties its state instead of removing it |
| | `/mnt/nv/soundtap` (except init's boot logs), and `/run/audio-bin/app`, where soundtapd runs and `/api/soundtapd` swaps it, off root's PATH | soundtapd's |
| | `/mnt/nv/spotify`, `/run/soundtap/spotify` (librespot's), `/run/soundtap/snapserver` (snapcast:soundtapd 2770, so soundtapd seeds and keeps `server.json`) | made and handed over by init; soundtapd no longer chowns |
| | the firmware loader's search path | init sets it to `/run/soundtap/firmware`; soundtapd writes it only when it says something else |
| (b) capabilities soundtapd keeps | `CAP_NET_BIND_SERVICE` | :80 |
| | `CAP_NET_ADMIN`, `CAP_NET_RAW`, `CAP_NET_BIND_SERVICE` | raised for its network tools only (`childproc.NetTool`): ip, iw, ifconfig, wpa_supplicant, hostapd, udhcpc, udhcpd, dnsd, nft, ping, `lan-allow.sh`; nqptp keeps `CAP_NET_BIND_SERVICE` as before |
| | `CAP_SETUID`, `CAP_SETGID`, `CAP_KILL` | starting the media daemons and ffmpeg as their users (`childproc.RunAs`) and stopping them |
| (c) root, through soundtap-root | NAND erase and writes, ubiformat, the written slot's attach and read-only mount, `fw_setenv`, reboot, U-Boot's boot count in RTC SCRATCH2 (`/dev/mem`), the BCO's supply switch (`/dev/mem`), `sethostname`, exporting a GPIO line, `init.d/soundtap sync-clock` (the clock and `/mnt/nv/soundtap-clock`), `bt-ctl.sh` (BlueZ's pairing files), removing and recreating Spotify's and BlueZ's state in a factory reset | the requests below |

Not kept: `CAP_SYS_TIME`, `CAP_SYS_BOOT`, `CAP_SYS_ADMIN`, `CAP_SYS_RAWIO`, `CAP_CHOWN`, `CAP_FOWNER`
and `CAP_DAC_*`: each lets a process write what the NAND contract protects or become root again.
Nothing in soundtapd opens `/dev/mem` any more; init's own `devmem` reads (the boot count at boot,
the SoC MAC) stay in init, as root. The BCO flash keeps its serial link and TFTP in soundtapd and
asks the helper only for the supply switch; nothing of it needs root beyond that.

**soundtap-root** listens on `/run/soundtap-root/sock` (directory root:soundtapd 0750, socket
root:soundtapd 0660) and answers only soundtapd's uid and root (`SO_PEERCRED`). One connection
carries one request: a length-prefixed JSON object with an operation from a fixed list and its
numbers, unknown fields refused, at most 64 KiB, with up to two descriptors (`SCM_RIGHTS`): the
command's input and the operations lock, which the command holds as it did under soundtapd. The
helper never opens a path soundtapd names and runs no shell. Each request is logged.

| Request | Runs | Refused when |
| --- | --- | --- |
| `nand.erase` | `flash_erase /dev/mtdN OFF BLOCKS` | the partition is not an SPL copy, U-Boot, the environment or the kernel of a slot that is not running; the range is not whole erase blocks inside it |
| `nand.write` | `nandwrite [-p] [-s OFF] /dev/mtdN` from the passed file | as above; the input is not a regular file or does not fit |
| `nand.block` | `dd` of one erase block of the passed file into a bootloader partition (U-Boot's own half) | not a bootloader partition, not its erase block, not in the input |
| `ubi.format` | `ubiformat /dev/mtdN -f - -S SIZE -s 2048 -y` from the passed stream | not the rootfs of a slot that is not running, or larger than it |
| `ubi.attach`, `ubi.detach` | `ubiattach -m N -d 7 -O 2048`, then its volumes group-readable; `ubidetach -d 7` | as for `ubi.format` |
| `slot.mount`, `slot.umount` | `ubi7:rootfs` read-only on `/run/soundtap-root/verify-rootfs` | — (a fixed place only root writes) |
| `env.set` | `fw_setenv` over a `fw_env.config` the helper writes from the checked copies, with the passed script | a copy is not `NAND.U-Boot-Env` at 0 or `NAND.U-Boot` at the redundant offset, or not an environment size; the script names an entry `flash.EnvKeys` does not |
| `reboot`, `bootcount.read`, `bootcount.clear`, `bco.power` | reboot; read SCRATCH2; clear it and make the reboot mode cold; the BCO's supply on or off | — |
| `hostname.set` | `sethostname` | not one lowercase DNS label |
| `gpio.export` | export the line and hand its attributes to soundtapd | not one of the keypad, ST20 amplifier and panel lines soundtapd drives |
| `clock.sync`, `bt.ctl`, `nv.reset` | `init.d/soundtap sync-clock`; `bt-ctl.sh VERB`; `/mnt/nv/{spotify,bluetooth,bluealsa}` removed and made again, Spotify's for its user | a verb bt-ctl.sh does not have |

On a NAND boot whose running slot cannot be told from the command line no slot is writable; a RAM
boot runs neither slot and may write either, as the recovery plan does. The running slot,
`mtd10`-`mtd12` and every partition not named above are never written. soundtapd keeps every
decision of an install (signature, plan, order, readback); the helper holds each step to the NAND
contract. Without the socket in its environment (a development run, the tests, an OS that starts
it as root) soundtapd does every step itself, as before.

Every boot init hands soundtapd what on `/mnt/nv` is not its own yet (`migrate_nv_owner`): the first
boot after the update chowns the state every earlier build left root's
([ADR 0120](0120-on-box-state-migrates.md)); later boots find nothing and write nothing there. No
format changes.

### Logs are bounded in RAM, and children are rate-capped

BusyBox `syslogd` owns `/tmp/soundtap.log` and rotates it by rename at 256 KiB with one old copy,
so no line is lost to a trim. Shell writers append to `$LOG`, a FIFO that one supervised
`logger -t soundtap` drains into syslogd. soundtapd logs through `slog` as the default logger,
`http.Server` errors included, at the level in `SOUNDTAP_LOG_LEVEL`. Child output (ffmpeg, aplay,
the supervised players) passes a writer capped at 16 KiB per child per minute that reports what it
dropped; the ST20's BCO LAN log stops at 256 KiB. Nothing routine is logged to `/mnt/nv`: only the
boot trace and the crash trace of [ADR 0052](0052-soundtapd-lifecycle.md) survive a reboot.

Alternatives that lost: iptables (legacy; nftables is what the kernel and Buildroot treat as
current); filtering in alpha too (a test listener that silently does not answer costs more on the
bench than it protects); loading the filter as modules (the image loads none); one shared `media`
user (a hole in one daemon would reach the others' state, Spotify's credentials included);
dropping capabilities as root (uid 0 still owns most files); routine logs on `/mnt/nv` (NAND wear
outweighs keeping them across a reboot). For soundtapd's own user: dropping to it inside soundtapd
(it would parse its arguments and environment as root first); file capabilities on the binary (an
uploaded app would carry none, and the image keeps no xattrs); one capability set wide enough for
NAND and `/dev/mem` (`CAP_SYS_RAWIO` and `CAP_DAC_OVERRIDE` are root in all but name); a generic
run-this-command helper (a hole as wide as root); util-linux `setpriv` (a new package for what
`childproc.ExecAs` does in Go).

## Consequences

- Anything that writes under `/` at boot fails and shows in the log instead of changing the image.
  Any firmware file placed under `/run/soundtap/firmware` wins over the image's; soundtapd's
  Wi-Fi manager links the region's wl18xx configuration there, and init points the loader at it.
- Anything new that listens on a fixed port needs a line in `firewall.nft`, or it works in alpha
  and not in production. Anything that gives an interface an address another way must call
  `lan-allow.sh`.
- A port forward, routed VLAN or VPN into the home network reaches nothing on the speaker, as on
  stock.
- The ephemeral range stays open to the LAN, so whatever binds there on 0.0.0.0 is reachable;
  pinning it would need fixed ports in shairport-sync's AirPlay 2 channels, which have no such
  option.
- A soundtapd under an attacker's control is no longer root: it cannot write the running slot,
  `mtd10`-`mtd12`, `/mnt/nv` outside its own state, any file of root's, or environment entries it
  does not write itself. It can still write the inactive slot, the bootloaders and the slot
  selection through the helper, as an install does: the helper holds the NAND contract, not the
  package's provenance, which soundtapd checks. Moving the signature check into the helper is the
  next step if that boundary has to hold too.
- Root still writes into directories soundtapd may write (`/run/soundtap`, `/mnt/nv`,
  `/mnt/nv/soundtap`, `/run/audio-bin/app`). Where both write they are sticky, and init removes a
  temporary name before it writes one, so a link soundtapd leaves there is not followed.
  `fs.protected_symlinks` covers only world-writable sticky directories; this is the remaining gap.
- A device or root-owned file soundtapd needs is a line in `grant_soundtapd`; a new root step is a
  new helper request with its check and a row in the table above, never a generic one. A new
  environment entry is a new entry in `flash.EnvKeys`.
- A compromised media daemon can write the source bus and its own state, and read what is
  world-readable, but not `/mnt/nv/soundtap`, the boot environment or another daemon's files. A new
  media daemon needs its own user and the same treatment.
- Daemons that log to syslog themselves keep their tag; the shell writers share `soundtap`.

## Code

- `os/board/rootfs/firewall.nft`, `os/overlay/opt/soundtap/lan-allow.sh`,
  `os/board/rootfs/rootfs-overlay/etc/inittab`, `os/board/image/linux.fragment`.
- `app/internal/web/firewall.go`: `web.NewFirewallCheck`, `FirewallCheck.Status`.
- `os/board/rootfs/users.txt`, `os/board/rootfs/post-build.sh` (the `/etc/asound.conf` and
  Bluetooth links, the production ruleset).
- `app/cmd/soundtapd/procs.go`: `procUser`, `procCaps`, `decoderUser`, `snapclientArgs`;
  `childproc.RunAs`; `app/internal/supervise/supervise.go` (`supervise.Spec`).
- `os/overlay/etc/init.d/soundtap`: syslogd, the log FIFO and the bus FIFO's group.
- `supervise.ChildLog` (`app/internal/supervise/capwriter.go`); `app/cmd/soundtapd/main.go`
  sets the default logger and level.
- `app/cmd/soundtap-root`; `app/internal/rootd` (`Server`, `Client`, `NewLayout`,
  `CheckEnvScript`); `app/internal/childproc/caps_linux.go` (`ExecAs`, `ClearAmbient`, `NetTool`,
  `SoundtapdCaps`); `app/cmd/soundtapd/roothelper.go`; `flash.EnvKeys`, `flash.ConfirmBootCountVia`,
  `flash.VerifyDir`.
- `os/overlay/etc/init.d/soundtap`: `grant_soundtapd`, `migrate_nv_owner`, `start_root_helper`,
  `mark_network_ready`; `os/overlay/etc/dbus-1/system.d/soundtapd.conf`;
  `os/package/soundtapd/soundtapd.mk` builds soundtap-root.
- `os/support/rootfs_check.py`: fails a rootfs whose init remounts `/` read-write or starts
  soundtapd other than as its own user through soundtap-root, and a production rootfs without
  `nft`, a default-deny ruleset or an inittab that loads it first.
