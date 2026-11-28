# Frequently asked questions

**The project:** [What is SoundTap?](#what-is-soundtap) ·
[Why does it exist?](#why-does-it-exist) ·
[Is it really built from scratch?](#is-it-really-built-from-scratch) ·
[How is it different from other SoundTouch projects?](#how-is-it-different-from-other-soundtouch-projects) ·
[Is it affiliated with Bose?](#is-it-affiliated-with-bose) ·
[Is it open source?](#is-it-open-source) ·
[What does it cost?](#what-does-it-cost)

**Speakers:** [Which speakers are supported?](#which-speakers-are-supported) ·
[What about the SoundTouch 30, Portable or Wave?](#what-about-the-soundtouch-30-portable-or-wave) ·
[I want to buy a used SoundTouch for this. Which one?](#i-want-to-buy-a-used-soundtouch-for-this-which-one)

**Installing:** [Can I brick my speaker?](#can-i-brick-my-speaker) ·
[Can I go back to Bose's software?](#can-i-go-back-to-boses-software) ·
[USB cable?](#does-the-first-install-need-a-usb-cable) ·
[Do I need Linux?](#do-i-need-linux) ·
[What happens to my presets?](#what-happens-to-my-bose-presets) ·
[Does the Bose app keep working?](#does-the-bose-soundtouch-app-keep-working) ·
[Which Wi-Fi?](#which-wi-fi-networks-does-it-join)

**Everyday use:** [Internet](#does-it-need-the-internet) ·
[Privacy](#does-it-phone-home) ·
[App](#is-there-an-app) ·
[Spotify](#does-spotify-work) ·
[AirPlay and Apple Home](#does-airplay-work-and-apple-home) ·
[Google Cast and Sonos](#why-no-google-cast-google-home-or-sonos) ·
[Alexa](#alexa) ·
[MQTT](#mqtt) ·
[Multiroom](#multiroom) ·
[Remote](#does-my-remote-work) ·
[Buttons](#what-do-the-buttons-do) ·
[Bluetooth and AUX](#bluetooth-and-aux) ·
[Stereo pairs](#can-i-pair-two-speakers-as-left-and-right) ·
[Languages](#which-languages) ·
[Bug reports](#how-do-i-report-a-bug) ·
[Crash reports](#can-the-speaker-report-its-own-crashes)

**Security:** [Is it safe on my network?](#is-it-safe-on-my-network) ·
[Can I install my own builds?](#can-i-install-my-own-builds) ·
[Why hold a button?](#why-does-the-speaker-ask-me-to-hold-a-button)

---

## The project

### What is SoundTap?

New software for Bose SoundTouch speakers. Not an app, not a server you run next to them:
it replaces the speaker's own software completely, from the first instruction the chip runs
to the web page on your phone. Afterwards the speaker works on its own, on your home
network, with internet radio, Spotify Connect, AirPlay 2, multiroom, MQTT and its
six preset buttons.

### Why does it exist?

Bose ended its cloud service on 6 May 2026. Browsing music services, cloud presets and new
firmware went with it; Bose's updated app still offers local control
([Bose's announcement](https://www.bose.com/soundtouch-end-of-life-row)). SoundTap gives the
speaker a maintained operating system and its own web app, without a SoundTap account or server.

### Is it really built from scratch?

Yes. Neither the repository nor the package holds Bose files
([ADR 0049](../decisions/0049-no-stock-bytes.md)); every layer is SoundTap's own or open source:

| Layer | SoundTap |
| --- | --- |
| Bootloader | SoundTap's SPL and U-Boot, built from mainline source |
| Kernel | mainline Linux 6.12 with SoundTap's own device trees |
| Drivers | SoundTap's own: audio, keypad, LEDs, infrared, the ST20 display |
| System | built by Buildroot from pinned source |
| Player and web page | `soundtapd` (Go) and a React app, written for SoundTap |
| Updates | SoundTap's own installer and A/B slots |

Two things the speaker already carries stay as they are ([ADR 0049](../decisions/0049-no-stock-bytes.md)): ST10
and ST20 retain their own stock codec configuration for the original tuning, and the ST20
retains its BCO boot loaders (BFL/BSL) for recovery. SoundTap's BCO app includes its own WiLD core;
it does not load a stock radio blob. ST10/WLA Wi-Link firmware comes from upstream
`linux-firmware`, with its provenance recorded.

### How is it different from other SoundTouch projects?

Several good projects keep SoundTouch speakers alive. Most of them either **emulate Bose's
cloud** on a computer in your home, or **add a small program on top of Bose's software**. Both
keep Bose's software on the speaker, which is a sensible choice if you want to change as little
as possible.

SoundTap replaces the **whole operating system** instead. That is more work, but it lets
SoundTap add things the speaker never had (AirPlay 2, Snapcast multiroom, UPnP/DLNA, MQTT, a
new web page), fix problems in the system itself, and keep receiving updates without Bose
code.

### Is it affiliated with Bose?

No. SoundTap is an independent project. It is not affiliated with, endorsed by or sponsored
by Bose Corporation. *Bose* and *SoundTouch* are trademarks of Bose Corporation, used here
only to say which speakers SoundTap runs on.

### Is it open source?

The SoundTap code (bootloader configuration, drivers, system, `soundtapd`, the web app and all
tools) is licensed under the GNU GPL v3.0. The tree contains no stock firmware, and the
public repository starts from one clean commit, so its history holds none either
([ADR 0049](../decisions/0049-no-stock-bytes.md)). Local stock inputs stay outside git.

### What does it cost?

Nothing. No account, no subscription, no ads, and nothing to buy.

---

## Speakers

### Which speakers are supported?

The SoundTouch 10, the SoundTouch 20 Series I, the Wireless Link adapter and the SoundTouch 300
soundbar, whose own processor keeps driving its bass module, rear speakers and HDMI
([notes](install.md#soundtouch-300)). All of them run the same package.

- **Waiting for hardware validation:** the SoundTouch 20 Series II.
- **Previews:** the SoundTouch 20 Series III, the SoundTouch 30 generations, the Portable, the
  SA-5 and the SoundTouch adapter (SA-4, Stereo JC, Wave, CineMate, Lifestyle, VideoWave; on a
  Lifestyle only the older adapter). They are worked out from Bose's software and have never
  run on hardware, their way back included. The installers offer them only after an extra
  confirmation.
- **Refused by the installers:** the Lifestyle 600/650 module, which has no recovery gesture at
  all, and with it a Lifestyle's newer adapter, which the installers cannot tell apart from it.

SoundTap is alpha software. "Supported" means it runs every day on the maintainer's speakers and that a
release goes out only after it has passed that model's hardware checklist (no candidate has
passed it yet). It does not mean every feature is proven on every model. The
[compatibility matrix](compatibility.md) says what works on each, the
[hardware checklist](../hardware/hardware-test.md#release-checklist-per-model) what has run on
which speaker, and the [road to 1.0](roadmap.md) what is still open.

### What about the SoundTouch 30, Portable or Wave?

The SoundTouch 30, the Portable and the Wave SoundTouch adapter are previews: the release's
preview package installs on them, worked out from Bose's software, but none has run on
hardware, and their recovery gestures are untested. Other models need someone with the speaker
and some patience. The first step is collecting **read-only** information from a speaker that
still runs Bose's software, and nothing in it risks the speaker:
[open a device issue](https://github.com/stein155/soundtap/issues/new?template=device.yml).

### I want to buy a used SoundTouch for this. Which one?

The **SoundTouch 10** is small, cheap second-hand, and has the most hours on SoundTap. The
**Wireless Link adapter** turns any amplifier or active speakers into a SoundTap player. The
**SoundTouch 20 Series I** has a display; the other SoundTouch 20 generations are not proven yet.

---

## Installing

### Can I brick my speaker?

SoundTap is designed to make that unlikely: updates go into the half of the storage that is
not running, and the speaker keeps a new version only after it has stayed up for a minute;
otherwise it returns to the previous one after a few restarts (seen on the SoundTouch 10, 20
and Wireless Link with an earlier setting; not yet with the current one, nor on the
SoundTouch 300). SPL/U-Boot and the boot
environment are shared, however: a failed bootloader write is outside that A/B protection.
Below all software, the chip can be started over USB from a computer. That is a developer's
tool, not a proven rescue: a full restore that way has not run on any model yet, and on the
SoundTouch 20 it means opening the speaker. A SoundTouch 300 has been seen answering over USB
that way, but nothing has been restored on one. **Alpha means: read the
[install guide](install.md) first, and do not start with your only speaker.**

### Can I go back to Bose's software?

Yes. Download Bose's own update file for your model, choose it on the speaker's Updates page,
confirm the return to Bose and hold a button on the speaker. No SoundTap signature is needed
for Bose's file. This way back has run on all four models, though not yet from the release
candidate. On a SoundTouch 20 Bose's software then puts its own app back on the network
processor itself, when it finds another one there (seen by the owner). Bose then comes back
without its Wi-Fi network (Bose kept that on the network processor), so you set up Wi-Fi again
in the Bose app, and it may come back without its presets. A USB stick is built to take you
back too, with Bose's own update file for your model on a stick as `Update.stu` and the
power-on gesture, but it does not work yet: on the SoundTouch 10 a known fault refuses Bose's
file, and it has not been tried on the other models. See
[Going back to Bose](install.md#going-back-to-bose).

### Does the first install need a USB cable?

No. A speaker on your Wi-Fi downloads SoundTap itself from the update server. If that does not
work, you can upload the package on Bose's own update page (port 17008, in its setup mode) from
the browser of a phone or a computer. A first install from a USB stick with Bose's Volume −
and Preset 4 gesture (ST10, ST20) has not been tried; stock Wireless Link has no USB-stick
update gesture. Once SoundTap runs, all later updates
install over Wi-Fi from the speaker's settings page. See the [install guide](install.md).

### Do I need Linux?

No. The one-line install runs in Terminal on a Mac or Linux and in PowerShell on Windows, and
the other routes need only a browser. Building SoundTap yourself does need a Linux machine
with Docker.

### What happens to my Bose presets?

On the first SoundTap start, locally stored radio presets (TuneIn and Radio Browser) are
imported when there is no SoundTap preset file yet. Only radio stations come over: presets for Spotify, other
music services, or music from your computer or a media server stay empty. This needs the stock
`Presets.xml` to still be on the speaker. This has not yet been checked on
hardware. Existing SoundTap presets are kept, and a factory reset does not import stock presets
again. To store a preset, search for a station and hold a preset tile, or choose a Spotify
playlist or album or a media-server item.

### Does the Bose SoundTouch app keep working?

On stock firmware, Bose's updated app still supports local setup, control and grouping
([Bose's announcement](https://www.bose.com/soundtouch-end-of-life-row)). After installing
SoundTap, use the web page served by the speaker. Bose's app is not a supported SoundTap interface.

### Which Wi-Fi networks does it join?

The networks Bose's software joined: open, WEP, WPA and WPA2, and on the SoundTouch 20 only on
2.4 GHz. The SoundTouch 20 is also built to join WPA3; the other speakers join a network that
mixes WPA2 and WPA3 over WPA2, but not one that is WPA3 only. On a SoundTouch 20, WPA2, WPA,
mixed WPA/WPA2 and WEP networks have been tested and joined, and one that requires protected
management frames. WPA3, a hidden network and an open network have not been tested on it yet,
and on the other speakers none of these kinds has had its own test. A phone has joined the
SoundTouch 20's setup network and got an address; choosing your Wi-Fi on its setup page from a
phone has not been tested on that model yet. See
[Put the speaker on your Wi-Fi](install.md#put-the-speaker-on-your-wi-fi).

---

## Everyday use

### Does it need the internet?

Internet services include radio, Spotify, album artwork, time synchronisation and
update checks/downloads. AirPlay with a local source, Bluetooth, AUX, your own media server
and multiroom use the home network; an online sender or service still needs its own internet
connection. With no time sync, the speaker may not have an accurate clock after power loss.
Until its clock has been set after a start, the speaker skips the alarm, shows no standby clock
(SoundTouch 20) and does not check for updates; the Updates page then says the clock is not
set yet.

### Does it phone home?

There is no SoundTap account, telemetry service or tracking server. Besides selected media
services, the speaker synchronises its clock automatically and contacts GitHub when checking
for updates. These are the external contacts:

| What | Where |
| --- | --- |
| Radio search and streams | TuneIn and the station you play |
| Spotify | Spotify |
| Album artwork | Apple's public iTunes search |
| Clock | the NTP servers your router names through DHCP, then Cloudflare's and Google's public time services (`time.cloudflare.com`, `time.google.com`), which allow any device without registration. Servers of your own in `/mnt/nv/ntp-servers` replace those two |
| Checking for updates | GitHub releases, once the clock is set |

[Crash reports](#can-the-speaker-report-its-own-crashes) are off unless you turn them on, and
do not send anything by themselves: *Report this crash* opens a pre-filled GitHub issue in your
browser that you read before sending.

### Is there an app?

The speaker's web page is the app. Open it on any phone, tablet or computer and add it to the
home screen. It installs like an app, has light and dark mode, and needs no app store, no
account and no updates of its own: every speaker serves the current version.

### Does Spotify work?

Yes, as **Spotify Connect**: the speaker shows up in the Spotify app like any other Connect
speaker. You never type your Spotify password into SoundTap. Once you have played to it from
the Spotify app, you can also browse your playlists, albums and liked songs from the speaker's
page and store them as presets. Spotify Connect on third-party speakers needs **Spotify
Premium**.

On a skip, the speaker closes and reopens the Spotify audio stream so buffered sound from the
previous track cannot come back. A track that ends by itself plays to the end, but fully
gapless transitions (as on live albums) are not guaranteed. Audio already queued at the output
still causes a short delay before a change is heard, especially in multiroom.

### Does AirPlay work? And Apple Home?

**AirPlay 2** is built in on every supported model, for iPhone, iPad and Mac, and you can add
the speaker to the **Home** app as an AirPlay speaker. It is not a HomeKit accessory, and the
Home tile's play/pause buttons do not work: Apple reserves those for certified receivers
([ADR 0003](../decisions/0003-apple-home-via-airplay-no-cast-no-sonos.md)). On the SoundTouch 20
it needs SoundTap's own BCO app (ADR 0031), which the install puts on it.

### Why no Google Cast, Google Home or Sonos?

Because neither can be done honestly ([ADR 0003](../decisions/0003-apple-home-via-airplay-no-cast-no-sonos.md)).
Google Home and the Sonos app only adopt speakers that hold a certificate signed by Google or
Sonos; there is no open mode, and faking one is not something SoundTap would ship. Casting itself
also needs the service's own receiver app, a web page that needs a browser the speaker does not
have. Android phones can use Spotify Connect, Bluetooth or UPnP/DLNA apps such as BubbleUPnP
instead, and Music Assistant plays Deezer, Tidal, Qobuz, YouTube Music and
Apple Music to the speakers over AirPlay or UPnP/DLNA (see [Music Assistant](music-assistant.md)).

### Alexa?

No. There is no open way to make a speaker an Alexa device.

### MQTT?

Yes. Enter your MQTT broker under *Settings → Integrations → MQTT* and the speaker publishes
its state and listens for commands, so any MQTT client can use it (Node-RED, openHAB, Domoticz,
ioBroker, scripts). While the bridge is enabled, it also publishes discovery configuration
for compatible hubs. Hubs can also send it audio through UPnP/DLNA, and announcements
through the speaker's notify API. MQTT has been tested on hardware on the SoundTouch 10 only.

**Topic reference.** `<base>` is the base topic from the settings page (default
`soundtap/<deviceID>`). State topics are retained; payloads are plain text except for the JSON update state.

| Topic | Direction | Payload |
|---|---|---|
| `<base>/availability` | state | `online` / `offline` (last will) |
| `<base>/volume/state` | state | `0`..`100` |
| `<base>/power/state` | state | `ON` / `OFF` (`OFF` = standby) |
| `<base>/mute/state` | state | `ON` / `OFF` |
| `<base>/preset/state` | state | name of the current station |
| `<base>/station`, `/track`, `/artist` | state | current station, track, artist |
| `<base>/status` | state | `playing` / `paused` / `stopped` / ... |
| `<base>/sw_version`, `/model` | state | SoundTap version, speaker model |
| `<base>/update/state` | state | JSON `installed_version`, `latest_version`, optional `release_url` |
| `<base>/volume/set` | command | `0`..`100` |
| `<base>/power/set` | command | `ON` / `OFF` (anything else is ignored) |
| `<base>/mute/set` | command | `ON` / `OFF` (anything else is ignored) |
| `<base>/preset/set` | command | the name of one of the six presets |
| `<base>/transport/<key>/set` | command | any payload; `<key>` is `play`, `pause`, `stop`, `next` or `previous` |
| `<base>/update/install` | command | any payload; installs the latest stable SoundTap app, not Core |

The retained discovery configs go under the *discovery prefix* (default
`homeassistant`, the prefix compatible hubs listen on; change it if your hub uses another).

### Multiroom?

Yes, through [Snapcast](https://github.com/badaix/snapcast): group speakers and play the same
music everywhere, in sync, with a volume per room. As on stock, the volume buttons and the
remote change only the speaker they belong to; play, pause and skip on a grouped speaker act
on the whole group. Any SoundTap speaker can lead; the leader
serves Snapcast on port 1704, so any Snapcast client (a Raspberry Pi, a laptop) can join when you give it the speaker's address; the speakers do not announce Snapcast over mDNS.
The stream is lossless FLAC, which Snapcast clients play as built upstream. Keep every speaker
on the same SoundTap Core: one on an older Core stays silent when it follows an updated one.
SoundTap speakers do not group with speakers that still run Bose's software. On hardware, the
SoundTouch 10, SoundTouch 20 and Wireless Link have each led the other two (2026-11-14), and the
SoundTouch 300 has followed (2026-11-15); a SoundTouch 300 leading a group has not been tested yet.

### Does my remote work?

The remote works as before on the SoundTouch 10, including holding a preset to store
it. On the SoundTouch 20 the remote was seen working (2026-10-10, again on the
bench 2026-11-20); no full checklist run is recorded yet. Another
infrared remote can be taught key by key ([runbook](../../RUNBOOK.md#teaching-the-st10-an-ir-remote)).

### What do the buttons do?

- **AUX** (the source key): a tap switches between AUX and Bluetooth. Holding it picks
  Bluetooth, as on stock. A paired phone is forgotten on the speaker's Bluetooth page, one at
  a time.
- **Bluetooth** (Wireless Link): a tap picks Bluetooth; holding it opens pairing.
- **POWER**: acts on the press. Holding it does nothing more: stock's low-power standby,
  which turns the network off, is not supported, because the speaker could then no longer be
  reached or woken from the app.
- **Volume −** with **Preset 1** for ten seconds resets the speaker; with **Preset 2** it
  opens the setup network.

### Bluetooth and AUX?

Both are built for the models that have them, but they are still unproven work, not finished
features: neither has been proven end to end on any model yet. Bluetooth has played on the
SoundTouch 10, the Wireless Link and the SoundTouch 300, AUX on the SoundTouch 10, the 20 and
the Wireless Link; the rest is untested ([checklist](../hardware/hardware-test.md#release-checklist-per-model)). Of the SoundTouch 20s, only the Series III has Bluetooth; Series I and II do not.

To pair a phone, pick Bluetooth on the speaker: with nothing paired yet it opens pairing by
itself, as the Bose software did. Holding the Bluetooth key, or the button on the web page,
opens pairing for another phone.

### Can I pair two speakers as left and right?

Yes, any two speakers, also of different models, though no hardware run is recorded yet. In a
speaker's settings, open *Stereo pair*, pick the partner and the side this speaker plays. The
pair is a multiroom group in which each speaker plays one channel
([ADR 0107](../decisions/0107-stereo-pair-acts-as-one-speaker.md)).

### Which languages?

The language picker offers 25 languages, including English, Nederlands, Deutsch, Français,
Español and Afrikaans. The page follows a supported browser language and falls back to English
otherwise. Choose another under *Settings → Language*. Translations are a good first
[contribution](../../CONTRIBUTING.md#ways-to-help).

### How do I report a bug?

Open *Settings → Maintenance → Help and support → Support bundle* and press any button on the speaker when the page
asks. The browser saves one ZIP file; attach it to an
[issue](https://github.com/stein155/soundtap/issues/new). Nobody has to log in to the speaker
(release builds have no SSH), because the file holds what support would otherwise look up there:

- the diagnostics page: version, model, network, memory and disk, audio, multiroom, discovery
  and the state files;
- the versions and the health report, and the kernel's messages;
- every log in full: this boot's, and the last three boots' the speaker keeps;
- what the kernel kept of the previous boot: how it ended and, after a crash, its record.

Before the file leaves the speaker, every part of it goes through one filter: Wi-Fi passwords
and keys, the names of Wi-Fi networks, the MQTT password, Spotify's stored login, tokens, API
keys, passwords in stream addresses and public IP addresses are replaced by placeholders such
as `<wifi-ssid-1>`. Addresses on your home network stay: they show how the speaker sees it.
Everything inside is plain text, so you can read it before you post it.

### Can the speaker report its own crashes?

Only if you ask it to, and only through you. Turn on *Settings → Maintenance → For developers → Offer crash reports*;
it is off until you do. When the speaker then finds a crash (the app stopped with an error, the
previous start ended in a kernel crash, or a music service kept failing and was put to rest), its
settings page shows a notice. *Report this crash* shows you the report word for word: what
happened, the SoundTap versions, the model, how long it had been running and the error itself,
with the same placeholders as the support bundle. *Open the report on GitHub* opens it as a new
issue in your browser, where you submit it yourself; attach the support bundle there, since the
report holds only the start of the error. There is no SoundTap server: the speaker sends nothing.
A crash you reported or dismissed is not offered again
([ADR 0126](../decisions/0126-opt-in-crash-reports-without-a-server.md)).

---

## Security

### Is it safe on my network?

Not yet as locked down as it will be at 1.0. Like the original Bose software, it lets anyone
on your network control the speaker, and there is no login on its web page yet. Published
packages, alphas included, have no SSH at all, so you never need to log in to the speaker
(only self-built development images accept a root login without a password): for a bug report, attach the [support bundle](#how-do-i-report-a-bug). Use
SoundTap 0.x on a network you trust; see [SECURITY.md](../../SECURITY.md). Published updates are signed, and only a signed release the speaker fetches itself installs without you. A
file you upload, signed or not, installs only when you press a button on the speaker
(ADR 0048).

### Can I install my own builds?

Yes, without a key. Build SoundTap yourself, open the speaker's Updates page, choose *Other
ways* → *Upload a file* and pick your `.stu`. The speaker checks it and says it is not signed;
choose *Install firmware* and hold a button on the speaker to confirm. Every upload waits for
that press, so nobody else on the network can install one without it
([RELEASING.md](../../RELEASING.md#installing-your-own-builds),
[ADR 0048](../decisions/0048-signed-updates.md)). The USB stick at power-on takes your build too,
renamed `Update.stu`; so far that has worked on the SoundTouch 10 only
([USB stick](install.md#usb-stick)).

### Why does the speaker ask me to hold a button?

Resetting, rebooting, changing Wi-Fi, forgetting a paired Bluetooth
device, installing an uploaded file and reading debug data
only go ahead after someone holds a button on the speaker for two seconds within a minute. Any
button on the speaker itself counts except volume, which keeps working while it waits; a short
press and the remote do not count. A SoundTouch 300 has no buttons, so there you hold a key on
its remote (not volume).

While it waits, the lamps blink (the ST20 display says what the hold will do, such as *Hold a
button to reset*) and the web page shows the same request. If another request comes in
meanwhile, it replaces the first, and the first request's page says so. Everything else, such
as volume, sources and presets, works without a hold.

Updates the speaker fetches itself need no hold: *Update now*, *Update* on a newer app or
SoundTap Core (it asks once, then the speaker updates and restarts by itself; for SoundTap
Core not yet tested on a speaker), and choosing a specific release (an older one included). Every file you upload asks for the hold, with or
without its `.sig`: your own build, Bose's update file, or a release you downloaded. These are
the production rules; on alpha images a device confirmation token can stand in for the hold.
See [ADR 0055](../decisions/0055-household-token-and-windows.md) and
[ADR 0118](../decisions/0118-update-button-without-press.md).
