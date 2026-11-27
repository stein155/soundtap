# State on `/mnt/nv`

`/mnt/nv` is the speaker's persistent volume (`mtd10`, `ubi1:persistent_volume`). Updates and
the slot switch leave it alone, so every build reads what the one before it wrote. From 0.1.0
on, a format change to any file below that holds user state ships a migration and a test that
loads the previous release's file ([ADR 0120](../decisions/0120-on-box-state-migrates.md)).
A PR that adds a file here or changes a format updates this page.

**Kind** says what a lost or unread file costs. *User* state must survive every update: it has
a fixture in `app/testdata/nv-<release>/` that the tests load. *Machine* state, logs and caches
may be dropped by a new build, which must still start with the old file in place. *Other*
files belong to another program, which reads its own format.

**Version** says whether the file carries a format version. JSON loaders generally ignore
unknown fields and give absent fields their zero value; this does not guarantee acceptance
of a different file shape or malformed data. The table records important failure behaviour.
The four JSON stores below (settings, presets, favorites, recent) open by one rule
(`app/internal/atomicjson`): only a missing file starts from defaults. Any other read error
(EIO, EACCES, a directory in its place) leaves the store on defaults in memory and read-only,
logged once at error level, so no save replaces a file that may still be intact; malformed
JSON is moved aside before anything is written. The support bundle (`/api/debug`, "on-box
stores") has a line per store, and a factory reset removes the `.corrupt` copies with the rest.
Before 0.1.0, fixture files describe the current development format; the 0.1.0 tag freezes
them ([ADR 0120](../decisions/0120-on-box-state-migrates.md)).

**Ownership.** soundtapd runs as the user `soundtapd`
([ADR 0066](../decisions/0066-production-hardening.md)). `/mnt/nv` is root's, group `soundtapd`,
mode 1775: soundtapd creates and replaces its own files there (`wifi.conf`, `.good`, `.known`)
and cannot remove anyone else's. `/mnt/nv/soundtap` and everything in it is soundtapd's but
the boot logs, `spotify/` is librespot's user's (`spotify:audio`), `bluetooth/` and `bluealsa/`
stay root's, and so do the files only the init script writes (`soundtap-clock`, the boot and
network-failure traces, `current-boot.log` and `previous-boot*.log`). Every earlier build left
all of it root's: at every boot `migrate_nv_owner` in the init script hands soundtapd and
librespot what is not theirs yet, so the first boot after the update chowns the old files and
later boots change nothing. Ownership is no format: no file changes.

## `/mnt/nv/soundtap`: soundtapd's home

