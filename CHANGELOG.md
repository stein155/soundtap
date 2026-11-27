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

- **SoundTouch 20: joins open networks** (without a password), and an open network the
  original software had saved is taken over on the first install.
- **SoundTouch 20: joins older routers with WPA (TKIP)**, and WPA2 routers set to TKIP, as the
  original software did.
- **SoundTouch 20: joins WPA3 networks**, and networks that mix WPA2 and WPA3, with the same
  password as always. Where your router offers it, it also uses protected management frames, so
  nobody nearby can knock it off your Wi-Fi. The other speakers join networks that mix WPA2 and
  WPA3 as before, over WPA2; a WPA3-only network needs support their Wi-Fi chip's driver does not
  have yet.
- **Joins WEP networks**, on every speaker, as the original software did: pick *WEP* as the
  security type, or pick the network from the list and type its WEP key. A WEP network the
  original software had saved is taken over on the first install.
- **The first install also keeps your Radio Browser presets** from the original software,
  not only your TuneIn ones. They appear shortly after the first start, once the speaker is
  online: SoundTap asks radio-browser.info for each station's stream, sending only the
  station's id. A preset you set yourself in the meantime is never replaced, and the log says
  for each preset whether it was taken over or why not.

### Fixed

- **SoundTouch 20: the setup network handles phones better.** The speaker's network chip now
  accepts what a phone sends it, keeps messages for a phone that has gone to sleep until it
  wakes, and sends messages for every phone at the slowest speed, so phones further away hear
  them too. A phone on it still gets no address on our test speaker; that is not solved yet.
- **SoundTouch 20: phones that save battery stay connected to the setup network**: its
  broadcasts now come exactly on time, when a sleeping phone wakes to listen for them.
- **SoundTouch 20: the setup network stays up while a phone is on it**, instead of closing
  after a few minutes to try your home network again, and closes again once the phone has left.
- **SoundTouch 20: the display counts down while you hold Preset 2 and Volume Down** (or
  Preset 1 and Volume Down for a reset), with the seconds left and a bar, and says "Setup mode
  on" when the setup network starts.
- **The log shows how the setup network is doing**: when it started broadcasting, which phones
  are on it, and each address request a phone makes, without a line every few seconds.
- **SoundTouch 20: the countdown also shows while the display shows the start-up or setup
  screen.**
- **SoundTouch 20: on networks that mix WPA and WPA2, apps find the speaker again.** The
  speaker now also receives such a network's broadcasts, which the AirPlay, Spotify and
  SoundTap apps use to find it.
- **A WPA3 network is no longer given up after a passing Wi-Fi hiccup.** When the Wi-Fi
  software failed to start once for an unrelated reason, the speaker stopped offering WPA3 and
  protected management frames until it was restarted.
- **Radio Browser presets from the original software are no longer given up at once** on a
  speaker that cannot reach Radio Browser right after the first install: the week SoundTap
  keeps trying now starts once the speaker knows the time.
- **A factory reset no longer brings back the original software's Radio Browser presets**
  when it came while SoundTap was still looking them up.
- **On a network that hands out no gateway**, or has no internet, the display and the Wi-Fi
  lamp now say connected instead of "Connecting" for ever.

## [app-v0.1.0-alpha.6] - 2026-11-27

- The SoundTap app that SoundTap Core 0.1.0-alpha.6 carries; what it changes is listed under
  that release.

## [os-v0.1.0-alpha.6] - 2026-11-27

The sixth alpha of SoundTap Core 0.1.0.

### Fixed

- **SoundTouch 20: the setup network appears** when you hold Preset 2 and Volume Down. Its
  network chip now sends the setup network's beacons itself.
- **SoundTouch 20: joins more routers**, among them networks that still offer older WPA
  next to WPA2 and networks that mix WPA2 and WPA3. When a join fails, the settings say
  whether the password was wrong or the network's security is not supported, instead of
  staying on "Connecting".
- **SoundTouch 20: the first install takes over your Wi-Fi** from the original software, also
  when the network chip needs a moment to answer.
- **SoundTouch 20: the display counts up while SoundTap installs** from the original software,
  instead of staying at 1%.
- **SoundTouch 20: a two-second press confirms**, as on the other speakers, instead of four
  seconds.
- **The first install keeps your name and radio presets even when the first start is cut
  short**: the takeover from the original software is only marked as done once everything is
  stored, and the log says what was found and why a preset was left out.
- **SoundTouch 20: Wi-Fi stays connected while idle**: routers that check an idle speaker no
  longer drop it, so AirPlay and the display stop flipping to "Connecting".
