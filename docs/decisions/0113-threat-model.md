# 0113. What a stranger can do to a production speaker, and what we accept

Status: accepted — 2026-11-21

## Context

The access rules come from several decisions: the home network is trusted and dangerous actions
need a key held on the speaker ([ADR 0055](0055-household-token-and-windows.md)), every update is
signed ([ADR 0048](0048-signed-updates.md)), the root filesystem is read-only behind an inbound
filter ([ADR 0066](0066-production-hardening.md)), and a production image has no SSH
([ADR 0119](0119-no-ssh-in-production.md)). This ADR says in one place what a stranger can and
cannot do with all of them together, including what we leave undefended and why. It is the
project's one security story; [`SECURITY.md`](../../SECURITY.md) is the reporting policy and
points here.

It covers a production image. An alpha image adds SSH and a USB root shell on purpose and is not
meant to leave the bench.

## Decision

For each stranger: what they can do, what stops the rest, and what we accept.

### A stranger on the home network, or on the open setup hotspot

- **What reaches them.** The filter answers only hosts on the speaker's own subnets, as stock
  did; a port forward or routed network gets nothing. From those it lets in port 80 (soundtapd)
  and 7000 (AirPlay); new Snapcast connections on 1704 only from addresses in `snap_followers`,
  never from the setup interface `wlan1` (soundtapd adds a follower and refreshes its ten-minute
  allowance, and anyone on the LAN may ask through `POST /api/multiroom/listen`, so the set keeps
  out passive listeners only); mDNS, SSDP and PTP, the last two not from `wlan1`; the kernel's
  ephemeral range 32768–60999; and on the setup hotspot only, DHCP and DNS.
- **What they can do.** Everything an owner does from the app: play, change volume, presets,
  settings and multiroom, open the Bluetooth pairing window, read the backup. That is the Sonos
  model. Every route has a class in `app/internal/web/route_access_test.go`, which fails for a
  route without one.
- **That includes the linked Spotify account.** The `/api/spotify/*` routes are open: anyone on
  the LAN can read the account's library and recent listening, like or unlike tracks, start
  playback and unlink the account. We accept this because the speaker's Spotify session is the
  household's, as on a shared Connect speaker, and the app's Spotify browser is built on these
  routes; a press per read would make it unusable. No route returns the Spotify credentials or an
  access token.
- **So does a crash report, once the owner turned them on.** `GET /api/crash-report` is open and
  returns the last crash's trace, redacted as the support bundle is but without its logs
  ([ADR 0126](0126-opt-in-crash-reports-without-a-server.md)). While reports are off it returns
  nothing.
- **The traffic is plain HTTP.** A host that can see the traffic reads what the UI reads, and the
  passwords an owner sends in: a new Wi-Fi password and the MQTT broker password. We accept this:
  such a host can already ask the speaker for everything else, there is no certificate a browser
  would trust for a `.local` name, and no route sends a password back out.
- **What stops the rest.** Reboot, factory reset, restore, Wi-Fi changes, setup mode, every update
  but the latest one the speaker is offered ([ADR 0118](0118-update-button-without-press.md)), the owner key and
  the debug bundle wait for a key held on the speaker. Firmware and daemon updates must also carry
  a signature from a release key or from the owner key set on that speaker. The player,
  receiver and Bluetooth hooks answer only 127.0.0.1. No route returns a password: the backup
  leaves them out, and saved Wi-Fi lists names only.

### A web page in the owner's browser

The Host check (DNS rebinding) and the Origin/Sec-Fetch-Site check (CSRF) protect mutating
requests. An HTTP origin with the same accepted speaker hostname or IP on another port is
deliberately allowed; these checks do not authenticate LAN clients. Accepted names are the IP, the
speaker's `.local` names, its bare name and that name in the domains of its DHCP lease, which the
home router hands out and a rebinding page cannot pick.

`GET /api/version` alone allows credential-free cross-origin reads
(`Access-Control-Allow-Origin: *`), after the Host check, so the hosted installer can verify an
install. It exposes build versions and update capability, no speaker identity, settings or
credentials; no preflight or mutating method is opened. The installer reports success only after
reading that JSON with nonempty versions and an API level, never from an opaque response or the
stock port going away; when the browser blocks the read, the outcome stays unconfirmed.

### A stranger in Wi-Fi range while the speaker is in setup mode

