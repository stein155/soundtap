# 0135. BCO network card: standard firmware features

Status: accepted — 2026-11-26

## Context

The ST20's BCO runs SoundTap's own app as the SCM's network card ([ADR 0031](0031-bco-network-card.md)).
Until now it lacked what a Wi-Fi co-processor's firmware normally does for its host driver:

- A deauthentication, a disassociation or beacon loss ended the link on the BCO, but the SCM's
  driver only learned of it at its next STATUS poll, up to 5 s later, and reported every loss
  with an unspecified reason.
- A speaker joined the strongest access point of its network once and kept it, however weak it
  became, while another access point of the same network was much stronger.
- The BCO scanned and probed on channels 1-13 whatever the speaker's country, and the SCM's
  regulatory domain (soundtapd sets the country from the manufacturing data,
  [ADR 0063](0063-wifi-lease-and-setup-ap.md)) never reached it.
- The directed probe for a hidden network named only the profile's SSID, so a hidden network
  stayed nameless in the scan list until a profile was already set.
- Whether the ST20 keeps an antenna setting in its NOR is not known, and nothing could look.

The stock BCO app is not among the local stock copies, so none of this follows a stock
behaviour; the rules come from how Linux attaches FullMAC co-processors (`brcmfmac`, `mwifiex`)
and from `wpa_supplicant`'s bgscan simple.

## Decision

