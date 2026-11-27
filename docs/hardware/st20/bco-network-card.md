# The BCO network card in detail

Under SoundTap the ST20's BCO (DM870) runs SoundTap's own app as the SCM's network card
([ADR 0031](../../decisions/0031-bco-network-card.md) records the decision and its rules). This page
holds the detail behind it: the link protocol, how the setup access point and the Wi-Fi data path
use the WiLD core, and the measurements taken on the bench ST20. The app's build, first stage and
bring-up notes are in [the app README](../../../bco/app/README.md). A measurement here is a historical
observation on one board, not approval of a later image: release evidence belongs in the
[hardware checklist](../hardware-test.md).

## Contents

1. [The link](#the-link)
2. [Control messages](#control-messages)
3. [Choosing the network](#choosing-the-network)
4. [Protocol level and resent requests](#protocol-level-and-resent-requests)
5. [Flow control and the counters](#flow-control-and-the-counters)
6. [Link events, roaming and the regulatory domain](#link-events-roaming-and-the-regulatory-domain)
7. [Antenna setting](#antenna-setting)
8. [The card's clock](#the-cards-clock)
9. [The setup access point on the WiLD core](#the-setup-access-point-on-the-wild-core)
10. [CCMP on the core's cipher engine](#ccmp-on-the-cores-cipher-engine)
11. [WEP and TKIP: stock's engine modes, and where SoundTap runs them](#wep-and-tkip-stocks-engine-modes-and-where-soundtap-runs-them)
12. [WPA3 and PMF](#wpa3-and-pmf)
13. [Medium access and rate adaptation](#medium-access-and-rate-adaptation)
14. [Measurements on the bench ST20](#measurements-on-the-bench-st20)

## The link

How Linux normally attaches a Wi-Fi co-processor over USB: one driver owns the USB function and
creates the network interfaces itself; frames carry a small per-frame header that names the
interface or channel; control and data share the bulk pipes. `brcmfmac` (BDC header, interface
index), `mwifiex` (BSS index), `rndis_wlan` (usbnet minidriver with a cfg80211 `wlan0`) and the
`qmi_wwan` multiplexer all work this way. The BCO link follows that pattern.

- The BCO enumerates one interface, class `0xff`, with a bulk IN and a bulk OUT endpoint (512
  bytes, high speed). The SCM driver `soundtap_bco`, a usbnet minidriver like `rndis_wlan`, binds
  to it by vendor/product and interface class. Development uses the pid.codes test ID `1209:0001`.
- Every message is `le16 hdr` followed by its payload; `hdr = channel << 12 | length`, length ≤
  1536 bytes of payload (`bco/app/src/bcolink.h`, mirrored in the driver). Channel 0 is control,
  1 is Wi-Fi, 2 is the wired port; data payloads are whole Ethernet frames. A transfer carries one
  message and ends with a short packet (a zero-length packet when it is a multiple of 512); the
  receiver also accepts trailing padding, because the length is in the header. The two-byte
  header leaves the IP header 4-byte aligned.
- `wlan0` is a cfg80211 FullMAC station whose data travels on channel 1, with the device's own
  Wi-Fi MAC (PersParam `MAC0`, reported in `HELLO`), so the access point sees one station. `scan`
  maps to SCAN, `connect` to SET_PROFILE with the handshake offloaded
  (`NL80211_EXT_FEATURE_4WAY_HANDSHAKE_STA_PSK`), `disconnect` to FORGET and station info to
  STATUS; STATUS is polled while connected and a drop reports a disconnect, at once when the app
  sends a [link event](#link-events-roaming-and-the-regulatory-domain). `eth0` is the wired
  port on channel 2. The usbnet device itself is the transport (`bcolink0`, no addresses); the
  driver brings it up with its children.
- On the BCO, channel 1 goes to the Wi-Fi station (Ethernet ↔ 802.11 data with the device MAC),
  channel 2 to the GEM (`bco/app/src/wired.c`), channel 0 to the management server
  (`bco/app/src/mgmt.c`). The BCO's own DHCP client is a hardware self-test only.
- The BCO has about 16 MB of RAM and a slow debug UART (about 42 ms per character), so the data
  path needs buffer discipline, and tests count frames instead of logging them. A UART line
  busy-waits, and USB and the receive rings wait with it, so the shipped image (`link-own`)
  writes one only at boot and for rare events: a failed join, a lost link, a wedged transmitter.
  The per-channel scan lines, the scan summary, the per-state join trace and the three-second
  receive-ring heartbeat are in the bench build `link-own-trace` only
  ([the app README](../../../bco/app/README.md#build)).

## Control messages

Control messages on channel 0 are `u8 type, u16 seq` and a payload; the SCM retries on silence
and the BCO answers with the same `seq` and `type|0x80`, status first.

- `HELLO`: firmware version, Wi-Fi MAC, state, protocol level and capability bits.
- `SCAN`: payload `{1}` starts a scan, `{1, ssid_len, ssid}` starts one whose directed pass probes
  for that SSID (`BCOLINK_CAP_SCAN_SSID`), an empty payload polls; status 3 means still scanning,
  status 0 carries the results (`ssid_len, ssid, bssid[6], channel, rssi i8, security`: 0 open,
  1 RSN, 2 WPA1 only, 3 WEP: the Privacy bit without either IE). With `BCOLINK_CAP_SECURITY` the
  first byte is a set of flags, 1 start and 2 detail, and a poll `{2}` asks for the detail too: six
  bytes after each entry's security byte, the RSN group cipher suite type, the RSN pairwise set (1
  CCMP, 2 TKIP), the RSN AKM set (1 PSK), the RSN capabilities' low byte, the WPA IE's group and its
  pairwise set. The driver builds each network's RSN and WPA IE from them for its scan cache. A scan is two passes over the channels the regulatory domain allows (1-13
  until one is set), 200 ms per channel, probing on each but a passive one, where it only listens;
  while connected the cached list is returned. The second pass probes for the SCAN's SSID, else the
  profile's ([choosing the network](#choosing-the-network)).
- `SET_PROFILE`: `ssid_len, ssid, security, key_len, key, 32 random bytes` from the SCM kernel for
  the SNonce, and with `BCOLINK_CAP_ROAM` a tail of `flags, bssid[6]` (1 this BSSID only, 2 this
  BSSID first, 4 roam within the SSID). Without the tail the app joins any BSSID and never roams.
  Security 1 is WPA2-PSK with the 32-byte PMK as the key; with `BCOLINK_CAP_SECURITY`
  ([ADR 0136](../../decisions/0136-every-network-stock-joined-and-wpa3.md)) security 0 is an open
  network, without a key, and 2 is WEP: the key index (0-3), the authentication (0 open system, 1
  shared key, 2 open system first and shared key when the access point refuses it with status
  13) and the 5- or 13-byte key. With `BCOLINK_CAP_WPA3` security 3 is WPA3-SAE, without a key
  ([WPA3 and PMF](#wpa3-and-pmf)). Anything else, or a key length that does not fit the security, is
  answered "unsupported" (status 2). 2.4 GHz only. The key is never echoed or logged.
- `FORGET` leaves the network without sending deauthentication and drops the profile; the WiLD
  core keeps running (a core reboot recalibrates the radio in busy waits that stall USB; the app
  reboots the core only when its transmitter wedges). `STATUS` reports state, network, channel
  and RSSI, then why the last join failed: a step (`WIFI_FAIL_*` in `bco/app/src/wifi.h`: not
  found, security it cannot join, authentication or association refused or unanswered, handshake
  not completed, sent away) and the AP's IEEE status or reason code. The driver passes it to
  wpa_supplicant as the connect result: the AP's own status for a refusal, 15 for a handshake that
  did not complete (wpa_supplicant never sees a wrong password itself with the handshake
  offloaded), 43 for security it cannot join, and a scan, authentication or association timeout.
  soundtapd reads 15 as a wrong password. Last comes the network's RSN group cipher (2 TKIP, 4
  CCMP, 0 unknown), which the driver reads only when present and logs with the join result
  (`BCO joined …, group cipher …` or `BCO join failed: step …, code …, group cipher …`); a TKIP
  group's broadcasts the app deciphers itself (an app before `BCOLINK_CAP_SECURITY` dropped them).
  After it comes what the network offers, as in
  SCAN (0 open, 1 RSN, 2 WPA1, 3 WEP) and its six bytes of detail, which the driver puts in its
  scan cache for a roam or a join it reports. `SCAN` lists what fits in one reply (1536
  bytes: 36 networks when every SSID is 32 bytes long), its count matching. A join that runs past
  90 s, above the app's worst case of about 68 s (scan, then five tries of authentication,
  association and handshake), makes the driver send `FORGET` and report a timeout, so the BCO
  never keeps joining behind a new connect.
- The scan table always makes room for the profile's SSID and the directed scan's: a full table of
  neighbours drops a stranger, never the owner's network. The 4-way takes only a frame whose MIC
  verified as the replay reference: a resent Message 1 is answered, and a forged one cannot lock
  the handshake out. EAPOL goes in the clear until the AP is seen using the pairwise key, so a
  Message 4 resent after a lost one stays readable. A rekey's Message 4 goes out under the
  pairwise key in use: the core enciphers a frame when it sends it, so the new key is handed to it
  only once Message 4's buffer is back (the access point switches keys on that Message 4). A
  GTK or IGTK the access point sends again (a resent group message, or a rekey's Message 3 with
  the group key unchanged) is not installed again, as wpa_supplicant does, so its receive counter
  never goes back; a new one starts from the frame's Key RSC or IPN, the app's TKIP group key too.
  A deauthentication or disassociation to every station from the joined BSS ends the link. The
  setup access point keeps an associated client's slot: a seventh newcomer gets status 17, and
  only an authenticated-only client or one silent for 300 s gives its slot up.
- `AP_START` (6: channel, beacon interval in TU, beacon body), `AP_STOP` (7) and `AP_STATUS` (8:
  the associated clients) drive the setup access point.
- `FLOW` (13: `1` on, `0` off) switches the credit reports of
  [flow control](#flow-control-and-the-counters) on or off; its reply is a report.
- `LINK_STATS` (14): status, then five le32 counters since the app started: Wi-Fi frames
  acknowledged, failed on air after the retries, dropped before the air (no free transmit buffer,
  not joined), messages the link dropped on any channel, and frames from the SCM the radio took.
- `EVENTS` (15: `1` on, `0` off; reply status, on) switches the unasked link events on; `SET_REGDOM`
  (16: `country[2], le16 channels, le16 passive, i8 dBm`; reply status, enforced) sets the
  regulatory domain; `NV_INFO` (17, read-only) lists the PersParam keys
  ([link events, roaming and the regulatory domain](#link-events-roaming-and-the-regulatory-domain),
  [antenna setting](#antenna-setting)).

## Choosing the network

- SET_PROFILE joins the strongest access point on channels 1-13 with the profile's SSID and a
  security it matches (an open profile only an access point without the Privacy bit; a PSK
  profile one whose RSN or WPA IE offers PSK with pairwise CCMP, or pairwise TKIP with a TKIP group,
  joined over RSN before WPA and with CCMP before TKIP, ADR 0136): the highest level of its last beacon or probe response, and on a tie the one heard in the later
  scan. An access point heard without a level counts as the weakest. The scan table keeps a BSS
  heard in any of the last five scans.
- A hidden network leaves its SSID out of the beacon. The scan's second pass sends a probe request
  naming the profile's SSID (the core's probe from payload bytes 6 onward), the access point
  answers it with its name, and the scan table keeps that name: a later beacon without the SSID
  does not erase it. The first pass stays a wildcard, so the list still shows every network. The
  driver puts the SSID of `wpa_supplicant`'s scan request (`scan_ssid=1`, which soundtapd writes
  for every network) into SCAN (ADR 0135), so the first join of a hidden network finds it by name;
  without a SCAN SSID the profile's is probed, as before. A first join of a hidden network on the
  speaker is still open (the hardware checklist's hidden-network row).
- SET_PROFILE's tail names a BSSID: a fixed one is the only one joined, a hinted one is joined
  first when it was heard, else the strongest. `wpa_supplicant` sends a hint, not a fixed BSSID, to
  a driver that roams by itself (`WIPHY_FLAG_SUPPORTS_FW_ROAM`), unless the network names a
  `bssid`.
- An open network's association request carries no security IE and no Privacy bit, JOIN_DONE goes
  without privacy, the link is up once the access point associates, and data goes both ways in the
  clear.
- A WEP profile joins an access point with the Privacy bit and neither an RSN nor a WPA IE. Shared
  key authentication answers the access point's challenge in transaction 3, enciphered under the
  profile's key (`TX_RAW`, the Protected bit set); the association carries Privacy and no IE; then
  the key goes to the core as a mark for its index and the link is up. Frames go out under that key
  with an IV that starts from the SCM's random seed and counts up, and come in under any of the
  default keys the core passes up by key id (only the profile's is installed).
- The association request's capability is ESS and Privacy (not for an open network), plus Short
  Preamble and Short Slot Time when the access point's own capability has them, as an ERP station sends: a station that
  leaves Short Slot out makes the access point move the whole BSS to the long slot.

## Protocol level and resent requests

- HELLO ends with a one-byte protocol level and le32 capability bits (`BCOLINK_CAP_AP`,
  `BCOLINK_CAP_REPLAY`, `BCOLINK_CAP_FLOW`, `BCOLINK_CAP_STATS`). The level is `BCOLINK_PROTO` in
  `bco/app/src/bcolink.h` (the version string is `link-<level>`), and it goes up in the commit that
  changes a message's layout or meaning. A message the other side may ignore keeps the level and
  adds a capability bit: the driver sends it only to an app that announces the bit, and the app
  sends nothing unasked until the driver switched it on.
- The driver compares the level with its own `BCO_LINK_PROTO`. A different level is refused with
  a log line naming both levels and saying to flash a matching BCO app. A reply without the field
  is accepted with a warning: refusing it would take the ST20 off the LAN, which is also the way a
  new app arrives.
- The driver resends a request three times when no reply comes within 500 ms, with the same seq,
  and starts its seq at a random value so that a reloaded driver cannot collide with the key the
  app still holds. The app keeps the reply to the last state-changing request (SET_PROFILE,
  FORGET, AP_START, AP_STOP), keyed on type and seq, and sends it again for a repeated key instead
  of running the request; queries run again. Only the reply is kept, never the request, because
  SET_PROFILE carries the PMK.
- `os/tests/hardware/test_bcolink_contract.py` holds both sides to the same framing values,
  protocol level, capability bits, report layouts, message numbers and USB match, and checks the
  patch's hunk count.

## Flow control and the counters

The radio sends one frame at a time and retries it; USB delivers frames far faster. Before flow
control the app dropped a channel-1 frame that found no free transmit buffer (`wifi_tx_drop`), and
the SCM never knew: on the owner's ST20 leading one Snapcast follower (alpha.3, about 1.5 Mbit/s
up) about one TCP segment in ten was retransmitted, with no drops in the kernel, and the follower
disconnected during the bursts.

- The app has seven transmit buffers (`WIFI_TX_BUFS`). It reports `free`, the buffers the SCM may
  fill now (the free ones less one kept for its own authentication, association and EAPOL frames),
  and `taken`, the channel-1 messages it has taken off the link since it started, accepted or
  dropped. A report is the reply to `FLOW` and a control message the app sends unasked:
  `type 13 | 0x80`, seq 0, payload `status 0, free, total, on, le32 taken`.
- The driver counts the channel-1 frames it sends (`tx_sent`). While `tx_sent - taken` reaches
  `free`, it stops the sending port's queue (`wlan0`, or `wlan1` while the access point runs); the
  next report with credit wakes both. Frames then wait in that port's qdisc, where TCP Small Queues
  hold each flow back, instead of being dropped on the BCO.
- The app sends a report when buffers came free that the driver cannot know of yet (its last report's
  `free` less what was taken since), at most once per main-loop pass, and every 100 ms while
  reports are on. A report the link cannot take is sent again on the next pass.
- `FLOW`'s reply starts the count afresh, as if nothing were in flight. The driver asks again when
  a queue has been held for a second without credit (a lost report or a frame lost on the link)
  and when `bcolink0` comes back up. A BCO that refuses or does not answer leaves Wi-Fi without flow
  control until the link comes back.
- An app without `BCOLINK_CAP_FLOW` is never asked, and an older driver never asks, so either keeps
  the old behaviour. The wired port has no flow control: its MAC sends far faster than the radio.

The driver shows the app's `LINK_STATS` counters and its own state, read-only, in
`/sys/class/net/bcolink0/bco/`: `tx_ok`, `tx_failed`, `tx_dropped`, `link_dropped`, `tx_frames`
(asked of the BCO at most once a second; an app without `BCOLINK_CAP_STATS` answers `EOPNOTSUPP`),
`flow` (1 while frames are held for credit), `flow_stops` (times a queue was stopped) and
`flow_resyncs` (counts started afresh after a held second). The BCO counters are 32 bits from the
app's start, so they wrap and start over after a BCO power cycle. `/api/health` carries them as
`bco` on an ST20.

### The SRAM budget

The WiLD core reaches the app's buffers only through UMR0, the 64 KB internal SRAM
(`bco/app/src/stacks.h`); the 16 MB SDRAM is out of its reach. A transmit buffer is `0x6d0` bytes:
the 8-byte record, the `0xc4` descriptor, LLC/SNAP, 1500 bytes and the CCMP header and MIC the core
adds in place, or the IV, Michael MIC and ICV of a TKIP frame the app enciphered (20 bytes). A
receive buffer is `0x6e0` bytes, so a TKIP frame comes up whole for the app to decipher
([ADR 0136](../../decisions/0136-every-network-stock-joined-and-wpa3.md)).

| SRAM | Use |
|---|---|
| `0x0000-0x0fff` | vectors and trampolines |
| `0x1000-0x401f` | 7 receive buffers |
| `0x4100-0x70af` | 7 transmit buffers (6 until flow control) |
| `0x7220-0x959f` | the core's command rings |
| `0x9600-0xa13f` | frame and supply rings |
| `0xa200-0xd01f` | the wired port's MACB rings and buffers (`wired.c`) |
| `0xd020-0xd9e7` | unused by the app; the first stage's stack before the app runs |
| from `0xd9e8` | where BFL loads the first stage (960 bytes); unused by the app |

An eighth transmit buffer would have to go into the `0xd020` gap or above the first stage, apart
from the other seven, and neither area is proven free of BFL's use on hardware; the gaps at
`0x4020` and `0x70b0` are too small. `wifi.c` asserts at build time that the receive buffers end
below the transmit buffers and the seven transmit buffers below the command rings.

## Link events, roaming and the regulatory domain

[ADR 0135](../../decisions/0135-bco-standard-firmware-features.md) holds the decision; each feature
has a capability bit and the driver uses it only when HELLO announces it.

- **Link events.** With `EVENTS` on, the app sends `type 15 | 0x80`, seq 0, payload `status 0,
  event, le16 reason, bssid[6], channel, rssi` the moment it drops the link: 1 deauthenticated
  and 2 disassociated (with the access point's reason code), 3 beacon loss (no beacon for 8 s,
  `WIFI_BEACON_LOSS_MS`), 4 the transmitter wedged and the core was rebooted, 5 roamed, 6 a roam
  failed. The app keeps the newest four; a report the link cannot take is sent again. The driver
  runs its STATUS poll at once and reports `cfg80211_disconnected` with the access point's reason
  (not locally generated) or, for beacon loss, `WLAN_REASON_DISASSOC_DUE_TO_INACTIVITY` (locally
  generated), and takes `wlan0`'s carrier down. Without events the 5 s poll still finds it.
- **Roaming**, like `wpa_supplicant`'s bgscan simple. The app averages the beacons of its access
  point (each moves the average an eighth). Below -70 dBm it scans in the background, 30 s after
  the previous scan at the soonest; above -65 dBm it stops. A background scan visits one allowed
  channel at a time other than its own: it gives the SCM no new credit and waits up to 50 ms for
  the transmit buffers to empty, sends one probe for the SSID (a passive channel: listens), stays
  200 ms, then sends the core's join command (`0xbc`) for its own access point and channel, which
  retunes the radio and keeps the peer and its keys (`JOIN_DONE`, which drops them, is not sent),
  and stays home 500 ms. Frames from the SCM while away are dropped (`tx_dropped`); beacon loss
  does not count the time away. After the last channel it roams to the strongest access point of
  the SSID heard in that scan when that one is at least 8 dB above its own average: it leaves the
  old one locally (nothing on air), joins the new one with the same PMK and a fresh 4-way, and
  reports `roamed`. Otherwise the next scan waits twice as long, up to 300 s. The driver keeps the
  connection while STATUS says joining during a roam and reports `cfg80211_roamed` and the open
  port when STATUS names a new BSSID. A fixed BSSID, or a profile without the roam flag, never
  leaves its channel.
- **Regulatory domain.** The driver's `reg_notifier` turns the domain cfg80211 settles on (soundtapd
  runs `iw reg set` with the speaker's country, [ADR 0063](../../decisions/0063-wifi-lease-and-setup-ap.md))
  into SET_REGDOM: the enabled channels, the `NO_IR` ones as passive, and the lowest power limit
  among them. The app scans and joins only those channels, never probes on a passive one and never
  starts the access point on one. Until SET_REGDOM it allows 1-13, as before. The TX power limit
  is recorded, not applied: the core has no known TX power control, and none is guessed; the
  driver logs that.

## Antenna setting

Whether the ST20's BCO keeps an antenna setting in its NOR, and under which PersParam key, is not
known: no stock file on hand records it, and the stock BCO app is not among the local stock
copies. The read path exists so that a check can find it without writing anything: `NV_INFO` makes
the app walk the PersParam log (`pp_keys` in `bco/app/src/persparam.h`) through the NOR's memory
map, as it reads `MAC0` at boot, and answer each key with its value length, plus the value when it
is at most 4 bytes, so no passphrase or PMK leaves the BCO. The driver asks only when root reads
`/sys/class/net/bcolink0/bco/nv_keys`; nothing reads it on its own, and the app applies no antenna
setting. The read has not been run: it needs a read-only check at the speaker with the owner's OK
for that read (the hardware checklist's antenna row), and the key it finds is recorded here.

## The card's clock

The BCO keeps time from the host's 1 ms SOF frames (`usblink_ms`), not from a timer interrupt:
after the clock replay that brings up the MUSB, the timer IRQ does not fire. Linux must keep the
USB link active for that clock to run, so the driver does not enable runtime autosuspend. USB
starts before the radio, because that replay also resets the WiLD core.

## The setup access point on the WiLD core

Stock builds its access point in the BCO app's own C++ MLME: AP start is command type 15 to
module 4, which hands stock's WiLD core a beacon template (core commands `0xe2`, the fixed fields
and IEs before the TIM, and `0xe3`, the IEs after it) and then sets the BSS up with `0xbc`, its own
MAC as BSSID. Stock's core then beacons on its own TBTT timer, inserts the TIM and answers probe
requests from the template (read from `0x6003bfb0` case `0x5b`, `0x60073c90` and `0x60073e84`).
That C++ code is not SoundTap's and is not re-implemented.

SoundTap's WiLD core (`bco/wild/fw`) has neither the template commands nor the TBTT timer. Until
2026-11-26 the app still sent `0xe2`/`0xe3`, which the core dropped without a word, so the AP
reported itself up while nothing went on air (seen on two ST20s: hostapd up, no network on any
phone). Now:

- SoundTap's app (`bco/app/src/wifi.c`) sends `0xbc` with the BCO's own MAC as BSSID, the channel
  and the beacon interval. A join on its own address puts the core in `ap_mode`: it tunes the
  channel and passes probe requests to the app, a wildcard one without the address filter. Once
  the core reports the BSS (`0xbd`, or after a second) the app sends `0xb8` without privacy.
- The app sends a beacon at each target beacon transmission time from its main loop: hostapd's
  body (TIM included) behind a broadcast header, BSSID = own MAC, at 1 Mb/s, at every TSF that is
  a multiple of the beacon interval (TSF = microseconds since the BSS came up) and stamped with
  that TSF. A dozing phone wakes at those times. Until 2026-11-27 beacons went every 102 ms instead
  of 102.4 ms (100 TU) stamped with the millisecond clock, so they drifted 0.4 ms a beacon against
  the times a phone wakes at. A late poll sends one beacon, not a burst. A wildcard probe request, or one for
  the AP's SSID, gets a probe response with the same body minus the TIM.
- The app answers open-system authentication and (re)association itself, for up to six clients
  (one core peer slot each).
- HELLO's `BCOLINK_CAP_AP` says the app runs an access point; the driver offers AP mode (and with
  it `wlan1` to hostapd) only then, so an app without it no longer makes the speaker leave its
  network for an AP that never goes on air.
- Frames from associated clients (toDS) go to the SCM as Ethernet frames (DA = addr3, SA =
  addr2); frames from the SCM go out fromDS to an associated client or to every client. The core
  sends group-addressed data at the basic rate (1 Mb/s, as management frames) and unicast data at
  its data rate; a probe response gets one retry, as the phone that asked has moved on to the next
  channel by then. While the access point runs it owns link channel 1; the station is left first.
  There is no intra-BSS relay.
- In `ap_mode` the core counts a unicast frame whose A1 is the BCO's own address as passing the
  address filter (`mac.c`): that filter is set up for a station, and whether it flags a toDS frame
  was never measured. A phone that joined the setup network on alpha.6 got no DHCP lease and
  udhcpd logged nothing (2026-11-27). So a client's data, null-data, QoS data and PS-Poll are kept
  and ACKed; QoS data goes up without its QoS control, a PS-Poll padded to a 24-byte header. Other
  networks' group data on the channel is not kept: it filled the core's four-frame receive queue.
- Power save: the app follows each client's power-management bit (data and null-data frames).
  Unicast frames for a dozing client are held, four at most (the oldest goes), in SDRAM past the
  image (`app.ld` `.noinit`), and its bit (AID = slot + 1) is set in the beacon's TIM; a PS-Poll
  fetches one frame with More Data while more wait, a frame with the bit clear fetches the rest.
  Every beacon is a DTIM beacon (count 0, period 1); while any client dozes, group frames wait for
  the next one, which carries the group bit, and follow it.
- `AP_STATUS` (`bco/app/src/mgmt.h`): status, count, the client MACs and eight counters (AP up,
  probe requests, frames heard, transmits ACKed, lost, dropped, frames to the SCM, beacons); behind
  `BCOLINK_CAP_APDIAG` then the ms from `AP_START` to the first beacon, the core's frames to our
  address, those the address filter missed, client data kept (its flight recorder, which
  `HOST_FLAG2` locates), held frames lost, and per client: dozing, frames held, ms since last
  heard, data and null-data from it, unicast frames to it ACKed and lost, and the level and rate
  code of its last frame; after the clients the core's damaged frames to our address (bad FCS)
  with the last one's rate index, level and frame control, a client's frames with a body with the
  last one's, and those that found its receive queue full. A client not heard for 90 s has left
  without a word: it is not listed and holds no group frame back. `AP_STOP` drops every peer in
  the core (`0xb7`), which ends its `ap_mode`. The driver lists the
  clients for `iw dev wlan1 station dump` (inactive time, packets, failures), which soundtapd's
  hold reads to keep the setup network up while a phone is on it (before, the dump was always
  empty and the hold took the network for idle), and writes the whole reply to
  `/sys/class/net/bcolink0/bco/ap` as `name value` lines. soundtapd looks at it every 10 s and logs
  it (`setup AP status`, a `setup AP client` line per client by the last three octets of its
  address) at once when a phone joins, leaves or sends data or a frame reaches `wlan1`, and
  otherwise at most once a minute and the first beacon once (`setup AP beaconing`, with the time since `starting the
  setup AP`); udhcpd runs with `-v`, so each DISCOVER and REQUEST is in the log next to the OFFER
  and ACK.
- On the SCM, `soundtap_bco` advertises AP mode, a STA + AP interface combination on one channel
  and `WIPHY_FLAG_HAVE_AP_SME` (the device does authentication and association, as `brcmfmac`).
  `start_ap` sends hostapd's beacon (head + a TIM + tail) down as `AP_START`; `stop_ap` sends
  `AP_STOP`. Beacon updates after start (`change_beacon`) are not passed on, and hostapd gets no
  new-station events; `iw dev wlan1 station get` asks the BCO.
- The radio stays on the access point's channel, so a scan while it runs returns the scan made before it. Beacons come
  from the main loop, so their spacing follows its polling: a few milliseconds of jitter, which
  clients tolerate.
- Seen on air on alpha.6 (2026-11-27): the network appeared on a phone after a while and the
  phone joined, but got no address. With the receive rule above and power save (same day) the
  phone's frames reached the core and passed its filter, yet over 90 s the app saw 347 null-data
  frames and one data frame from it, and no DHCP request reached udhcpd. Null-data goes at a low
  rate, data at the top OFDM rates, so the ST20's `hostapd.conf` now offers rates up to 24 Mb/s
  (11b rates basic), and the damaged-frame counters and each client's last rate and level show
  whether the radio hears the phone's data at all. The third test (same day, a few metres): the
  radio heard the phone well (−57 to −60 dBm, 15 damaged frames) but the phone sent no frame with
  a body at all, only null-data, about six a second, dozing within seconds: the phone itself never
  started DHCP. That is what a phone does when it misses its beacons (the drift above) or does not
  hear its frames acknowledged, so the beacons now keep their target times, and `AP_STATUS` counts
  the core's ACKs (with the last one's rate class, the rate it answered and the frame it
  acknowledged) and per client the frames with the Retry bit, those repeating a sequence number
  and the last sequence control (`retries`, `repeats`, `seq` on its line in
  `/sys/class/net/bcolink0/bco/ap`). A phone getting an address, and how soon the network
  appears, is the hardware checklist's setup-AP row for the ST20.

## CCMP on the core's cipher engine

Known-answer tests in the core at boot (IEEE 802.11 Annex M CCMP vector) and a targeted read of
stock's engine setup (`0x5a10`, `0x5ec0`, `0x60e8`, `0x5240`, `0x4634`) showed how the `0xb00`
cipher engine behaves:

- Channel A enciphers: mode `0x44` (`| 2` for the last fragment). Stock also sets `0x018` bit 1
  around each run. Completion is `0xb18` bit 5, after about 75 polls for 64 bytes.
- Channel B deciphers: mode `0x40`, block at `0xb0c`, start with `0xb04` = 2. It writes the
  plaintext to the destination and reads the MIC after the ciphertext. Completion is `0xb18` bit
  21; bits 16-18 carry errors, and a tampered byte sets bit 18. Bit 24 stays set once B has run,
  so it cannot serve as a done bit: polling it acknowledges the engine mid-run.

How the core uses it (`bco/wild/fw/src/xport.c`, `bco/wild/fw/src/sta.c`):

1. Every protected frame it transmits is enciphered on channel A (`mac_ccmp_tx`): mode `0x46`,
   the destination peer's pairwise key and the next PN, in place in the staged packet. It polls
   `0xb18` bit 5 (bounded) and acknowledges on `0xb14`. If the engine does not finish, or the
   destination's peer slot has no pairwise key, the frame is dropped and its record goes back as
   failed; it is never sent in clear. A pairwise key belongs to its peer slot: PEER_DEL, dropping
   all peers, JOIN_DONE and a new MAC in the slot remove it, and an install for a MAC not in the
   peer table installs nothing and answers failure.
2. Every protected data frame it receives is deciphered on channel B (`mac_ccmp_rx`), with the
   transmitter's pairwise key or the group key its key id names (`sta_rxkey`). Header and body are
   first copied to the word-aligned RX staging buffer.
3. The PN must be above the key's last accepted one. A group key starts from the Key RSC the
   access point sent with it (the app passes it in bytes 24-29 of the install), a pairwise key
   from 0. A frame without a key, with an old PN or with a MIC error is dropped in the core. The PN is recorded only once the frame has a host
   buffer.
4. A received frame goes up as plaintext with the protected bit clear and no CCMP header or MIC.
   Record byte 7 = 1 (`RX_DECIPHERED`) marks it. Once keyed, the app accepts only marked frames.
5. Both channels run with IRQs off. Completion raises interrupt status bit 4, which the IRQ
   handler does not clear, so a run from the main loop with IRQs on re-enters the IRQ until the
   core hangs before its own acknowledge.
6. The BCO app does no CCMP; it keeps AES only for the key unwrap of the 4-way handshake
   (`bco/app/src/wpa.c`). A retry resends the staged frame unchanged, so it keeps its PN and
   ciphertext.

Software AES was too slow for the link: about 10 ms per 1400-byte frame on the core, 1.35 ms per
1.5 KB frame on the app's ARM926.

## WEP and TKIP: stock's engine modes, and where SoundTap runs them

Stock joins open, WEP, WPA-PSK and WPA2-PSK networks with AES or TKIP (its saved security values
are in [the stock profile](bco-recovery.md#6-the-stock-apps-saved-network)). Whether the `0xb00`
engine can do more than CCMP was read from stock's WiLD core, the image inside stock's BCO app
9423 (`os/stock/PROVENANCE.md`, "ST20 cipher engine modes"). The core starts at offset
`0x3b7db8` of the unpacked app image; its FIQ vector points at `0xa8bc`, the receive handler named
in [the receive path](#ccmp-on-the-cores-cipher-engine). Facts only, as stock uses them:

- The control block's first word is `header length << 24 | flags << 16 | key length << 8 | mode`.
  Stock picks the mode from the key's cipher suite type, the 802.11 suite selector: 1 WEP-40,
  2 TKIP, 4 CCMP, 5 WEP-104 (TX block builder `0x5a10`, TX start `0x5ec0`, RX `0x4634`).
- **WEP** (types 1 and 5): mode `0x24` to encipher, `0x20` to decipher. The key length field is
  the key length + 3, and the RC4 seed sits at block offset 40: the 3-byte IV, then the key from
  offset 43. TX writes the IV and the key id (`id << 6`) into the 4-byte header before the body;
  RX copies the IV from the frame and deciphers from body + 4, the data length being the body
  less 8 (IV and ICV).
- **TKIP** (type 2): mode `0x35` to encipher, `0x31` to decipher a frame's first or only
  fragment, `0x30` a later fragment; `| 2` marks the last fragment, as for CCMP. The 16-byte TK
  sits at offset 40 (key length 16), the 8-byte Michael key at offset 28, word 36 is 0, and the
  TSC goes in the PN words 5 and 6. Stock never mixes the key in software: the engine takes the
  TK, the TSC and the transmitter address from the header. The Michael key is bytes 24-31 of the
  32-byte TKIP key to transmit as a station, 16-23 to receive (16-23 and 24-31 in AP role). A
  later fragment carries the running Michael state (the previous run's words 28-36) instead of
  the key. Stock builds the 8-byte IV itself (`0x3b0c`): TSC1, `(TSC1 | 0x20) & 0x7f`, TSC0,
  `id << 6 | 0x20`, TSC2-TSC5. The data length is the body less 12 (IV, ICV) for a fragment
  that is not the last, less 20 (IV, Michael MIC, ICV) for the last.
- **Results** (`0xb18` at completion, `0x4a80`): bit 16 is an ICV failure, bit 18 a MIC failure.
  With a TKIP mode a MIC failure goes to stock's countermeasure path (`0x511c`); with CCMP it only
  counts. The mode the engine ran is read back from the block's first word, bits 4-6
  (`0x20` WEP, `0x30` TKIP, `0x40` CCMP).
- So the bits read: `0x40` AES-CCM, `0x20` RC4, `0x10` Michael on top of RC4, `0x01` Michael
  starts from the key, `0x04` encipher, `0x02` last fragment.

SoundTap has measured none of the WEP and TKIP modes on its own core (the CCMP modes were
measured bit-exact against software, above). Running them blind would mean a network that fails
outright if one field is off: the TKIP key half, the source of Michael's addresses and priority,
or the fragment state. So SoundTap runs WEP and TKIP in software in the BCO app, where the keys
already are, and keeps the engine for CCMP data ([ADR 0136](../../decisions/0136-every-network-stock-joined-and-wpa3.md)):

- The app enciphers WEP and TKIP frames itself and marks them so the core
  sends them as they are; the core passes a protected frame whose key the app installed as a
  software key up undeciphered, and the app deciphers it, checks the TSC, the ICV and the Michael
  MIC. The keys never leave the BCO.
- RC4 and Michael cost far less than AES, and the app's ARM926 with software AES still moved
  4 MB up in 7-9 s ([the measurements](#throughput-with-the-cipher-engine)); a WEP or TKIP
  network is at most 54 Mb/s anyway.
- Measuring the engine's modes against the software path, one mode at a time, is an open row in
  [the hardware checklist](../hardware-test.md); with them proven the core could take WEP and
  TKIP over as it does CCMP.

How it runs (`bco/app/src/wsec.c`, `bco/app/src/wifi.c`, the core's `sta.c` and `xport.c`):

1. A key install names the cipher suite type (`set_key`): CCMP goes to the engine with its key;
   WEP or TKIP goes with the type and no key bytes, and the core marks that key slot as the app's.
   The app keeps the key, and for TKIP the TSC it last sent and the one it last accepted. A
   32-byte TKIP key is the TK, the MIC key the AP sends with (bytes 16-23, which the station
   receives with) and the one it receives with (24-31).
2. Receive: a protected data frame under a slot of the app's comes up as it came, cipher header
   and trailer included, protected bit set, record byte 7 = 2 (`RX_SOFT`). A header without the
   ExtIV bit is WEP's, whose key id names a default key in the core's group table, for unicast and
   group frames alike. The app copies the body out, refuses a TSC that is not above the key's last
   one, deciphers, checks the ICV and then Michael (DA addr1, SA addr3, TA addr2, priority 0), and
   takes the TSC only then.
3. Transmit: under a TKIP pairwise key the app builds the MSDU, adds Michael (the station's MIC
   key) and the ICV, enciphers with the next TSC and sends the frame with descriptor byte `0x13` =
   1 (`TX_RAW`): the core sends that body as it is, without the engine and without a key of its
   own. A protected frame without the mark and without a CCMP key is still never sent.
4. A Michael failure (the ICV held, the MIC did not) is reported to the access point in an
   EAPOL-Key frame with Error and Request. A second within 60 s starts the countermeasures of IEEE
   802.11: a deauthentication with reason 14, the link dropped (a link event with reason 14), and no
   network with a TKIP group or pairwise cipher joined for 60 s.
5. The receive and transmit buffers grew by the 20 bytes of a TKIP frame
   ([the SRAM budget](#the-sram-budget)).

## WPA3 and PMF

[ADR 0136](../../decisions/0136-every-network-stock-joined-and-wpa3.md) records the decision; the
messages and steps (`BCOLINK_CAP_WPA3`):

1. SET_PROFILE security 3 (SAE). The BCO picks an access point whose RSN IE offers AKM 8 with
   pairwise and group CCMP and MFPC, sets the BSS up and sends JOIN_DONE, then asks unasked
   `EXT_AUTH` (18 | 0x80, seq 0): `status 0, action 1, bssid[6], ssid_len, ssid`. The driver calls
   `cfg80211_external_auth_request`, and wpa_supplicant starts SAE.
2. wpa_supplicant's SAE frames come to the driver's `mgmt_tx` and go down as `AUTH_FRAME` (19:
   the authentication body, algorithm 3); the BCO sends each to the access point. The access
   point's authentication frames of algorithm 3 go up unasked as `AUTH_FRAME` (19 | 0x80, seq 0:
   `status 0`, the 24-byte header and the body) and to wpa_supplicant through
   `cfg80211_rx_mgmt`. The driver reports each sent frame's status once its request was answered.
3. wpa_supplicant puts the PMK in its PMKSA cache; the driver's `set_pmksa` sends it as `SET_PMK`
   (20: `bssid[6]`, the 32-byte PMK). Then `external_auth` sends SAE's result as `EXT_AUTH` (18:
   `le16 status, bssid[6]`). With status 0 and the PMK the BCO associates with AKM 8 and MFPC
   (MFPR where the access point requires it) and runs the 4-way with that PMK: the 802.11
   KDF-SHA256 PTK, AES-CMAC MICs, key descriptor version 0. A refusal fails the attempt with
   wpa_supplicant's status; 15 s without a result is an authentication timeout; either sends
   `EXT_AUTH` with action 2 and tries again.
4. A PSK profile takes PSK-SHA256 (AKM 6, key descriptor version 3) where the access point offers
   it, and PMF wherever the access point offers it with pairwise CCMP; a network that requires
   PMF needs pairwise CCMP.
5. Under PMF the BCO keeps the IGTK from Message 3 and each group rekey. A robust management frame
   from the access point (deauthentication, disassociation, action) counts only protected:
   unicast under software CCMP with the PTK's TK, the nonce's Management flag set and a packet
   number above the last one; group-addressed with an MMIE that checks out under BIP-CMAC-128 with
   a newer IPN. An unprotected deauthentication or disassociation sends a protected SA Query
   (action category 8) and ends the link with its reason only when no protected answer with the
   same transaction id comes within 1 s. The access point's SA Query gets a protected answer. The
   BCO's own protected management frames count their packet numbers from 2^40, so they never meet
   the core's data packet numbers under the same key; receivers keep separate replay counters for
   management frames.
6. The driver offers SAE (`NL80211_FEATURE_SAE`), authentication frames on the station, BIP and
   optional PMF only with the capability; the access point's interface keeps no management frame
   types, as before.

## Medium access and rate adaptation

The core sends host frames on the management queue 9, because the access-class (EDCA) queues do
not transmit on this core. A queue-9 start with only the start bits goes out at once, without
waiting for the medium to be free and without a random backoff, so the core adds the 802.11 DCF
itself (`tx_kick`, `ACK_LATE`, `rate_cls` in `bco/wild/fw/src/xport.c`):

1. Join sets the backoff slot time (`0x6b0` = 9 us - 1). `mac_prep` leaves it at `0 - 1`, and with
   that value a start with backoff slots never goes out.
2. Every attempt of a host frame starts with a DCF backoff: `0x20000 | slots` on `0x674`. The
   slots are random in [0, CW]; CW starts at the core's CWmin and doubles per retry, up to 1023.
   A unicast frame gets seven attempts (`TX_RETRY` 6), 802.11's `dot11ShortRetryLimit`, which
   counts attempts; until flow control it got five.
3. Without an ACK 1 ms after the TX-done, the attempt is retried (`ACK_LATE`). The 5 ms limit from
   the start of the attempt stays as the upper bound. At the basic rate the wait after the TX-done
   is 3 ms.
4. Data starts at 54 Mb/s. Two frames in a row that needed a retry step the rate down (54 -> 36 ->
   24 Mb/s), and ten frames that went through at once step it up again. The last tries of a frame
   stay at the basic rate.

Limits: 24 Mb/s is the floor of the adaptation, because below it a frame outlasts `ACK_LATE`; a
weaker link falls back to the basic rate on the last tries. 9 us assumes a BSS with short slots
(every OFDM/ERP network); the association request asks for them when the access point offers
them ([choosing the network](#choosing-the-network)). A BSS with 11b-only stations would need
20 us, which is not handled.

Porting stock's EDCA scheduler would mean reading stock TX code, which stays off-limits; queue 9
with DCF backoff gives the same medium access for best-effort traffic.

## Measurements on the bench ST20

Historical observations on the one bench ST20 and its access point. Where no build or complete
capture is identified, that provenance is missing.

### LAN and API response

2026-10-06: `wpa_supplicant` reached COMPLETED in 12 s, `wlan0` took a DHCP lease, SoundTap
answered on :80 from the LAN and 1500-byte frames passed. Not every API route was exercised.

### SoundTap's first stage through the real BFL

2026-10-16: SoundTap's own first stage started on the ST20 through the real BFL, flashed through the BSL
XMODEM menu, with stock restorable the same way. With SoundTap's own WiLD core embedded in the app, the
BCO runs no Bose code above BFL/BSL. This is not a fault-injection test of every interrupted-flash
case.

### Throughput with the cipher engine

2026-10-22, one ST20 and access point:

| Image | 4 MB up | 16 MB down |
|---|---|---|
| stock firmware | 4-5 s | 14-16 s |
| app enciphers in software | 7-9 s | 11-16 s |
| core's cipher engine | 5-8 s | 10-14 s |

### Retries and the DCF backoff

Counters in the core during a 4 MB upload, before the backoff: 15-50 % of the frames needed a
retry, depending on how busy the air was; each retry waited the full 5 ms ACK timeout (3.2 s of
9 s in one upload); an ACK at the data rate arrived about 0.55 ms after the TX-done. The loss rate
was the same at 24, 36 and 54 Mb/s and higher at 18 Mb/s, where a frame is on air longer. That was
read as contention; these observations alone do not exclude other radio or link problems.

Against the image without the backoff and rate adaptation, alternating within the same hour:

| | 4 MB up | 16 MB down |
|---|---|---|
| with the backoff | 5-6 s | 16 s (two outliers of 30 and 44 s) |
| without it | 6-8 s | 16-19 s (one outlier of 49 s) |

- About 13 % of first attempts still needed a retry; the retries cost 0.8 s per 4 MB, against
  2-3 s before.
- Retrying 1 ms after the TX-done without the backoff made uploads slower (7-9 s), because the
  retries met the same busy medium. Keeping the 5 ms ACK wait and only adding the backoff gave 7 s.
- Fixed rates: 54 Mb/s took 4 MB up in 4-5 s, 36 Mb/s in 5-6 s and 24 Mb/s in 6-7 s.
