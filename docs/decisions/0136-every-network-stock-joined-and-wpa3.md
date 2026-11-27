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

Stock joined no WPA3 network and protected no management frame. Routers now ship WPA2/WPA3
transition networks, WPA3-only ones and networks that require management frame protection
(IEEE 802.11w, PMF); a speaker that refuses them has to be moved to a guest network.

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

### WPA3 and PMF: SAE on the SCM, the rest on the BCO

WPA3-Personal's SAE is a password exchange with elliptic-curve arithmetic. wpa_supplicant already
has it, on every model; the BCO has neither the code nor the CPU for it. So on the ST20 SAE runs
in wpa_supplicant over cfg80211's external authentication, as `brcmfmac` does: an SAE profile
(SET_PROFILE security 3, no key) makes the BCO ask the driver for SAE with the access point's BSSID
and SSID (`EXT_AUTH`), pass up the access point's authentication frames and send
wpa_supplicant's (`AUTH_FRAME`); the driver hands the PMK wpa_supplicant derived (`SET_PMK`, from
its PMKSA entry) and SAE's result (`EXT_AUTH`). The BCO then associates with AKM 8 and runs the
4-way with that PMK, the keys never leaving it. All of it is behind `BCOLINK_CAP_WPA3`.

The BCO runs the rest of WPA3 and PMF itself: the 4-way under the SHA-256 AKMs (SAE and
PSK-SHA256: the 802.11 KDF, AES-CMAC MICs, AES key wrap, key descriptor versions 0 and 3), MFPC
in its RSN IE whenever the access point offers PMF and MFPR when it requires it, the IGTK from
Message 3 and group rekeys, robust unicast management frames (deauthentication, disassociation,
action) protected and checked in software CCMP under the PTK's TK with the nonce's Management
flag and packet numbers of their own, group-addressed ones checked with BIP-CMAC-128, and the SA
Query: an unprotected deauthentication or disassociation is answered with a protected SA Query and
ends the link only when no protected answer comes within a second; the access point's SA Query
is answered. A PSK profile prefers PSK-SHA256 where it is offered, as wpa_supplicant does.

On every model soundtapd writes `key_mgmt=WPA-PSK WPA-PSK-SHA256 SAE` and SAE's password (as hex)
beside the PSK for a saved password, so wpa_supplicant picks WPA2, WPA3 or the transition by
itself. Management frame protection is wpa_supplicant's global `pmf=1` rather than each network's
`ieee80211w=1`: only the global default is dropped for a driver without BIP, and mainline's
`wlcore` (the WL1835) lists no BIP cipher and no MFP capability, where a network's `ieee80211w=1`
would ask for PMF the radio cannot do. With `sae_check_mfp=1` wpa_supplicant uses SAE only where
PMF can be used, so on the WL1835 a WPA2/WPA3 router is joined over PSK as before, and a WPA3-only
or PMF-required one is not, until the radio's driver gains it. SAE needs the password itself: a
network saved as a raw 64-digit key joins over PSK only. wpa_supplicant is built with Buildroot's
WPA3 option; hostapd is not, since the setup network stays open
([the hardware checklist](../hardware/hardware-test.md) has each model's runs).

These features are optional to the join. A wpa_supplicant that does not take one (a build
without `CONFIG_SAE` answers FAIL to `SAE` in `key_mgmt` and to `sae_password`, one without
`CONFIG_WEP` to `wep_key0`) costs only that feature: soundtapd logs once which setting was
refused and offers the network the next suites, `WPA-PSK WPA-PSK-SHA256` then `WPA-PSK`; a
network the build cannot join at all, such as WEP without `CONFIG_WEP`, is left out and the other
saved networks still join. When wpa_supplicant does not start with `pmf=1` and `sae_check_mfp=1`,
it starts without them. Only a control socket that stops answering fails the join. On an ST20 a
stale wpa_supplicant without SAE had refused every join this way and left the speaker on its
setup network.

Alternatives that lost:

- **SAE on the BCO.** A big-number and elliptic-curve library on a 200 MHz ARM926 in an app that
  must stay small, for a few round trips per join that wpa_supplicant already does right.
- **Only the transition network's PSK side.** Leaves WPA3-only and PMF-required networks out.
- **PMF frames on the core's engine.** The engine takes its nonce and AAD from the header and has
  no Management flag; robust management frames are a few per hour, cheap in software.

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

- WPA3 on the ST20 needs the driver and the app of one build or newer; with an older app the
  driver offers no SAE and wpa_supplicant joins the PSK side of a transition network.
- On the WL1835 models (WLA, ST10, ST300) WPA3-only and PMF-required networks stay out of reach
  with mainline's `wlcore`; transition networks join over PSK.
- soundtapd hands wpa_supplicant SAE's password over the control socket, the one place a
  passphrase now goes beside the PSK (ADR 0063); never into a file or a log.
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
- WPA3 and PMF: `bco/app/src/wpa.c` (`wpa_kdf_sha256`, `wpa_cmac`, `extract_igtk`),
  `bco/app/src/wsec.c` (`wsec_ccmp_protect`, `wsec_bip_check`), `bco/app/src/wifi.c` (`sae_start`,
  `wifi_sae_tx`, `send_protected`, `saq_start`, `mgmt_open`, `bip_open`), `bco/app/src/mgmt.c`
  (`EXT_AUTH`, `AUTH_FRAME`, `SET_PMK`, `mgmt_sae_due`), the driver's `bco_mgmt_tx`,
  `bco_external_auth` and `bco_set_pmksa`, `app/internal/wifi/station.go` (`configure`) and
  `os/package/soundtap-runtime/Config.in` (wpa_supplicant's WPA3).
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