Each feature is a capability bit in HELLO, at the same protocol level: the driver uses it only
when the app announces it, and the app sends nothing new and changes no behaviour until the driver
asks, so an older app with a newer driver and the other way round work as before
([the details](../hardware/st20/bco-network-card.md#link-events-roaming-and-the-regulatory-domain)).

- **Link events** (`BCOLINK_CAP_EVENTS`). Switched on with `EVENTS`, the app sends an unasked
  control message the moment it drops the link: deauthenticated or disassociated (with the access
  point's reason code), beacon loss (8 s without a beacon), a wedged transmitter, a roam, a
  failed roam, and an access point whose beacon changed the Privacy bit the profile needs (the app
  left it, [ADR 0136](0136-every-network-stock-joined-and-wpa3.md)). The driver runs its STATUS poll at once and reports `cfg80211_disconnected` with the
  access point's reason, or beacon loss as locally generated, and takes the carrier down. The
  5 s poll stays as the fallback.
- **Roaming within the SSID** (`BCOLINK_CAP_ROAM`). SET_PROFILE gets a tail with a BSSID and
  flags: fixed (cfg80211's `bssid`), preferred (`bssid_hint`) and roaming. The driver announces
  `WIPHY_FLAG_SUPPORTS_FW_ROAM`, so `wpa_supplicant` hands a hint, not a fixed BSSID. While the
  averaged level of its access point is below -70 dBm the app scans in the background one channel
  per visit, back on its own channel in between, and stops looking once it is 5 dB above that. It
  roams only to an access point of the same SSID and security at least 8 dB stronger; a scan that
  finds none doubles the time to the next, from 30 s up to 300 s. Above the threshold it never
  leaves its channel on its own. A SCAN while joined runs the same visits over every allowed
  channel, its own too, with a wildcard probe and 300 ms home in between (about 7 s), so the scan
  list holds what is in range now; it never roams on a strong link. The driver keeps the connection while the app reassociates and reports
  `cfg80211_roamed` and the open port. A fixed BSSID never roams.
- **Regulatory domain** (`BCOLINK_CAP_REGDOM`). The driver's `reg_notifier` sends `SET_REGDOM`
  with the country, the channels cfg80211 left enabled, those it marked no-IR as passive, and the
  lowest power limit among them. The app scans, joins and starts its access point only on allowed
  channels, and never probes or beacons on a passive one. The TX power limit is recorded and not
  applied: SoundTap's WiLD core has no known TX power control and none is guessed; the driver logs that.
  soundtapd needs no change: it already sets the country on every model.
- **The scan's SSID** (`BCOLINK_CAP_SCAN_SSID`). SCAN carries the SSID of `wpa_supplicant`'s scan
  request, and the scan's directed pass probes for it, so a hidden network answers before any
  profile is set.
- **Antenna setting, read path only** (`BCOLINK_CAP_NVINFO`). `NV_INFO` lists the keys of the
  BCO's PersParam log through the NOR's memory map, with each value's length and only values of at
  most 4 bytes, so no passphrase or PMK leaves the BCO. The driver asks only when root reads
  `/sys/class/net/bcolink0/bco/nv_keys`. Nothing reads it on its own, nothing applies an antenna
  setting, and there is no write path. Which key holds the setting is unknown until a read-only
  check at the speaker, with the owner's OK for that read, has run.

Alternatives that lost:

- **Roaming in `wpa_supplicant` (bgscan and a new connect).** Every roam would be a full
  disconnect and connect through SET_PROFILE, and the BCO would have to report signal changes
  anyway; the app already holds the scan table and the handshake.
- **Off-channel scans that sweep all channels at once while joined.** About 2.6 s off the
  channel per scan, longer than a multiroom stream's buffer; one channel per visit keeps each
  absence to about 200 ms.
- **Scanning in the background above the threshold too** (bgscan simple's long interval). A
  speaker that streams gains nothing from it while its signal is good.
- **Applying the TX power limit through an RF register found by trial.** The contract forbids
  guessed BCO commands; the limit waits for a known control.
- **A regulatory setting in soundtapd sent over its own route.** cfg80211 already settles the
  domain from soundtapd's `iw reg set`; a notifier is how FullMAC drivers take it.
- **Reading a guessed antenna key at boot.** Would act on a key nobody has seen; the listing
  finds it without acting on it.

## Consequences

- A lost link ends the connection on the SCM within a moment, with a reason `wpa_supplicant` and
  the log can tell apart, and the reconnection starts at once.
- While its signal is weak the speaker leaves its channel for about 200 ms every 700 ms during a
  background scan; frames from the SCM wait in the qdisc (no credit) or, while it is away, are
  dropped. That is the price of finding a better access point; above -70 dBm it is never paid.
- The return to the own channel relies on the core's join keeping the peer and its keys, which
  holds in the WiLD core's code (`JOIN_DONE`, which drops them, is not sent) and is not yet proven on
  the speaker.
- Channels 12 and 13 are only listened on under the world domain; whether the WiLD core's passive
  listen receives on the ST20 after earlier probes is unproven (a listen without any transmit
  received nothing on the bench).
- The TX power limit is not enforced until a TX power control of the core is known from read-only
  analysis.
- Hardware proof is per candidate, in the ST20's rows of the
  [hardware checklist](../hardware/hardware-test.md): link events, roaming, the regulatory domain,
  the hidden network and the antenna read.

## Code

- `bco/app/src/bcolink.h`: `BCOLINK_CAP_EVENTS`, `BCOLINK_CAP_ROAM`, `BCOLINK_CAP_REGDOM`,
  `BCOLINK_CAP_SCAN_SSID`, `BCOLINK_CAP_NVINFO`.
- `bco/app/src/mgmt.h`, `bco/app/src/mgmt.c`: `EVENTS`, `SET_REGDOM`, `NV_INFO`, SET_PROFILE's tail,
  SCAN's SSID, `mgmt_event_due`; `bco/app/src/netcard.c`: `netcard_event_poll`, `mgmt_nv`.
- `bco/app/src/wifi.c`: `post`, `wifi_event_next`, `wifi_set_regdom`, `chan_ok`, `probe_on`,
  `listen_on`, `pick`, `bg_step`, `roam_to`, `go_home`; `bco/app/src/persparam.h`: `pp_keys`.
- `os/board/rootfs/linux-patches/0007-net-usb-add-soundtap-bco-link-driver.patch`: `bco_ctrl_rx`
  (events), `bco_join_poll` (`cfg80211_roamed`, `bco_report_lost`), `bco_connect` (the tail),
  `bco_reg_notifier`, `bco_reg_work`, `bco_scan_work` (the SSID), `nv_keys_show`.
- `bco/app/tests/wifi_roam_test.c`, `bco/app/tests/mgmt_test.c`, `bco/app/tests/netcard_test.c`,
  `bco/app/tests/persparam_test.c`, `os/tests/hardware/test_bcolink_contract.py`.
