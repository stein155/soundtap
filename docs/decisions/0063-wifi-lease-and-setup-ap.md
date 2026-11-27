# 0063. Wi-Fi: soundtapd's manager, renewed leases and an open, temporary setup access point

Status: accepted — 2026-10-21; Wi-Fi moved into soundtapd 2026-11-21

## soundtapd manages Wi-Fi through wpa_supplicant's control socket

### Context

Until 0.1.0 a 600-line shell script, `wifi.sh`, brought Wi-Fi up: it read the regulatory
country, joined the saved networks, fell back to the last network that worked, opened the
setup AP and retried, serialised itself with a lock directory, and handed `wpa_cli` an action
script (`wifi-event.sh`) for network changes. soundtapd re-ran it on every change and could only
see its exit status. Its tests cut functions out of the script with regular expressions. The
argument for keeping it in shell ([ADR 0115](0115-shell-only-for-boot.md)) was that Wi-Fi had to
work without soundtapd; but the setup AP only serves soundtapd's page and waited for soundtapd
before it handed out a lease, and a boot whose soundtapd never answers is rolled back by U-Boot's
boot count ([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)). Owner's decision 2026-11-21: the
move belongs in 0.1.0.

### Decision

`app/internal/wifi` owns Wi-Fi on every model whose main processor drives the radio
(`SOUNDTAP_WIFI_MODE=scm`: ST10, WLA, ST300, and the ST20 with SoundTap's BCO app). soundtapd starts it
once its name is published; `/etc/init.d/soundtap` starts no Wi-Fi script.

- **Control socket.** `app/internal/wpactrl` speaks wpa_supplicant's ctrl_iface protocol
  (datagrams on `/var/run/wpa_supplicant/wlan0`; ATTACH and events on the same socket). The
  library candidates (`pifke.org/wpasupplicant`, `hdiniz/wpa_supplicant-go`) are unmaintained
  for years and cover only part of it; the protocol is a few commands, so it is SoundTap's own small,
  tested package ([ADR 0124](0124-third-party-code.md)).
- **No key in a file.** wpa_supplicant starts with a configuration that names only its control
  socket and, since [ADR 0136](0136-every-network-stock-joined-and-wpa3.md), `pmf=1` and
  `sae_check_mfp=1`. The manager adds each network with ADD_NETWORK and SET_NETWORK: the SSID in hex, the
  PSK as the 32-byte key that wpa_passphrase would derive (PBKDF2-SHA1), or `key_mgmt NONE`.
  wifi.conf first at priority 6, the known networks below it, every one with `scan_ssid 1`.
  No passphrase reaches `/tmp` or the log. WPA3's SAE needs the password itself, so wpa_supplicant
  gets it as `sae_password` (in hex) over the control socket, beside the PSK
  ([ADR 0136](0136-every-network-stock-joined-and-wpa3.md)); a network saved as a raw 64-digit
  key has none and joins over PSK.
- **Events.** A WRONG_KEY `CTRL-EVENT-SSID-TEMP-DISABLED` marks a wrong password beside the
  4-way-handshake state; `CTRL-EVENT-CONNECTED` to another SSID makes udhcpc release and renew
  (what `wifi-event.sh` did).
- **udhcpc, hostapd, udhcpd and dnsd stay** as the busybox and upstream daemons, started by the
  manager with the same arguments as before. They and wpa_supplicant are daemons, not soundtapd's
  children to restart: a soundtapd that crashes leaves the network up, and the next one finds a
  joined station or a running setup AP by their pid files and keeps it.
- **One transition at a time.** A request (new credentials, the setup chord, a factory reset)
  cancels the transition that runs and starts from a clean slate; the lock directory is gone.
- **Regulatory.** Country and region from the manufacturing data, overridden by
  `country_code`/`region_code` in the U-Boot environment, as before; the WiLink board
  configuration's region link and `iw reg` are set by the manager.
- **The clock.** After a join the manager runs `/etc/init.d/soundtap sync-clock`; clock.sh stays
  in shell ([ADR 0125](0125-time-sources-and-clock-gating.md)).
- **On-box files keep their format** ([ADR 0120](0120-on-box-state-migrates.md)): `wifi.conf`,
  `wifi.conf.good` and `wifi.conf.known` are read and written as before; the 0.1.0 fixtures join
  in `app/internal/wifi`'s tests.

### Consequences

- The join, the fallback, the AP, the hold and the reasons a join failed are Go tests against a
  fake control socket (`app/internal/wifi`). A failure to bring Wi-Fi up shows in `/api/health`.
- soundtapd reads the link and the scan table over the control socket; nothing calls `wpa_cli`.
- Wi-Fi starts a few seconds later in the boot than `wifi.sh` did: when soundtapd starts, not
  before it. Every Wi-Fi row of the hardware checklist needs a rerun on the candidate.
- A soundtapd that never starts leaves the speaker without Wi-Fi until the boot count rolls the
  slot back; with `wifi.sh` it had Wi-Fi and a setup AP that served nothing.

## udhcpc keeps its lease; with saved credentials the setup AP is temporary

### Context

Two radio-exposure findings from a review:

- `wifi.sh` (now soundtapd's Wi-Fi manager), the eth0 path in `init.d/soundtap` and the BCO
  lan watch ran `udhcpc -b -q`. `-q` exits once a lease is bound, so nothing ever renewed it.
  When the lease ran out the router could give the address to another host: an address conflict, and the speaker off the LAN until reboot.
- A failed join at boot (no address after 45 s) started the open setup AP, and nothing ever went
  back to the station. After a power cut the router usually boots slower than the speakers, so
  every speaker came back as an open AP with a catch-all DNS, for good.

### Decision

udhcpc runs without `-q` everywhere: it stays in the background and renews its lease. The BCO lan
watch, which used to start a new udhcpc only when none was running, now replaces a running one
when the new netdev has no address (the old one would wait for its renewal time on a netdev the
BCO took away); it only kills the pid in `/run/soundtap-udhcpc.pid` when `/proc/<pid>/comm` says
`udhcpc`.

When a join fails while credentials are saved, the Wi-Fi manager still opens the setup AP, and
retries: every 5 minutes, while the setup AP is up and has no client associated, it tears the AP
down and tries the saved network again. A device on the AP holds it open, for at most
30 minutes. Without saved credentials the AP stays, as before.

When the network was not found or gave no address, the first ten retries come every minute instead:
a router coming back from a power cut then costs about a minute, not up to five. A wrong password
keeps the 5 minutes, since it needs the AP and trying sooner does not help.

### Consequences

- A speaker recovers by itself from a router that came up late. Each attempt leaves at most
  5 minutes of open AP (1 minute for the first ten retries after not-found or no address), plus
  the 90 s join and address window.
- A wrong saved password now cycles: AP for 5 minutes, a failed join, AP again. The setup page is
  still reachable in each AP window, and a connected phone holds the cycle off for up to
  30 minutes.
- Buildroot's `/usr/share/udhcpc/default.script` sets address, default route and resolv.conf
  on bound and renew; its hook `default.script.d/soundtap-log` logs each lease event. soundtapd
  is not told about address changes and picks them up within 15 s (`maintainIP`).

### Alternatives that lost

- Keep the station retrying while the AP is up: opening the AP stops wpa_supplicant, and the
  ST20's BCO link has one radio context; a full new pass reuses the tested join path instead.
- A supervised udhcpc with its own script: more machinery, and the lease renewal alone only
  needed dropping `-q`.

## The setup access point: channel, country and lease as on stock; SoundTap's own subnet

### Context

A comparison with stock 27.0.6 (NetManager) on 2026-11-18 found the setup AP fixed on
channel 6, without 802.11d, and with udhcpd's default lease. Stock picks the quietest of
channels 1, 6 and 11, always sends its country, and leases for 600 s. Stock serves
192.0.2.1/24 (pool .2-.254); SoundTap serves 192.168.42.1/24 (pool .10-.50).

### Decision

As on stock: the manager picks the channel among 1, 6 and 11 with the fewest networks in the
scan it already makes (6 on a tie or without a scan; stock also weighs channel load, which
SoundTap does not measure), hostapd sends `country_code` with `ieee80211d=1` when the country is
known, and udhcpd leases for 600 s. The ST20 keeps channel 6 and no country element: its
`wlan1` is the BCO's radio behind `soundtap_bco` ([ADR 0031](0031-bco-network-card.md)), proven
on channel 6 only.

Kept on purpose: 192.168.42.1/24. The portal, the firewall and their tests use it, and a
phone on the AP sees no difference.

When DHCP fails, stock gives the station a link-local address (`avahi-autoipd`) and stays
on the network. SoundTap does not: after 90 s without a lease the manager opens the setup AP and
retries the saved network (above). A network without DHCP is rare, and a station with only
a 169.254 address cannot be set up from the app, while the AP can.

### Consequences

- In a crowded channel 6 the setup AP moves to 1 or 11, as stock's did.
- The setup AP's channel needs per-model hardware proof (the Wi-Fi rows of
  [the hardware checklist](../hardware/hardware-test.md)).

## The setup access point stays open, as on stock

### Context

When a speaker has no usable Wi-Fi, soundtapd's Wi-Fi manager opens a setup access point
(`openAP` in `app/internal/wifi/ap.go`): hostapd without WPA, plus catch-all DNS for the setup
page. The section above bounds the outage case: with saved credentials, the saved network is
retried every 5 minutes while no client is on the AP. That leaves the question of whether the AP
itself should be open.

Stock Bose firmware does the same: its setup network is open. The ST10 and the Wireless Link
have no screen, so a per-device passphrase would have to be printed on a label, and the
speakers carry Bose's label, not SoundTap's.

### Decision

The setup AP stays open, as on stock. Project decision, 2026-10-09.

### Consequences

- Setup works on every model with no label, screen or button chord, the same way people
  already know from stock.
- Anyone in range can join the AP while it is up: when no Wi-Fi is configured, and during a
  router outage until the next retry. There is no household token
  ([ADR 0055](0055-household-token-and-windows.md)), so a phone on the AP reaches the same routes
  as on the home network; dangerous routes still need the press on the speaker, and installs a
  signature ([ADR 0048](0048-signed-updates.md)).

### Alternatives that lost

- **WPA2 with a per-device PSK.** Lost because the ST10 and WLA have no screen, and a label
  that SoundTap does not ship cannot carry it.
- **Open the AP only after a button chord when credentials exist.** Lost because it differs
  from stock and strands a speaker whose router changed, until someone finds the chord.