The setup access point is open, as on stock
([ADR 0063](0063-wifi-lease-and-setup-ap.md#the-setup-access-point-stays-open-as-on-stock)), and
the phone sends the home network's name and password to `POST /api/wifi` over plain HTTP. Anyone
in range who captures that exchange learns the password, and anyone can run their own open
`SoundTap-Setup` (an evil twin) and collect it from an owner who joins that one instead. What
limits it: setup mode is short and comes only when no network is saved, or for stretches after a
failed join; and a stranger on the access point cannot point the speaker at their own network
without a hold on it. We accept it because the ST10 and the Wireless Link have no screen and carry
Bose's label, so a per-device passphrase has nowhere to be shown, and it is the setup stock
offered. An owner worried about it sets the speaker up where no stranger is in range, or changes
the Wi-Fi password afterwards.

### A stranger in Bluetooth range

They can pair only while a pairing window someone opened from the speaker or the LAN is open, and
the agent authorizes only A2DP and AVRCP. Paired, trusted devices reconnect after the window
closes; outside it new authorization requests are refused (ADR 0055).

### A malicious update file

Over the network, `POST /api/firmware` and `POST /api/soundtapd` verify the ed25519 signature
before selecting a candidate, and they need a hold. The STU handler authenticates the expected
digest before streaming and compares the received digest at the end, so replayed authentic
metadata with different bytes can still overwrite the inactive slot before that final mismatch is
caught. Two routes accept an unsigned package, and both need hands on the speaker: the stock
installer on :17008, which runs only on stock firmware, and the power-on USB stick, whose key chord
is the authorization and which also takes Bose's own file back to stock
([ADR 0130](0130-usb-stick-recovery.md)).

### A stranger with the speaker in hand

There is no secure boot, and secrets on the speaker are stored in plain text. Someone who holds
the speaker, or already has root on it, can run anything and read everything on it.

- **No chain of trust.** The AM335x ROM boots an unsigned SPL from NAND or USB; that is how USB
  recovery works. Our SPL loads U-Boot, and U-Boot loads a legacy uImage and device tree from the
  selected slot without checking a signature; it is built without `FIT_SIGNATURE`. Whoever can
  write NAND, through the ROM's USB boot or a clip on the chip, boots what they like; a remote
  attacker with root on a running speaker can write a slot, and the next boot does not notice.
- **Secrets in plain text on `/mnt/nv`.** The persistent volume is not encrypted: the home Wi-Fi
  password and up to five earlier networks (base64 or the derived WPA key, which is as good as the
  password for joining), librespot's reusable Spotify credentials, and the MQTT broker password
  ([ADR 0110](0110-mqtt-bridge.md)). Each file is mode 0600 for its owner. A factory reset deletes
  them as files; it does not scrub the flash. [`docs/contributors/nv-state.md`](../contributors/nv-state.md) lists every
  file.
- **What holds.** Over the network nothing reaches NAND without a signature and a hold, the root
  filesystem is read-only, the media daemons do not run as root, and there is no SSH or root
  login. The protection is against getting root, not against what root can do.
- **Why we accept it.** The ROM boots unsigned code over USB on any opened board, and that is our
  recovery path, so a chain of trust after the ROM stops nobody with the speaker on a bench while
  it would cost the way back. The boards have no secure element: a key to encrypt the secrets
  would sit on the same NAND. wpa_supplicant and the MQTT client need the passwords themselves.

### What the model rests on beyond the speaker

- Only the Release workflow builds what is published, from a commit CI passed on
  ([ADR 0035](0035-releases.md)), and every Core package and app binary is signed with the
  release key by `sign.yml`, the one job that reads it: an encrypted secret of the `release`
  environment, limited to `main`. Whoever can run a workflow there can sign, so the workflows and
  the release tooling are code-owner reviewed under branch protection of `main`.
- A speaker still on Bose's software installs whatever the update feed on `main` names, with a
  CRC check only. Code-owner review of that path and a check that the feed names a published
  release whose `.sig` verifies stand in for the signature check the stock updater lacks; they
  hold only while branch protection on `main` requires that review
  ([`RELEASING.md`](../../RELEASING.md#the-first-install-feed)).
- Buildroot packages, container images, Go modules and npm packages are pinned
  ([ADR 0011](0011-one-buildroot-pipeline.md), [ADR 0124](0124-third-party-code.md)); each is code
  we trust and review.
- Credentials are inert data, never sourced or evaluated as shell code.
- Core updates go into the inactive A/B slot; SPL, U-Boot and the boot environment are shared and
  may change during an install, outside that protection. App updates use an override with trial
  and rollback ([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)).

Alternatives that lost: a household token (it kept locking the owner out; a hold protects the
dangerous few without that cost); verified boot with FIT signatures in SPL and U-Boot (the ROM's
USB boot runs unsigned code anyway, and a signed chain needs key handling in the build and a
recovery route that still works); WPA2 or HTTPS for setup (no place to show a passphrase, and a
self-signed certificate on an open network teaches people to click through); Spotify library
reads behind a hold (the library is what the app browses); closing the ephemeral range (AirPlay 2
and Spotify Connect pick their ports at run time, and pinning them means patching both).

## Consequences

- The model stands or falls with "the speaker is not reachable from the internet". A port forward
  to port 80 hands every open route to the internet. The app and docs say so; the speaker cannot
  detect it.
- Only a key held on the speaker's own panel confirms; the ST10 and ST20 IR remote never does. On
  a SoundTouch 300 a held key from its remote confirms, which needs someone in the room or an IR
  blaster in front of the bar. A host on the LAN can replace a waiting request with its own; see
  [ADR 0055](0055-household-token-and-windows.md#dangerous-actions-need-a-key-held-on-the-speaker).
- Anyone within Wi-Fi range of a speaker in setup mode can read or phish the home Wi-Fi password
  sent during setup.
- The speaker is not a vault: losing it, or handing it on without a factory reset, hands over the
  home Wi-Fi password, the Spotify login and the MQTT password. Changing those is the remedy.
- A failed filter load is reported, not hidden (ADR 0066).
- The port scan of a production image must list every listener in the ephemeral range, not only
  the fixed ports.
- A new route is a security decision: the route test makes the author pick its class.

## Code

- `os/board/rootfs/firewall.nft`; `multiroom.AllowFollower` (`app/internal/multiroom/firewall.go`)
  keeps `snap_followers`.
- `app/internal/web/route_access_test.go`: the class of every route.
- `app/internal/web/guard.go`: the Host and Origin checks, `sameSpeakerOrigin`, and the open
  `Access-Control-Allow-Origin` on `/api/version`.
- `app/internal/web/confirm.go:needsPress`: the routes behind a hold.
- `app/internal/web/spotify.go`: the open Spotify routes.
- `os/bootloader/build-wla-uboot.sh`: U-Boot without `FIT_SIGNATURE`.
- `app/cmd/soundtapd/network.go`: the Wi-Fi credentials on `/mnt/nv`.