| File | Kind | Owner (writes / reads) | Format | Version |
| --- | --- | --- | --- | --- |
| `presets.json.settings` | User | `app/internal/settings` | JSON object: name, source names, language, MQTT, audio, display, alarm, multiroom, integrations, channel, AUX name, and the crash report opt-in with the last 32 crashes reported or dismissed (`crashReports`, absent while it was never on; [ADR 0126](../decisions/0126-opt-in-crash-reports-without-a-server.md)). A corrupt file goes to `.corrupt` and `.corrupt.1`; a newer schema stays in place and is read-only until a reset; an unreadable file is read-only | `schemaVersion`, with a migrations list |
| `presets.json` | User | `app/internal/store` | JSON array of presets (slot 1..6, station or source/type/location, name, logo). Malformed JSON goes to `.corrupt` and `.corrupt.1`; an unreadable file stays in place and the store starts empty and read-only | none |
| `favorites.json` | User | `app/internal/favorites` | JSON array: source, name, location, art. The list offers one favorite, the first; adding one through the API makes the list that one alone, and removing it empties the list. A longer list (saved before, at most 94 through the API or a restored backup, or hand-edited) loads whole and unchanged; the next add, or removal of the first, leaves it at one or none. Malformed JSON goes to `.corrupt` and `.corrupt.1`; an unreadable file is read-only | none |
| `recent.json` | User | `app/internal/recent` | JSON array, newest first: source, name, location, art, `stoppedAt` (RFC 3339); at most 50. Longer lists are truncated in memory. Malformed JSON goes to `.corrupt` and `.corrupt.1`; an unreadable file is read-only | none |
| `tunein-streams.json` | Machine | `app/internal/player/streams.go` | JSON object by station location: `urls` (the stream list TuneIn last gave) and `at` (RFC 3339). Written only when a list changes; stations not resolved for 60 days are dropped. Used when TuneIn does not answer. Unreadable files load as empty | none: new in this version, older versions ignore it |
| `multiroom-server` | User | `app/internal/multiroom` owns the follow state through `Group` (ADR 0122); `snapclientArgs` in `app/cmd/soundtapd` reads line 1 | Two lines: the leader's address, then its device id. Absent: this speaker serves itself | none |
| `bar-source`, `bar-audio-mode`, `bar-cec-mode`, `bar-hdmi-input` | User (ST300) | soundtapd (`internal/lpm/state.go`) | One decimal number: the processor's source, audio mode, CEC mode, HDMI input button | none |
| `firmware-pending.json` | Machine, read across the update | `app/internal/web/firmware` writes it before the reboot; `app/internal/install` reads it after | JSON: target slot, package size, kernel hash, models | none |
| `.stock-imported` | User | soundtapd (`identity.go`) writes it on the first start after an install from stock; a factory reset keeps it | Presence, empty. Lost: the next start takes stock's name and presets over again | none |
| `stock-presets-pending.json` | Machine, read across starts | soundtapd (`stock_radiobrowser.go`) writes it on the first start after an install from stock and works it off in the background; a factory reset removes it | JSON object: `version` (1) and `presets`, each `slot`, `uuid` (a Radio Browser station), `name`, `logo` and `firstSeen` (RFC 3339, absent while the clock was not set): stock's Radio Browser presets waiting for their stream address ([ADR 0129](../decisions/0129-first-install-keeps-settings.md)). Entries go as they are stored, dropped or given up after 7 days, and the file with the last one. A file that does not parse, or has no or an older version, is logged and removed; a newer version is left alone. Lost: those presets are not taken over | `version` |
| `multiroom-enabled` | User | `app/internal/web`; the init script tests for it | Presence; content `enabled` is ignored | none |
| `hostname` | Machine | soundtapd writes it from the name at every start; the init script reads it | One line, a DNS label | none |
| `st20-eth0` | Machine (ST20) | Written by hand from stock's address; the init script and `app/internal/bco` read it | One line: `ADDR/PREFIX GATEWAY [DNS]` | none |
| `codec/` | Machine, must not be lost | The init script copies the speaker's own codec script out of the slot it was installed from ([ADR 0012](../decisions/0012-single-dac-writer.md)) | The speaker's own codec script, kept byte for byte | none |
| `soundtapd.override` (`.gz`, `.base`, `.trial`) | Machine | `app/internal/web/firmware/soundtapd.go`; the init script | An uploaded soundtapd, gzipped, and the base firmware soundtapd hash it was made for; dropped when that hash no longer matches, a previous trial never passed, or loading fails; three consecutive runtime failures, or a web page or update route that still does not answer after five minutes, also fall back to the built-in app | none |
| `bco-image` | Machine (ST20) | `app/internal/bco` (`autoinstall.go`) | One line: the BCO image's SHA-256, then ` ok` once installed. A bare hash suppresses retries after an attempted install; ` ok` also checks that SoundTap's app still runs. Lost: the bundled image is attempted again, which may write BCO flash | none |
| `snapserver/` | Other | snapserver | snapserver's clients and groups; snapserver writes /run/soundtap/snapserver, soundtapd copies it here when more than lastSeen changes | — |
| `current-boot.log`, `previous-boot.log`, `previous-boot.1.log`, `previous-boot.2.log` | Log | The init script; the health page reads them | Text, 32 KiB each | — |
| `panic.log`, `last-boot/` | Log | soundtapd | Text: the last crash, and how the last boot ended | — |

Names nothing writes any more: `api-token`, `.sessions`, `plugins` (a factory reset removes
them), `allow-unsigned` (it switched on unsigned uploads on an alpha image; every start removes
it, `removeRetiredFiles` in `app/cmd/soundtapd`, tested against the previous release's set in
`retired_test.go`; [ADR 0048](../decisions/0048-signed-updates.md)) and `.version`, `.attempts`,
`.gaveup` (the diagnostics page shows them if present).

Not on `/mnt/nv`: builds before 0.1.0 could keep an owner's signing key in the U-Boot
environment variable `soundtap_owner_key`. Nothing reads it any more; an install's slot
selection removes it (`flash.Installer.SelectBootSlot`).

The [BCO install marker](../../app/internal/bco/autoinstall.go) is not a disposable cache: it
also decides whether a flash is attempted, so deleting it can cause another hardware write.

## `/mnt/nv`