- **SoundTouch 20: finds your network among many**: in a street full of networks your own is never
  pushed out of the list, and the network list no longer breaks with very many networks around.
- **SoundTouch 20: steadier joins**: a router that resends a step of the handshake, or that
  restarts, is handled; a join that takes long is stopped cleanly instead of repeating.
- **SoundTouch 20: setup network**: a phone on the setup network keeps its place when others try to
  join.
- **With a network cable in, the settings show the wired connection**, not the setup network.
- **The install page finds your speaker**: it names each model's own setup address (the
  SoundTouch 20's included), waits long enough for the SoundTouch 20, and finds a speaker that
  moved to another address. In Safari and Firefox, which do not let a web page reach the
  speaker, it sends you straight to the download route.

## [app-v0.1.0-alpha.5] - 2026-11-26

- The SoundTap app that SoundTap Core 0.1.0-alpha.5 carries; what it changes is listed under
  that release.

## [os-v0.1.0-alpha.5] - 2026-11-26

The fifth alpha of SoundTap Core 0.1.0.

### Added

- **A Sound page for each speaker**: a Sound row at the top of the Speaker group opens the
  speaker's sound settings: bass and the stereo output, or on the SoundTouch 300 its tone,
  audio mode, TV settings, extra speakers and ADAPTiQ.
- **A launch screen**: the app opens on the SoundTap mark while it loads, in light and dark.

### Changed

- **SoundTouch 20: a calmer display while music plays**: no clock or status line, a bigger
  title on two lines, and the screen moves a pixel every minute against burn-in. The standby
  clock is drawn thinner, closer to the original.
- **Every speaker serves the same app**: settings no longer single out the speaker you
  opened the page on as "this speaker".

### Fixed

- **SoundTouch 20: steadier Wi-Fi while streaming**: its network chip no longer stops to write
  diagnostics, it connects to the strongest access point of your network rather than the one it
  heard most often, and leaving a network no longer restarts its Wi-Fi radio.
- **SoundTouch 20: a dark display in standby with the clock off**, instead of "Connecting…".
- **Wi-Fi comes back by itself** after its helper stopped or a setup network failed to open,
  instead of waiting for a restart or the setup buttons.
- **SoundTouch 300: SoundTap no longer restarts the bar by itself** when its sound processor
  stops answering. That restart could switch the TV on. If the bar stops responding, unplug
  it for a few seconds.
- **The menu marks the page you are on**, Settings and Media servers included.
- **Quieter logs**: a device on the network that is no media server, and Bluetooth's own
  messages, are logged once.

## [app-v0.1.0-alpha.4] - 2026-11-25

- The SoundTap app that SoundTap Core 0.1.0-alpha.4 carries; what it changes is listed under
  that release.

## [os-v0.1.0-alpha.4] - 2026-11-25

The fourth alpha of SoundTap Core 0.1.0.

### Added

- **An update says how far it is**: the update page shows the percentage while the speaker
  downloads and writes the update, whenever the speaker can tell (`updateProgress` on
  `GET /api/version`), and no number when it cannot.
- **Media servers show what is on them**: under each server, the names of its top-level
  folders, such as Music and Audiobooks.
- **Recently played**: the ⋯ of an entry, or a long press, opens a sheet to play it, put it on
  a preset or remove it from the list. The tiles on the start page show their service.
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

- **Forget one Bluetooth device at a time**: each paired phone or tablet on the Bluetooth page
  has its own Forget, after a button press on the speaker (`POST /api/bluetooth/forget`).
  Forget all devices is gone from the page. The speaker checks that the device is still paired
  before it asks for the press, so a list that went stale no longer sends you to the speaker
  for nothing.
- **One card on the Updates page**: a newer SoundTap Core is the card when one is out, since it
  carries the app; otherwise the app's card.
- **Wi-Fi settings in three steps**: what the speaker is connected to and the networks it
  remembers, a page that finds the networks nearby (search again from the top bar), and a
  sheet for the chosen network's password. A hidden network is typed in under Other network.
- **First setup in two steps**: name the speaker, then pick your Wi-Fi from a list of the
  networks nearby; the language can be changed at the top. **An update follows through on a
  page of its own**, step by step, until the speaker is back on the new version. Settings marks
  a speaker with an update waiting, and a stereo pair shows which side each speaker plays.
- **The speaker page's settings as designed**: Connection and maintenance in one group, the
  alarm picks a preset (a Spotify playlist too, named with its service), the support bundle
  has its own section, and the red row reads Clear presets and settings.
