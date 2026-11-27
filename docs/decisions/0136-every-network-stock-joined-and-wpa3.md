# 0136. Every network security stock joined, and WPA3, on every model

Status: proposed — 2026-11-27

## Context

Stock joins open networks, WEP (64- and 128-bit keys, open-system and shared-key
authentication), WPA-PSK with TKIP, WPA2-PSK with AES or TKIP, and WPA/WPA2 mixed networks
whose broadcasts stay TKIP ([the ST20's saved security values](../hardware/st20/bco-recovery.md#6-the-stock-apps-saved-network)).
SoundTap's ST20 network card ([ADR 0031](0031-bco-network-card.md)) joined WPA2-PSK with
pairwise CCMP only, and dropped the broadcasts of a mixed network. On every model
`wpa_supplicant` 2.12 is built without WEP.

The ST20's WiLD core runs CCMP on its cipher engine. Stock's core drives the same engine for WEP
and TKIP too; the modes are read out of stock
([the engine modes](../hardware/st20/bco-network-card.md#wep-and-tkip-stocks-engine-modes-and-where-soundtap-runs-them)),
but SoundTap has run none of them.

## Decision

### The link asks for each security in SET_PROFILE

SET_PROFILE's security byte names what the speaker joins, behind the capability bit
`BCOLINK_CAP_SECURITY` ([the control messages](../hardware/st20/bco-network-card.md#control-messages)):
0 an open network, without a key; 1 a PSK network, with the PMK. The driver sends a connect
without a key as an open network only to an app that announces the bit. SCAN reports a WEP network
as such (3: the Privacy bit without an RSN or WPA IE), and with the bit set in its request each
entry carries its security detail (the RSN group, pairwise and AKM sets and capabilities, the WPA
IE's group and pairwise set), as STATUS does for the joined network, so the driver's scan cache
shows wpa_supplicant each network as it is: WPA only, TKIP, the capabilities. An open network
joins without a handshake and its frames go in the clear; the BCO joins only an access point
whose security matches the profile.

### A PSK profile joins WPA and WPA2 with CCMP or TKIP, as wpa_supplicant picks

With a PSK profile the BCO joins an access point whose RSN IE or, failing that, WPA IE (vendor
00-50-F2 type 1) offers PSK with a cipher pair it can use: pairwise CCMP with a CCMP or TKIP
group, or pairwise TKIP with a TKIP group. It prefers RSN over WPA and pairwise CCMP over TKIP, as
wpa_supplicant does, and sends that IE in its association request and Message 2. The handshake
follows what was negotiated: EAPOL-Key descriptor 2 for RSN, 254 for WPA; key descriptor version 2
(HMAC-SHA1 MIC, AES key wrap) under pairwise CCMP and version 1 (HMAC-MD5 MIC, RC4 key data with
the first 256 bytes of the stream dropped) under TKIP, with a 64-byte PTK. WPA's Message 3 has no
GTK; it comes in a group handshake, its key id in the key info.

### WEP joins as stock joined it, on every model

A WEP profile carries one static key (40 or 104 bits) and its index. The ST20's BCO joins an
access point with the Privacy bit and no RSN or WPA IE, with open system or shared key
authentication as the profile says, or open system first and shared key when the access point
refuses it (status 13); the driver passes cfg80211's connect key, its index and its
authentication type down, and keeps the static keys wpa_supplicant sets (`add_key`,
`set_default_key`), which cfg80211 requires once a connect carries keys. soundtapd takes a
`wep` security with a 5- or 13-character or 10- or 26-hex-digit key, stores it with
`SECURITY=wep` in `wifi.conf` (an older file has no such line and reads as before, ADR 0120) and
hands wpa_supplicant `key_mgmt=NONE`, `wep_key0`, `wep_tx_keyidx=0` and `auth_alg=OPEN SHARED`;
wpa_supplicant is built with WEP for that on every model. The setup page offers WEP beside open
and WPA, and a surveyed network without a WPA or RSN IE asks for a WEP key. The first install
takes a WEP network stock saved, on the ST20 from its BCO's settings, elsewhere from stock's
network profiles.

### WEP and TKIP run in software in the BCO app

The BCO app enciphers and deciphers WEP and TKIP frames itself: RC4, the CRC-32 ICV, TKIP's key
mixing, the TSC replay check and the Michael MIC with its countermeasures (a report to the access
point, and on a second failure within a minute a deauthentication and a minute without TKIP).
CCMP data stays on the core's cipher engine. The keys stay on the BCO, as ADR 0031 requires: the
app holds the software keys, and the core learns only that a key slot is deciphered by the app, so
it passes such a frame up undeciphered and sends a frame the app marked as enciphered as it is
([how it runs](../hardware/st20/bco-network-card.md#wep-and-tkip-stocks-engine-modes-and-where-soundtap-runs-them)).
A WPA/WPA2 mixed network's TKIP group key is one of these, so its broadcasts arrive.

Alternatives that lost:

- **Stock's engine modes for WEP and TKIP, blind.** Never run by SoundTap; one wrong field (the
  TKIP key half, Michael's addresses, the fragment state) and the network fails outright. Kept
  as a later speed-up once each mode is measured against the software path.
- **WEP and TKIP in the core in software.** The core's CPU is about seven times slower than the
  app's, and the app already holds the keys.

## Consequences

- A WEP or TKIP network costs the app's CPU per frame; RC4 and Michael are far cheaper than the
  software AES that already moved data at nearly the engine's speed.
- The engine modes stay documented facts until they are measured
  ([the hardware checklist](../hardware/hardware-test.md)).

## Code

- `bco/app/src/mgmt.c` (SET_PROFILE, STATUS), `bco/app/src/wifi.c` (`match`, `send_assoc`) and
  `bco/app/src/wstation.h` (`wstation_assoc`, `wstation_data`): the profile's security.
- `os/board/rootfs/linux-patches/0007-net-usb-add-soundtap-bco-link-driver.patch`: `bco_connect`
  and `bco_status_sec` in the driver.
- `app/internal/bco/stockprofile.go`: `stockProfileFrom` takes stock's open profile.
- `bco/app/src/wifi.c` (`send_challenge`, the `J_KEYS` step), `app/internal/wifi/conf.go`
  (`SecurityWEP`, `IsWEPKey`), `app/internal/wifi/station.go` (`configure`),
  `app/internal/speakerstate/backend_wifi.go` (`CheckWifi`), `app/cmd/soundtapd/stock_wifi.go`,
  `app/frontend/src/components/organisms/settings/NetworkSettings/useWifiJoin.ts` and
  `os/external.mk` (wpa_supplicant's `CONFIG_WEP`): WEP.
- `bco/app/src/wscan.h` (`rsn_parse`, `wpa_parse`), `bco/app/src/wifi.c` (`choose`, `match`) and
  `bco/app/src/wpa.c` (`wpa_sm_config`, `wpa_own_ie`, descriptor 254 and version 1): which security a
  PSK profile joins, and its handshake.
- `bco/app/src/wsec.c`: WEP, TKIP and their primitives; `bco/app/src/wifi.c` (`set_key`,
  `install_keys`, `sw_open`, `sw_data`, `mic_failure`) and `bco/app/src/wpa.c` (`wpa_mic_failure`).
- `bco/wild/fw/src/sta.c` (software key slots) and `bco/wild/fw/src/xport.c` (frames the app
  deciphers or enciphered).
