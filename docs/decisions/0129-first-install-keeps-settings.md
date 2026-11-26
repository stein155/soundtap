# 0129. A first install keeps the speaker's name, presets, Wi-Fi and clock

Status: accepted — 2026-11-11, revised 2026-11-24

## Context

A first install from Bose's software leaves stock's files in `/mnt/nv/BoseApp-Persistence/1/`:
the speaker's name in `SystemConfigurationDB.xml`, its presets in `Presets.xml`, its networks
in `NetworkProfiles.xml` and, on the ST20, its standby clock in `ClockDisplay.xml` with the
zone also as the `/mnt/nv/localtime` link. Without them the speaker comes up unnamed, with empty presets, on its
setup network, and the owner sets it up again.

Stock keeps each Wi-Fi passphrase AES-256-CBC encrypted (base64, PKCS#7) and no plain copy. The
key and IV are not on `/mnt/nv`: they are two ASCII digit strings, 32 and 16 long, inside stock's
`/opt/Bose/NetManager`, the same binary on the ST10, ST20, ST300 and WLA. Neither the repository
nor the image may hold Bose bytes ([ADR 0049](0049-no-stock-bytes.md)).

## Decision

On its first boot after an install from stock, soundtapd takes the name stock knew the speaker
by and stock's TuneIn presets (stock's other sources need its accounts or cloud, so those slots
stay empty), once. On a speaker with a panel (the ST20) whose clock has no settings yet it also
takes stock's clock: off when stock had it off or at brightness 0, 12 or 24 hours, and the zone
(`timezoneInfo`, else the `localtime` link) when this build knows it; stock's other brightness
levels, which it never used to dim, change nothing
([standby clock](../hardware/st20/standby-clock.md#where-stock-keeps-the-clocks-settings)).

For the Wi-Fi, the installer runs under stock, so it reads the key and IV from the NetManager
that is running: of all its runs of 12 or more ASCII digits there must be exactly two, one of 32
and one of 16; any other pattern gives nothing. It leaves them in
`/mnt/nv/soundtap/stock-wifi-key` (0600) only when SoundTap has no `wifi.conf` yet and stock saved
a profile. On the first boot soundtapd decrypts the profile stock joined last (`lastConnected`)
with Go's own AES, writes `wifi.conf`, deletes the key file and restarts Wi-Fi through its
Wi-Fi manager. WPA and open networks are taken; WEP is not. On the ST20, where Bose's software keeps the network in the BCO,
soundtapd reads the profile from the BCO while Bose's BCO app is still there, just before it
replaces that app.

Alternatives that lost: shipping the key and IV in SoundTap's source (simpler, but it puts Bose-derived
bytes in git); the owner typing the password again after the install.

## Consequences

- No Bose byte enters the repository or the image, and no Bose code runs under SoundTap: the
  installer only reads stock's binary as a file, once, while stock is still the running system.
- A NetManager with any other digit pattern, a passphrase that does not decrypt to a valid WPA
  passphrase, or a missing file leaves the speaker on its setup network.
- An update between SoundTap versions finds `wifi.conf` and does none of this.
- An ST20 installed from stock shows its clock, in stock's zone, without the settings first being
  opened in a browser ([ADR 0111](0111-st20-standby-clock.md)).
- The take-over is a row of the release checklist in
  [`docs/hardware/hardware-test.md`](../hardware/hardware-test.md#release-checklist-per-model).

## Code

- `os/installer/module/installer.c`: `stock_wifi_key` and the `--stock-wifi-key` self-test.
- `app/cmd/soundtapd/stock_wifi.go`: `adoptStockWifi`, `readStockNetwork`,
  `decryptStockPassphrase`, and `adoptStockBCOWifi` for the ST20 (`bco.ReadStockProfile`).
- `app/cmd/soundtapd/identity.go`: `stockDeviceName`, `stockPresets`, `stockClock`,
  `takeStockClock` and `takeStockOnce`.
