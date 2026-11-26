# 0003. Apple Home through AirPlay 2; no HomeKit bridge, Google Cast or Sonos

Status: accepted — 2026-10-21

## Context

Owners want the speaker in the Apple Home app, in Music Assistant, and playing services beyond
Spotify and AirPlay (Deezer, YouTube Music, Apple Music). Three routes keep coming back, and
each would add a protocol stack to a 128 MiB speaker:

- **A HomeKit (HAP) bridge** such as homebridge or HAP-python, to put the speaker in the Home app.
  The Home app does not render HAP speakers at all (`Speaker` and `SmartSpeaker` show as "Not
  supported"), so bridges fake volume as a lightbulb's brightness. "Play this in the kitchen"
  means Siri routes audio, which needs an AirPlay 2 target. homebridge alone needs 60 to
  100 MB of RAM on ARMv7.
- **Google Cast / Google Home and Sonos.** A Cast session loads the service's own receiver app,
  a web page that needs a browser on the speaker, and adoption through the Google Home app is
  gated on a certificate signed by Google. The Sonos app adopts only devices in its
  certificate-backed (S2) household. Both end at a certificate SoundTap cannot have and would not
  fake.
- **A dedicated Music Assistant player provider**, Python code in Music Assistant's own
  repository talking to the soundtapd API.

The speaker already ships two open receivers that these ecosystems speak: AirPlay 2
(shairport-sync) and a UPnP/DLNA renderer (`app/internal/upnp`).

## Decision

Ecosystems reach SoundTap through the open receivers it already ships. SoundTap writes no bridge,
emulation or provider for one ecosystem.

### Apple Home through AirPlay 2

shairport-sync's AirPlay 2 mode already does HomeKit pair-setup and pair-verify, so the speaker
is added in the Home app as an AirPlay 2 receiver (Add Accessory, *More options…*), assigned to a
room, grouped with HomePods and targeted by Siri for playback and volume. Upstream documents
the procedure in `ADDINGTOHOME.md`; the Home setting *Allow Speaker & TV Access* must be
**Anyone on the Same Network** with the password off. No HAP bridge ships.

Membership needs no persistent key state: shairport derives the AirPlay device id from the MAC
and the accessory's Ed25519 key pair from that id, and its verify path does not check the
controller pairings it keeps in RAM. A reboot on SoundTap's tmpfs `/tmp` and `/run` keeps the
speaker in the home.

shairport-sync keeps `ignore_volume_control = "yes"`, so the Mixer stays the one place volume is
applied ([ADR 0012](0012-single-dac-writer.md)). soundtapd reads shairport's `pvol` metadata and
moves the master volume to match, so the Home slider, Control Center and the phone's volume keys
work.

### No Google Cast, Google Home or Sonos

SoundTap does not implement, emulate or research Google Cast, Google Home or Sonos, as a
receiver or as a controller. Phones play through AirPlay 2 (which also carries Apple Music and,
from an iPhone or Mac, every other app), Spotify Connect ([ADR 0014](0014-spotify-through-librespot.md)),
Bluetooth and UPnP/DLNA. Another service is added as its own open receiver, as librespot is for
Spotify, when such a receiver exists.

### Music Assistant over DLNA and AirPlay

Music Assistant finds every speaker twice without a setting, as a DLNA renderer and as an AirPlay
receiver, and that is how it plays. The DLNA player with flow mode on and FLAC output is the
recommended setup; AirPlay is the choice for a Music Assistant group that mixes SoundTap with
other brands. SoundTap speakers are grouped in SoundTap: Music Assistant plays to the leader and
SoundTap's multiroom carries it to the followers ([ADR 0013](0013-multiroom-through-snapcast.md)).
A speaker is never in a SoundTap group and a Music Assistant group at once. What Music Assistant
lacks is closed in the DLNA renderer, the same way for every DLNA controller.

Alternatives that lost:

- **A HAP bridge.** The Home app shows no speaker service, it cannot route audio, and homebridge
  does not fit beside the audio daemons.
- **A Cast receiver for Deezer and YouTube Music.** It needs the services' web receiver apps, so
  a browser on the speaker, and still fails Google Home adoption.
- **A Sonos ZonePlayer emulation.** It stops at the S2 household certificate.
- **A Music Assistant player provider.** It needs upstream review and maintenance for one
  product line and ties SoundTap's API to Music Assistant's release cycle, for little over a
  DLNA renderer that already passes the playback checks.
- **Music Assistant over AirPlay only.** Music Assistant pushes its own volume on group start,
  sends no FLAC, and shows less of the speaker's state.
- **Music Assistant groups over SoundTap multiroom.** Two sync systems would drive the same
  speakers.

## Consequences

- No Cast, Google Home, Sonos or HAP code, issues or experiments in this repository.
- What only a certified (MFi) receiver gets stays out of reach: an accurate playback state on the
  Home tile, the Home transport controls (shairport forwards remote control for classic AirPlay
  only), and the occasional "No Response" on secondary devices. SoundTap does not chase them.
- Services that reach speakers only through Cast (YouTube Music on Android) stay out unless they
  ship an open protocol. Music Assistant is the route for Deezer, Tidal, Qobuz, YouTube Music
  and Apple Music libraries.
- Every DLNA controller (BubbleUPnP, Home Assistant's DLNA integration, VLC) benefits from what
  the renderer gains for Music Assistant, and SoundTap still runs without Music Assistant.
- SoundTap presets and sources do not appear as Music Assistant sources, and a multiroom group is
  not one Music Assistant player. Music Assistant's DLNA quirks are SoundTap's to work around or
  report upstream; an upstream PR needs the maintainer's approval.
- Exposing SoundTap's own controls (power, presets, source) to Siri and the Home app is a
  separate question and the one place a HAP accessory would add something; it is not done.
- Owners read [docs/owners/music-assistant.md](../owners/music-assistant.md) for which player and settings to
  pick; the recorded Music Assistant checks are there too.

## Code

- `os/package/soundtap-shairport-sync`, `os/overlay/etc/shairport-sync.conf`: the AirPlay 2
  receiver and its `ignore_volume_control`.
- `app/cmd/soundtapd/airplay_meta.go:airplayVolumePercent`: shairport's `pvol` onto the master
  volume; `app/cmd/soundtapd/airplay_meta.go:parseAirplayMeta` reads the metadata pipe.
- `app/internal/upnp`: the DLNA renderer Music Assistant plays to (`upnp.Renderer`), with GENA
  eventing in `app/internal/upnp/events.go` and the actions in `app/internal/upnp/scpd.go`.
