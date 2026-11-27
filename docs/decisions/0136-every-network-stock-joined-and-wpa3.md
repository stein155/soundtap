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

### WEP and TKIP run in software in the BCO app

The BCO app enciphers and deciphers WEP and TKIP frames itself: RC4, the CRC-32 ICV, TKIP's key
mixing, the TSC replay check and the Michael MIC with its countermeasures. CCMP data stays on
the core's cipher engine. The keys stay on the BCO, as ADR 0031 requires: the app holds the
software keys, and the core learns only that a key slot is deciphered by the app, so it passes
such a frame up undeciphered and sends a frame the app enciphered as it is.

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

- `bco/wild/fw/src/sta.c` (software key slots) and `bco/wild/fw/src/xport.c` (frames the app
  deciphers or enciphered).
