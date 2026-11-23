# Changelog

What changed for owners of a speaker running SoundTap, release by release. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/). SoundTap releases on two lanes
([RELEASING.md](RELEASING.md)), SoundTap Core (the `.stu` package) and the SoundTap app, so
each section is headed by the release's tag: `os-vX.Y.Z` or `app-vX.Y.Z`, and for an alpha,
a prerelease published before a release's hardware approval, `os-vX.Y.Z-alpha.N` or
`app-vX.Y.Z-alpha.N`.

## [Unreleased]

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