- **Your own builds without a key**: upload any `.stu` on the Updates page under *Upload a
  file*, signed or not, and press a button on the speaker to install it; this works on every
  speaker. A signature now only lets an update install without you. *Your own signing key*
  is gone from *For developers*: a key set earlier is no longer used, and the next install
  removes it.
- **Updating starts after one confirmation**: a release the speaker fetches itself installs
  without a button press on the speaker, alpha or release, an older version too: *Update now*,
  *Update* on a newer app or SoundTap Core and choosing a release.
  *Update* first asks once, as the Bose app does (it takes a few
  minutes and the speaker restarts), then start. After a SoundTap Core update the speaker
  restarts into it by itself, never after a failed install; the update page follows it away
  and back, and on the SoundTouch 300 says the last step can take a while.
- **Every upload needs a press**: a file you upload on the Updates page (your own build, Bose's
  update file, or a release with its `.sig`) waits for one button press on the speaker. A `.sig`
  is still checked, and one that does not match is refused. Before, anyone on the network could
  send a release's `.sig` with another file and wipe the speaker's fallback slot.
  Development builds signed for the bench install on production speakers too, after the press.
- **One favorite**: the presets page shows the six presets and one favorite. Press and hold a
  station to keep it as your favorite; a new one takes its place. Favorites saved earlier stay
  on the speaker and in a backup; the page shows the first.
- **Play from a link** is in the TuneIn search: under the results, for a station TuneIn does
  not list. Its page folds away what to check when a stream does not play. The TuneIn page
  itself no longer has a row for it.
- **The search as designed**: its own bar with the title and a way back, the field under it.
- **Media servers step by step**: a list of the servers on your network; a server opens on its
  own page, and each folder opens deeper, with Back to go up. Starting a track no longer shows
  a message.
- **Spotify**: more room around a playlist's cover, and its play button is black with a white
  glyph.
- **TuneIn in its own colours**: its logo in soft teal on navy. The speaker bar at the bottom
  stands off the page with a line and a soft shadow.
- **The USB stick at power-on needs no signature and also takes Bose's own update file**:
  `Update.stu` alone, without `Update.stu.sig`; holding the keys is the permission. A SoundTap
  package puts SoundTap back; Bose's file for the model goes back to Bose, its bootloader
  included. No install from a stick has run on any model yet.
- **Multiroom sends lossless FLAC between speakers instead of raw PCM**, roughly half to two
  thirds of the data, so a SoundTouch 20 leading a group has far less to push through its
  network chip, where followers dropped out. **Update every SoundTap speaker to this Core
  before grouping them:** a speaker on an older Core can still lead a group but stays silent
  when it follows an updated one.
- **Settings, laid out again as Stockholm's**: straight rows in titled groups (Speakers,
  Household and This app; on a speaker Sound, Speaker, Connection and Maintenance), a chevron on
  every row that opens a page, and switches and sliders that apply at once; the name and the
  alarm keep their own Save. Media servers moved to the menu, your own station to the TuneIn
  search, and firmware upload and crash reports under For developers. The MQTT
  port, TLS and discovery prefix wait behind More options. The stereo/mono switch shows only
  on the SoundTouch 10, as on stock. The colour scheme follows the system; the AUX name and the
  appearance setting are gone. Every group has a grey bar with its title, rows are white with
  line icons, and a speaker's page opens with its drawing large, its name, model and version,
  as in the installer.
- **Now playing shows the artwork large** at the top of the page; on a short screen it is
  small, and on a phone on its side it is left out, so the controls and the volume stay in
  view.

### Removed

- **No brightness setting for the standby clock** (SoundTouch 20): like stock's, the clock
  does not dim, and walks against burn-in instead. A brightness an older page sends is
  ignored.
- **The weather and the date on the SoundTouch 20's standby clock**, with the town search
  (`GET /api/geocode`) and the `city`, `lat`, `lon` and `weatherOn` display settings: the
  clock shows the time alone, as stock's, which keeps the least of the OLED lit, and the
  speaker no longer contacts Open-Meteo. The clock keeps the browser's timezone.
- **Backup and restore in the web UI**; `GET /api/backup` and `POST /api/restore` remain.

### Fixed

- **The favorite is one favorite**: saving a favorite keeps that one alone, and removing it
  leaves the list empty. Favorites an earlier build saved behind the first, which could not be
  seen or removed, go with the next change.
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
- The Wireless Link drew as a SoundTouch 10 where its model came as its short name.

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

- **A new version stays on the speaker.** SoundTap's bootloader can leave a value of several lines in
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

- **A complete system for the speaker.** SoundTap's own bootloader, mainline Linux 6.12 and one
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
