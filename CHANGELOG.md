# Changelog

What changed for owners of a speaker running SoundTap, release by release. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/). SoundTap releases on two lanes
([RELEASING.md](RELEASING.md)), SoundTap Core (the `.stu` package) and the SoundTap app, so
each section is headed by the release's tag: `os-vX.Y.Z` or `app-vX.Y.Z`, and for an alpha,
a prerelease published before a release's hardware approval, `os-vX.Y.Z-alpha.N` or
`app-vX.Y.Z-alpha.N`.

## [Unreleased]

### Added

- **The six presets on the start page**, as the speaker's own buttons: a tap plays one, an
  open one opens the presets page, and the one playing is ringed.

### Changed

- **Settings look like the installer**: every group has a grey bar with its title, rows are
  white with line icons, and a speaker's page opens with its drawing large, its name, model
  and version.
- **Now playing shows the artwork large** at the top of the page.

### Fixed

- The Wireless Link drew as a SoundTouch 10 where its model came as its short name.

## [app-v0.1.0-alpha.4] - 2026-11-24

- The SoundTap app that SoundTap Core 0.1.0-alpha.4 carries; what it changes is listed under
  that release.

## [os-v0.1.0-alpha.4] - 2026-11-24

The fourth alpha of SoundTap Core 0.1.0.

### Added

- **A firmware install names the bootloader copies it rewrote**, in its answer and on the
  Updates page, so it is clear whether the SPL and U-Boot were replaced.
- **`/api/health` reports the SPL and U-Boot on NAND** by SHA-256 (`boot.bootloader`), so the
  boot chain can be checked on a speaker without SSH.
- **An install from Bose's software keeps the SoundTouch 20's clock settings**: on or off,
  12 or 24 hours and the time zone, so the standby clock shows at once without opening the
  settings first.

- **Previews for more SoundTouch models**: the SoundTouch 20 and 30 Series III, the
  SoundTouch 30 Series I/II, the Portable, the SA-5 and the SoundTouch adapter. They are worked
  out from Bose's software alone and have never run on hardware, their recovery included. They
  install only from the release's preview package (`SoundTap-Core-X.Y.Z-preview.stu`), which the
  installers fetch after an extra confirmation. The Lifestyle 600/650 module is refused.

### Changed

- **The USB stick at power-on needs no signature and also takes Bose's own update file**:
  `Update.stu` alone, without `Update.stu.sig`; holding the keys is the permission. A SoundTap
  package puts SoundTap back; Bose's file for the model goes back to Bose, its bootloader
  included. No install from a stick has run on any model yet.
- **Multiroom sends lossless FLAC between speakers instead of raw PCM**, roughly half to two
  thirds of the data, so a SoundTouch 20 leading a group has far less to push through its
  network chip, where followers dropped out. **Update every SoundTap speaker to this Core before grouping them:** a
  speaker on an older Core can still lead a group but stays silent when it follows an updated
  one.

### Fixed

- **The bass no longer swells and fades while the volume changes** on the SoundTouch 10 and
  20: their codec now gets its level as stock sends it, a gain word and a decibel word,
  instead of one 48-bit number.
- **No more pop on a SoundTouch 20 leading a multiroom group**: the stream server and the radio
  decoder run below the sound card's feed, which ran dry for a moment.
- **The package report lists the SoundTouch 300** among the models a Core package installs on;
  it takes the Wireless Link's modules and was left out.
- **The SoundTouch 20 holds its Wi-Fi uploads back instead of dropping them on the network
  chip**: when the chip's transmit buffers are full, frames wait on the speaker, the chip
  has one buffer more and tries a frame on air seven times instead of five. A multiroom
  leader lost about one TCP segment in ten this way, and followers dropped out. `/api/health`
  shows the chip's transmit counters as `bco`.

### Changed

- **Settings, laid out again as Stockholm's**: straight rows in titled groups (Speakers,
  Household and This app; on a speaker Sound, Speaker, Connection and Maintenance), a chevron on
  every row that opens a page, and switches and sliders that apply at once; the name and the
  alarm keep their own Save. Media servers moved to the menu, your own station to the TuneIn
  page, and the signing key, firmware upload and crash reports under For developers. The MQTT
  port, TLS and discovery prefix wait behind More options. The stereo/mono switch shows only
  on the SoundTouch 10, as on stock. The colour scheme follows the system; the AUX name and the
  appearance setting are gone.

### Removed

- **The weather and the date on the SoundTouch 20's standby clock**, with the town search
  (`GET /api/geocode`) and the `city`, `lat`, `lon` and `weatherOn` display settings: the
  clock shows the time alone, as stock's, which keeps the least of the OLED lit, and the
  speaker no longer contacts Open-Meteo. The clock keeps the browser's timezone.
- **Backup and restore in the web UI**; `GET /api/backup` and `POST /api/restore` remain.

## [app-v0.1.0-alpha.3] - 2026-11-23

- The SoundTap app that SoundTap Core 0.1.0-alpha.3 carries; what it fixes is listed under
  that release.

## [os-v0.1.0-alpha.3] - 2026-11-23

The third alpha of SoundTap Core 0.1.0.

### Security

- **The MQTT bridge's websocket transport** masks its frames with a cryptographic random key
  (gorilla/websocket, GO-2026-6278).

### Fixed

- **The bootloader no longer stores board values it cannot read**, which on some speakers
  filled its settings with unreadable data.