| File | Kind | Owner (writes / reads) | Format | Version |
| --- | --- | --- | --- | --- |
| `wifi.conf` | User | soundtapd: the Wi-Fi manager (`app/internal/wifi`) reads it and rewrites it when another saved network joined; written on setup, on adopting stock's network and on a factory reset | `SSID_B64=`, `PSK_B64=` (empty: open network), optional `PSK_HEX=` lines; optional `SECURITY=wep`, `PSK_B64` then holding the WEP key as typed (ADR 0136; a file without it reads as before) | none |
| `wifi.conf.good` | User | soundtapd's Wi-Fi manager | A copy of the last `wifi.conf` that joined | none |
| `wifi.conf.known` | User | soundtapd's Wi-Fi manager; the API lists and forgets entries | Up to 5 lines, newest first: `SSID_B64 PSK_B64\|- PSK_HEX\|-`, then `wep` for a WEP network (ADR 0136) | none |
| `ir.map` | User, written by hand | soundtapd (`internal/hw/ir.go`) | `code = ACTION` lines, code decimal or `0x` hex, `#` comments | none |
| `buttons.map` | User, written by hand | soundtapd (`internal/hw/keymap.go`) | `index = ACTION` lines, `#` comments | none |
| `ntp-servers` | User, written by hand | `clock.sh` | NTP host names or addresses separated by white space, `#` comments. Asked after the DHCP lease's servers, instead of the default; a word that is not a name or an address is ignored. Absent or without a name: the default | none |
| `soundtap-clock` | Machine | The init script | Seconds since 1970, one line. Lost: the clock waits for NTP | none |
| `charger_active_level` | Other (Portable) | Stock's file; soundtapd reads it (`cmd/soundtapd/battery.go`) and never writes or removes it | One character, `1` or `0`: the charge enable's active level. Anything but `0`, or no file, means active high ([portable](../hardware/portable/README.md)) | none |
| `model` | Machine | Read by the init script, written by nothing in SoundTap | A model name; one that conflicts with the hardware is logged and ignored | none |
| `soundtap-boot.log`, `soundtap-netfail.log` | Log | The init script | Text | — |
| `bluetooth/`, `bluealsa/` | Other | BlueZ (pairings), bluealsa (volumes per device) | Theirs | — |
| `spotify/` | Other | librespot (`system/credentials.json`, `cache/`) | Theirs | — |
| `dropbear/` | Other, alpha only | `ssh.sh`, Dropbear | The host key | — |
| `BoseApp-Persistence/1/` | Other | Bose's software; soundtapd reads the name, presets, Wi-Fi and, on the ST20, the clock (`ClockDisplay.xml`, with `/mnt/nv/localtime`) once after an install from stock | Stock's XML; never written | — |

The support bundle ([`redact.go`](../../app/internal/web/redact.go)) reads `wifi.conf`,
`wifi.conf.good`, `wifi.conf.known`, `spotify/*/credentials.json` and `soundtap/api-token`
only to learn the values it must remove, and never copies them into the bundle.

## Fixtures and the update matrix

`app/testdata/nv-<release>/` is a copy of `/mnt/nv` as that release leaves it, with obviously
fake values (`example.com`, `home`/`homepass1`, no real address, serial or password).
[`nvstate_test.go`](../../app/cmd/soundtapd/nvstate_test.go) runs it through the loaders
soundtapd opens at boot:

- **N-1 to N.** Every set is copied to a temporary `/mnt/nv` and loaded; every value must come
  back, no loader may report an error or leave a `.corrupt` copy, and what this build then
  saves must load again (`TestNVFixturesLoadWithoutLoss`).
- **N to N-1.** A rollback runs the older build on newer files. The test gives the newest set
  a field only a later build knows and the settings a schema one past `SchemaVersion`: every
  known value still loads, opening changes no file, and the settings refuse writes
  (`TestNVRollbackLeavesNewerFilesInPlace`).
- **The list.** `nvFiles` in the test names every file the tables above list. User state
  needs a fixture in the newest set; any other name needs either a fixture or a reason
  (another program's format, a log, stock bytes, a test elsewhere). A name in the tables but
  not in `nvFiles`, a listed fixture that is missing, or an unlisted file in the set fails
  `TestNVEveryListedFileHasAFixture`.

To add a file: add its row here, its line to `nvFiles`, a fixture written by its own writer
(add the write to `writeNV` and run `go test ./cmd/soundtapd -run TestNVFormats -update-nv`
before 0.1.0; write a hand-written file such as `ir.map` yourself) and a check in `loadNV`.
`multiroom-server` is checked in `app/internal/web/nvstate_test.go` and the Wi-Fi files
through the Wi-Fi manager in `app/internal/wifi/manager_internal_test.go`.

For a release that changes a format: copy the newest set to `nv-<new release>/`, change
`nvCurrent` to it, regenerate it with `-update-nv`, and leave the older set as it is. Both
sets then load in every later build. A released set is never edited
([ADR 0120](../decisions/0120-on-box-state-migrates.md)).
