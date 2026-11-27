# 0129. A first install keeps the speaker's name, presets, Wi-Fi and clock

Status: accepted — 2026-11-11, revised 2026-11-27

## Context

A first install from Bose's software leaves stock's files in `/mnt/nv/BoseApp-Persistence/1/`:
the speaker's name in `SystemConfigurationDB.xml`, its presets in `Presets.xml`, its networks
in `NetworkProfiles.xml` and, on the ST20, its standby clock in `ClockDisplay.xml` with the
zone also as the `/mnt/nv/localtime` link. Without them the speaker comes up unnamed, with empty presets, on its
setup network, and the owner sets it up again.

Stock's presets name a TuneIn station by its guide id, which SoundTap plays as it is, or a
Radio Browser station by its uuid (`source="RADIO_BROWSER"`, location
`/stations/byuuid/<uuid>`). The stream address of a Radio Browser station is not in stock's
file: only Radio Browser ([radio-browser.info](https://www.radio-browser.info/), an open,
community-run station directory without accounts or keys) knows it. The first start runs
before the network is up (on the owner's ST20 the takeover ran half a minute before Wi-Fi
joined).

Stock keeps each Wi-Fi passphrase AES-256-CBC encrypted (base64, PKCS#7) and no plain copy. The
key and IV are not on `/mnt/nv`: they are two ASCII digit strings, 32 and 16 long, inside stock's
`/opt/Bose/NetManager`, the same binary on the ST10, ST20, ST300 and WLA. Neither the repository
nor the image may hold Bose bytes ([ADR 0049](0049-no-stock-bytes.md)).

## Decision

On its first boot after an install from stock, soundtapd takes the name stock knew the speaker
by, stock's TuneIn presets and its Radio Browser presets (stock's other sources need its
accounts or cloud, so those slots stay empty), once. On a speaker with a panel (the ST20) whose clock has no settings yet it also
takes stock's clock: off when stock had it off or at brightness 0, 12 or 24 hours, and the zone
(`timezoneInfo`, else the `localtime` link) when this build knows it; stock's other brightness
levels, which it never used to dim, change nothing
([standby clock](../hardware/st20/standby-clock.md#where-stock-keeps-the-clocks-settings)).
It records the takeover (`.stock-imported` in its home) only after the name, the presets and the
clock are stored, so a first start that stops half-way takes them over again on the next start.
It logs what each of stock's files gave: no file, a file it could not read or parse, how many
presets it took over, how many wait for a lookup, and each preset it left out with the reason.

A Radio Browser preset becomes a preset with its own stream address
([ADR 0041](0041-radio-one-ffmpeg-one-url-rule.md)): stock's name and logo, and the
station's `url_resolved` (else its `url`) from Radio Browser. Since that needs the network, the
takeover writes such presets down in `/mnt/nv/soundtap/stock-presets-pending.json` (slot, uuid,
name, logo, the time first seen once NTP has set the clock;
[nv-state](../contributors/nv-state.md)), and only when it takes stock's presets at all: a
speaker with presets of its own takes none. A background job in soundtapd then asks
`GET /json/stations/byuuid/<uuid>` of Radio Browser's servers `de1`, `de2` and `fi1`
`.api.radio-browser.info`, and the round-robin `all.api.radio-browser.info`, in turn, over
public addresses only, with the User-Agent `SoundTap/<version>`. The uuid is all a request
carries: no name, serial, address or anything else about the speaker or its owner. The job
runs at every start while the file is there, and after a round that left presets waiting
again after 30 s, doubling to every 10 minutes; there is no signal for "the network is up",
and a round without network costs a failed name lookup. A preset is stored only while its
slot is empty (checked and written under one lock, so a preset the owner stored meanwhile is
never replaced) and only with a stream address an owner could enter themselves (http or
https, at most 512 bytes). It is dropped, with a log line saying why, when its slot holds a
preset, Radio Browser knows no such station, or the address is not usable; after 7 days from
the time first seen without an answer it is given up. That time is taken only after NTP has
set the clock this boot (`/run/soundtap/clock-synced`): on the first boot after an install from
stock init sets the clock to the build's time, and a preset dated by it would be given up at the
first failed lookup once NTP moved the clock on by more than a week. A time no later than an
hour after the running build's time is taken for such a date and taken again. The file goes with its last entry, and
with a factory reset, which ends the lookups first so a round still in flight cannot write it
back. `.stock-imported` still records only that the takeover ran: the lookups
carry on after it.

For the Wi-Fi, the installer runs under stock, so it reads the key and IV from the NetManager
that is running: of all its runs of 12 or more ASCII digits there must be exactly two, one of 32
and one of 16; any other pattern gives nothing. It leaves them in
`/mnt/nv/soundtap/stock-wifi-key` (0600) only when SoundTap has no `wifi.conf` yet and stock saved
a profile. On the first boot soundtapd decrypts every profile stock saved with Go's own AES,
writes them all to `wifi.conf.known` and the first to `wifi.conf`, deletes the key file and
restarts Wi-Fi through its Wi-Fi manager, so wpa_supplicant joins whichever network is in range.
Stock's `lastConnected` cannot pick the one network the speaker was on: stock stamps it from a
clock without a battery, so a join at boot, before NTP, is dated July 2015 and loses against an
older join with a real date. The order is therefore only the order of trying: profiles stamped
2016 or later newest first, then the rest in stock's order; a profile that cannot be decrypted
is left out and the others are still taken, up to the 5 networks `wifi.conf.known` keeps.
WPA, WEP and open networks are taken
([ADR 0136](0136-every-network-stock-joined-and-wpa3.md)): a WEP profile's `wepKey`, as stock's
own scripts write it, or decrypted when the profile is encrypted.

On the ST20, where Bose's software keeps the network in the BCO, soundtapd reads it from the
BCO while Bose's BCO app is still there, before it replaces that app: afterwards it can no longer
be read. Stock keeps its settings as an append-only log in a NOR sector
([the stock app's saved network](../hardware/st20/bco-recovery.md#6-the-stock-apps-saved-network)).
soundtapd reads the active sector through the sdsShell's `rd`, takes the last valid record of
each setting, and takes the active profile's network when it is WPA with a passphrase, open, or
WEP with the key its key index names ([ADR 0136](0136-every-network-stock-joined-and-wpa3.md));
stock's own setup profile is not taken. The record numbers are BCO app 9423's,
in one table. A read that gets no reply or a garbled dump is tried again, up to 5 times, 30 s
apart, within 5 minutes; a read that was whole but holds nothing to take is not. The BCO install
waits for that answer, and goes ahead once the tries run out.

Alternatives that lost: shipping the key and IV in SoundTap's source (simpler, but it puts Bose-derived
bytes in git); the owner typing the password again after the install.

## Consequences

- A first boot that brings nothing over says why in its log, file by file and preset by preset.
- SoundTap contacts one more outside service, Radio Browser, and only for an install from
  stock with Radio Browser presets: once per such preset, with its uuid alone, for at most a
  week. A speaker installed otherwise, or whose presets were all resolved, never contacts it.
- A Radio Browser preset appears some seconds to minutes after the first start, once the
  network answers, not at once; a station Radio Browser has dropped stays empty.
- The stream address is the one Radio Browser gives today; a station that later moves its
  stream needs its preset set again, as any preset with its own stream address does.
- No Bose byte enters the repository or the image, and no Bose code runs under SoundTap: the
  installer only reads stock's binary as a file, once, while stock is still the running system.
- On the ST20 the first boot from stock confirms up to 5 minutes later, while the read is tried
  again; another BCO app version with other record numbers reads as "nothing to take".
- A NetManager with any other digit pattern, a passphrase that does not decrypt to a valid WPA
  passphrase or a WEP key that is not one, or a missing file leaves the speaker on its setup
  network.
- An update between SoundTap versions finds `wifi.conf` and does none of this.
- An ST20 installed from stock shows its clock, in stock's zone, without the settings first being
  opened in a browser ([ADR 0111](0111-st20-standby-clock.md)).
- The take-over is a row of the release checklist in
  [`docs/hardware/hardware-test.md`](../hardware/hardware-test.md#release-checklist-per-model).

## Code

- `os/installer/module/installer.c`: `stock_wifi_key` and the `--stock-wifi-key` self-test.
- `app/cmd/soundtapd/stock_wifi.go`: `adoptStockWifi`, `readStockNetwork`,
  `decryptStockPassphrase`, and `adoptStockBCOWifi` for the ST20 with its retry bounds.
- `app/internal/bco/stockprofile.go`: `ReadStockProfile`, the settings log walk and
  `stockLayout9423`; `ErrStockProfileNotImportable` marks the definitive answer.
- `app/cmd/soundtapd/identity.go`: `stockDeviceName`, `stockPresets`, `stockClock`,
  `takeStockClock` and `takeStockOnce`.
- `app/cmd/soundtapd/stock_radiobrowser.go`: `recordPendingStockPresets` and `stockLookups`,
  the lookups of stock's Radio Browser presets; `app/internal/radiobrowser`: the client.
- `app/internal/speakerstate/backend_presets.go`: `StorePresetIfEmpty`.
