# The BCO network card in detail

Under SoundTap the ST20's BCO (DM870) runs our own app as the SCM's network card
([ADR 0031](../../decisions/0031-bco-network-card.md) records the decision and its rules). This page
holds the detail behind it: the link protocol, how the setup access point and the Wi-Fi data path
use the WiLD core, and the measurements taken on the bench ST20. The app's build, first stage and
bring-up notes are in [the app README](../../../bco/app/README.md). A measurement here is a historical
observation on one board, not approval of a later image: release evidence belongs in the
[hardware checklist](../hardware-test.md).

## Contents

1. [The link](#the-link)
2. [Control messages](#control-messages)
3. [Protocol level and resent requests](#protocol-level-and-resent-requests)
4. [The card's clock](#the-cards-clock)
5. [The setup access point on the WiLD core](#the-setup-access-point-on-the-wild-core)
6. [CCMP on the core's cipher engine](#ccmp-on-the-cores-cipher-engine)
7. [Medium access and rate adaptation](#medium-access-and-rate-adaptation)
8. [Measurements on the bench ST20](#measurements-on-the-bench-st20)

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
  STATUS; STATUS is polled while connected and a drop reports a disconnect. `eth0` is the wired
  port on channel 2. The usbnet device itself is the transport (`bcolink0`, no addresses); the
  driver brings it up with its children.
- On the BCO, channel 1 goes to the Wi-Fi station (Ethernet ↔ 802.11 data with the device MAC),
  channel 2 to the GEM (`bco/app/src/wired.c`), channel 0 to the management server
  (`bco/app/src/mgmt.c`). The BCO's own DHCP client is a hardware self-test only.
- The BCO has about 16 MB of RAM and a slow debug UART (about 42 ms per character), so the data
  path needs buffer discipline, and tests count frames instead of logging them.

## Control messages

Control messages on channel 0 are `u8 type, u16 seq` and a payload; the SCM retries on silence
and the BCO answers with the same `seq` and `type|0x80`, status first.

- `HELLO`: firmware version, Wi-Fi MAC, state, protocol level and capability bits.
- `SCAN`: payload `{1}` starts a scan, an empty payload polls; status 3 means still scanning,
  status 0 carries the results (`ssid_len, ssid, bssid[6], channel, rssi i8, security`: 0 open,
  1 RSN, 2 WPA1 only). A scan takes about 11 s (13 channels passive, then probing); while
  connected the cached list is returned.
- `SET_PROFILE`: `ssid_len, ssid, security = 1, pmk_len = 32, PMK, 32 random bytes` from the SCM
  kernel for the SNonce. Only WPA2-PSK on 2.4 GHz; anything else is answered "unsupported"
  (status 2). The PMK is never echoed or logged.
- `FORGET` leaves the network without sending deauthentication and resets the WiLD core;
  `STATUS` reports state, network, channel and RSSI.
- `AP_START` (6: channel, beacon interval in TU, beacon body), `AP_STOP` (7) and `AP_STATUS` (8:
  the associated clients) drive the setup access point.

## Protocol level and resent requests

- HELLO ends with a one-byte protocol level and le32 capability bits (`BCOLINK_CAP_AP`,
  `BCOLINK_CAP_REPLAY`). The level is `BCOLINK_PROTO` in `bco/app/src/bcolink.h` (the version
  string is `link-<level>`), and it goes up in the commit that changes a message's layout or
  meaning.
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
  protocol level, message numbers and USB match, and checks the patch's hunk count.

## The card's clock

The BCO keeps time from the host's 1 ms SOF frames (`usblink_ms`), not from a timer interrupt:
after the clock replay that brings up the MUSB, the timer IRQ does not fire. Linux must keep the
USB link active for that clock to run, so the driver does not enable runtime autosuspend. USB
starts before the radio, because that replay also resets the WiLD core.

## The setup access point on the WiLD core

Stock builds its access point in the BCO app's own C++ MLME: AP start is command type 15 to
module 4, which hands the WiLD core a beacon template (core commands `0xe2`, the fixed fields and
IEs before the TIM, and `0xe3`, the IEs after it) and then sets the BSS up with `0xbc`, its own MAC
as BSSID. From then on the core beacons by itself, inserts the TIM and answers probe requests from
the same template (read from `0x6003bfb0` case `0x5b`, `0x60073c90` and `0x60073e84`). That C++
code is not ours and is not re-implemented; only those three core commands are reused. The core
transmits whatever management frame our app puts on its frame ring and delivers received frames
to us.

- Our app (`bco/app/src/wifi.c`) gives the core hostapd's beacon as the `0xe2`/`0xe3` template,
  then `0xbc` with our MAC as BSSID and the beacon interval, then `0xb8` without privacy. It
  answers open-system authentication and (re)association itself, for up to six clients (one core
  peer slot each).
- Frames from associated clients (toDS) go to the SCM as Ethernet frames (DA = addr3, SA =
  addr2); frames from the SCM go out fromDS to an associated client or to every client (group
  addresses at 1 Mb/s, unicast on the client's own rate chain). While the access point runs it
  owns link channel 1; the station is left first. There is no intra-BSS relay and no power-save
  buffering.
- On the SCM, `soundtap_bco` advertises AP mode, a STA + AP interface combination on one channel
  and `WIPHY_FLAG_HAVE_AP_SME` (the device does authentication and association, as `brcmfmac`).
  `start_ap` sends hostapd's beacon (head + a TIM + tail) down as `AP_START`; `stop_ap` sends
  `AP_STOP`. Beacon updates after start (`change_beacon`) are not passed on, and hostapd gets no
  new-station events; `iw dev wlan1 station get` asks the BCO.
- Clients that doze may lose unicast frames (no TIM bits, no buffering). The radio stays on the
  access point's channel, so a scan while it runs returns the scan made before it.

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
3. The PN must be above the key's last accepted one. A frame without a key, with an old PN or
   with a MIC error is dropped in the core. The PN is recorded only once the frame has a host
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

## Medium access and rate adaptation

The core sends host frames on the management queue 9, because the access-class (EDCA) queues do
not transmit on our core. A queue-9 start with only the start bits goes out at once, without
waiting for the medium to be free and without a random backoff, so the core adds the 802.11 DCF
itself (`tx_kick`, `ACK_LATE`, `rate_cls` in `bco/wild/fw/src/xport.c`):

1. Join sets the backoff slot time (`0x6b0` = 9 us - 1). `mac_prep` leaves it at `0 - 1`, and with
   that value a start with backoff slots never goes out.
2. Every attempt of a host frame starts with a DCF backoff: `0x20000 | slots` on `0x674`. The
   slots are random in [0, CW]; CW starts at the core's CWmin and doubles per retry, up to 1023.
3. Without an ACK 1 ms after the TX-done, the attempt is retried (`ACK_LATE`). The 5 ms limit from
   the start of the attempt stays as the upper bound. At the basic rate the wait after the TX-done
   is 3 ms.
4. Data starts at 54 Mb/s. Two frames in a row that needed a retry step the rate down (54 -> 36 ->
   24 Mb/s), and ten frames that went through at once step it up again. The last tries of a frame
   stay at the basic rate.

Limits: 24 Mb/s is the floor of the adaptation, because below it a frame outlasts `ACK_LATE`; a
weaker link falls back to the basic rate on the last tries. 9 us assumes a BSS with short slots
(every OFDM/ERP network); a BSS with 11b-only stations would need 20 us, which is not handled.

Porting stock's EDCA scheduler would mean reading stock TX code, which stays off-limits; queue 9
with DCF backoff gives the same medium access for best-effort traffic.

## Measurements on the bench ST20

Historical observations on the one bench ST20 and its access point. Where no build or complete
capture is identified, that provenance is missing.

### LAN and API response

2026-10-06: `wpa_supplicant` reached COMPLETED in 12 s, `wlan0` took a DHCP lease, SoundTap
answered on :80 from the LAN and 1500-byte frames passed. Not every API route was exercised.

### Our first stage through the real BFL

2026-10-16: our own first stage started on the ST20 through the real BFL, flashed through the BSL
XMODEM menu, with stock restorable the same way. With our own WiLD core embedded in the app, the
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