- **Turning the volume on a SoundTouch 20 right after starting a preset** no longer makes the
  sound drop out for a moment, heard as a pop.
- **Spotify Connect starts once the speaker is on the network**, instead of failing once at
  every start-up and trying again.

## [app-v0.1.0-alpha.2] - 2026-11-23

- The SoundTap app that SoundTap Core 0.1.0-alpha.2 carries; what it fixes is listed under
  that release.

## [os-v0.1.0-alpha.2] - 2026-11-23

The second alpha of SoundTap Core 0.1.0. Install it over 0.1.0-alpha.1: on some speakers that
version could not confirm its own boot and would have gone back to the previous version after
a few restarts.

### Fixed

- **A new version stays on the speaker.** Our bootloader can leave a value of several lines in
  its settings; the player could not read them then, so it never confirmed the new version and
  the speaker would have returned to the previous one after four restarts or power cuts.
- **A restart in the first minute of a new version** no longer counts as proof that it works:
  the speaker still goes back to the previous version if the new one keeps failing.
- **A package that is not the one its signature names** no longer leaves its system in the
  other slot when the install refuses it.

## [app-v0.1.0-alpha.1] - 2026-11-23

- The SoundTap app that SoundTap Core 0.1.0-alpha.1 carries; what it brings is listed under
  that release.

## [os-v0.1.0-alpha.1] - 2026-11-23

The first public alpha of SoundTap Core 0.1.0, with app 0.1.0-alpha.1: a prerelease, signed
like a release but published before the hardware tests of 0.1.0 have passed on every model.

### Added

- **A complete system for the speaker.** Our own bootloader, mainline Linux 6.12 and one
  player, `soundtapd`, replace Bose's software on the SoundTouch 10, the SoundTouch 20
  (Series I), the SoundTouch Wireless Link adapter and the SoundTouch 300. One universal
  package installs on all of them; the SoundTouch 20 and 30 of other series install it as a
  preview.
- **Installing from Bose's software without a cable.** The installer finds every speaker on
  the network that still runs Bose's software and installs the ones you pick, from one
  command in Terminal or PowerShell or from the browser by IP address. Uploading the package
  over the setup network and a USB stick are the fallbacks. Your stock Wi-Fi profile is
  carried over when it can be read.
- **The speaker's own web page**, at `http://soundtap.local`, that works like an app on any
  phone, tablet or computer: start page, presets, now playing, settings, light and dark
  themes, and 25 languages.
- **Internet radio** from TuneIn and your own stream URLs, with the song and album art read
  from the station's stream. A station keeps playing from its last known streams when TuneIn
  does not answer.
- **Spotify Connect**, and browsing your playlists, albums and liked songs from the speaker's
  page.
- **AirPlay 2**: play from iPhone, iPad and Mac, group with HomePods, add the speaker to the
  Apple Home app.
- **Multiroom** through Snapcast: group speakers with a volume per room, let any speaker
  lead, and add any Snapcast client. Two speakers of any model can be a stereo pair that
  acts as one speaker.
- **Six presets** on the speaker's buttons and the remote of the SoundTouch 10 and 20: a
  station, a Spotify playlist or something from your media server.
- **Your own music**: browse a NAS or media server over UPnP/DLNA, or push music from apps
  such as BubbleUPnP and VLC.
- **MQTT** for home automation: power, volume, presets, play/pause and what is playing, with
  discovery so compatible hubs find the speaker by themselves.
- **The SoundTouch 20's display**: now playing, and a standby clock with the weather for a
  town you pick.
- **The SoundTouch 300 soundbar**: radio, Spotify, the TV and HDMI inputs, volume and mute
  from the page, the bar's buttons and its remote; the TV input can be shared with a group.
- **Bluetooth** on the models that have it (the eight newest pairings are kept, as on stock),
  and AUX on the SoundTouch 10 and 20.
- **Sleep timer, alarm, recently played and announcements.**
- **Safe updates.** Core and app update from the speaker's Updates page into the slot that is
  not running, and the speaker keeps a new version only after it has started cleanly; a
  failed start falls back to the previous one. Every package is signed, and a speaker refuses
  one without a valid signature.
- **Recovery with a USB stick**: a signed package on a stick, and Volume Down + Preset 4
  (SoundTouch 10 and 20) or both buttons (Wireless Link) at power-on.
- **The way back to Bose**: upload Bose's own update file on the Updates page and confirm with
  a press on the speaker.
- **Your own builds**: set your own signing key on the Updates page and install builds you
  signed yourself.
- **A support archive** under *Settings*, redacted, for bug reports.

### Security

- No SoundTap account, cloud service or tracking. The speaker contacts only what it needs:
  your network's time servers first, public time services, the music services you use, and
  GitHub for updates, and it asks GitHub nothing before its clock is set.
- Changes that matter need a button held on the speaker itself: a reboot, a factory reset,
  Wi-Fi changes, a restore, installing a package file by hand (the way back to Bose
  included), setting a signing key and debug access. Installing the latest release with the
  Update button needs no press. A web page on the internet cannot drive the speaker through
  your browser.
- Production images have no SSH, no root login, a read-only root filesystem and an inbound
  filter; the player and the media services run as their own users, and only a small helper
  that writes the flash and reboots runs as root. Development images built with the alpha
  profile still accept a root login without a password and belong only on a network you
  trust; releases and alphas such as this one are production images.
- The setup access point is open only while it is needed and answers only the speaker's own
  subnets.
