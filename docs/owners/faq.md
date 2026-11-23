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
[Does the Bose app keep working?](#does-the-bose-soundtouch-app-keep-working)

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
[Bluetooth and AUX](#bluetooth-and-aux) ·
[Stereo pairs](#can-i-pair-two-speakers-as-left-and-right) ·
[Languages](#which-languages) ·
[Bug reports](#how-do-i-report-a-bug) ·
[Crash reports](#can-the-speaker-report-its-own-crashes)

**Security:** [Is it safe on my network?](#is-it-safe-on-my-network) ·
[Can I install my own builds?](#can-i-install-my-own-builds)

---

## The project

### What is SoundTap?

New software for Bose SoundTouch speakers. Not an app, not a server you run next to them:
it replaces the speaker's own software completely, from the first instruction the chip runs
to the web page on your phone. Afterwards the speaker works on its own, on your home
network, with internet radio, Spotify Connect, AirPlay 2, multiroom, MQTT and its
six preset buttons.

### Why does it exist?

Bose ended its cloud service on 6 May 2026, withdrawing service browsing, supported presets
and further firmware updates. Local control remains available in Bose's updated app
([Bose's announcement](https://www.bose.com/soundtouch-end-of-life-row)). SoundTap gives the
speaker a maintained operating system and its own web app, without a SoundTap account or server.

### Is it really built from scratch?

Yes. Neither the repository nor the package holds Bose files
([ADR 0049](../decisions/0049-no-stock-bytes.md)); every layer is ours or open source:

| Layer | SoundTap |
| --- | --- |
| Bootloader | our SPL and U-Boot, built from mainline source |
| Kernel | mainline Linux 6.12 with our own device trees |
| Drivers | ours: audio, keypad, LEDs, infrared, the ST20 display |
| System | built by Buildroot from pinned source |
| Player and web page | `soundtapd` (Go) and a React app, written for SoundTap |
| Updates | our own installer and A/B slots |

Two things the speaker already carries stay as they are ([ADR 0049](../decisions/0049-no-stock-bytes.md)): ST10
and ST20 retain their own stock codec configuration for the original tuning, and the ST20
retains its BCO boot loaders (BFL/BSL) for recovery. Our BCO app includes our own WiLD core;
it does not load a stock radio blob. ST10/WLA Wi-Link firmware comes from upstream
`linux-firmware`, with its provenance recorded.

### How is it different from other SoundTouch projects?

Several good projects keep SoundTouch speakers alive, and they are worth a look. Most of
them either **emulate Bose's cloud** on a computer in your home, or **add a small program on
top of Bose's software**. Both keep Bose's original software on the speaker, which is a
perfectly sensible choice if you want minimal change.

SoundTap takes the other road: a **whole new operating system**. That is more work, but it is
also why SoundTap can add things the speaker never had (AirPlay 2, Snapcast multiroom,
UPnP/DLNA, MQTT, a modern web page), fix things in the system itself, and keep
receiving updates without any Bose code left in the loop.

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
([notes](install.md#soundtouch-300)). The SoundTouch 20 Series II is waiting for hardware
validation, and the Series III and the SoundTouch 30 generations are previews. The [list on the home page](../../README.md#supported-speakers)
says which, and the [compatibility matrix](compatibility.md) says what works on each. All run the
same package. SoundTap is alpha software: "supported" means it runs every day on our own
speakers and a release goes out only after it has passed that model's hardware checklist, not
that every feature is proven on every model. What has run on which speaker is in
the [hardware checklist](../hardware/hardware-test.md#release-checklist-per-model); the [road to 1.0](roadmap.md) has
the details.

### What about the SoundTouch 30, Portable or Wave?

The SoundTouch 30 installs from the same file as a preview, not yet run on hardware.
The Portable and Wave are not supported yet. Other models need someone with the speaker and some
patience. Collecting **read-only** information from a speaker that still runs Bose's software
is the first step, and nothing in it risks the speaker:
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
otherwise it returns to the previous one after a few restarts. SPL/U-Boot and the boot
environment are shared, however: a failed bootloader write is outside that A/B protection.
Below all software, the chip can be started over USB from a computer. That is the safety net, and it is a technical
one: on the SoundTouch 20 its last resort means opening the speaker. **Alpha means: read the
[install guide](install.md) first, and do not start with your only speaker.**

### Can I go back to Bose's software?

Yes. Download Bose's own update file for your model, choose it on the speaker's Updates page,
confirm the return to Bose and hold a button on the speaker. No SoundTap signature is needed
for Bose's file. This way back has run on all four models, though not yet from the release
candidate. On a SoundTouch 20 it does not put Bose's software back on the network processor;
that is still a development step. A USB stick does not take you back: once SoundTap is
installed, the power-on stick gesture installs only a SoundTap package with its signature
(`Update.stu` and `Update.stu.sig`), and refuses Bose's file. See
[Going back to Bose](install.md#going-back-to-bose).

### Does the first install need a USB cable?

No. A speaker on your Wi-Fi downloads SoundTap itself from the update server. If that does not
work, you can upload the package on Bose's own update page (port 17008, in its setup mode) from
the browser of a phone or a computer. A
USB stick can use the stock Volume − and Preset 4 gesture on ST10/ST20; stock Wireless
Link has no USB-stick update gesture. See the per-model routes before choosing recovery. Once SoundTap runs, all later updates
install over Wi-Fi from the speaker's settings page. See the [install guide](install.md).

### Do I need Linux?

No. macOS, Linux and Windows all have the `curl` command the install uses. Building SoundTap
yourself does need a Linux machine with Docker.

### What happens to my Bose presets?

On the first SoundTap start, locally stored TuneIn presets are imported when there is no
SoundTap preset file yet. This depends on the stock `Presets.xml` still being available;
cloud/account-based sources are not imported. This has not yet been checked on hardware. Existing SoundTap presets are kept, and a
factory reset does not import stock presets again. You can search for a station and hold a
preset tile to store it, or choose a Spotify playlist/album or media-server item.

### Does the Bose SoundTouch app keep working?

On stock firmware, Bose's updated app still supports local setup, control and grouping
([Bose's announcement](https://www.bose.com/soundtouch-end-of-life-row)). After installing
SoundTap, use the web page served by the speaker. Bose's app is not a supported SoundTap interface.

---

## Everyday use

### Does it need the internet?

Internet services include radio, Spotify, album artwork, weather, time synchronisation and
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
| Weather (SoundTouch 20 display) | Open-Meteo |
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

Spotify track switches close and reopen the source audio stream so buffered sound from the
previous track cannot return after a skip. Natural endings keep their tail, but fully gapless
transitions between tracks (such as live albums) are not guaranteed. A short response delay
from audio already queued at the output remains, especially in multiroom.

### Does AirPlay work? And Apple Home?

Yes, **AirPlay 2**, from iPhone, iPad and Mac. Add the speaker to the **Home** app as an AirPlay
speaker, group it with HomePods and Apple TV, and ask Siri to play on it. It is not a HomeKit
accessory, and the Home tile's play/pause buttons do not work: Apple reserves those for
certified receivers ([ADR 0003](../decisions/0003-apple-home-via-airplay-no-cast-no-sonos.md)). AirPlay is built for
every supported model, but so far it has been run on hardware only on the Wireless Link, through
Music Assistant; an iPhone, the Home app and Siri are untested on any model. On the SoundTouch 20 it needs our own BCO app (ADR 0031), which the
install puts on it.

### Why no Google Cast, Google Home or Sonos?

Because neither can be done honestly ([ADR 0003](../decisions/0003-apple-home-via-airplay-no-cast-no-sonos.md)).
Google Home and the Sonos app only adopt speakers that hold a certificate signed by Google or
Sonos; there is no open mode, and faking one is not something we would ship. Casting itself
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
SoundTap speakers do not group with speakers that still run Bose's software. On hardware, the
SoundTouch 10, SoundTouch 20 and Wireless Link have each led the other two (2026-11-14).

### Does my remote work?

The remote works as before on the SoundTouch 10, including holding a preset to store
it. On the SoundTouch 20 it is built but not yet tested. Another
infrared remote can be taught key by key ([runbook](../../RUNBOOK.md#teaching-the-st10-an-ir-remote)).

### What do the buttons do?

- **AUX** (the source key): a tap switches between AUX and Bluetooth. Holding it picks
  Bluetooth, as on stock. Bluetooth pairings are cleared on the speaker's web page.
- **Bluetooth** (Wireless Link): a tap picks Bluetooth; holding it opens pairing.
- **POWER**: acts on the press. Holding it does nothing more: stock's low-power standby,
  which turns the network off, is not supported, because the speaker could then no longer be
  reached or woken from the app.
- **Volume −** with **Preset 1** for ten seconds resets the speaker; with **Preset 2** it
  opens the setup network.

### Bluetooth and AUX?

Both are built and available on the models that have them, but neither has been proven end to
end on every model yet. Bluetooth has run on the SoundTouch 10, the Wireless Link and the
SoundTouch 300, AUX on the SoundTouch 10 and 20. The Wireless Link has an AUX in too; its
checklist run is still open, and the rest is untested ([checklist](../hardware/hardware-test.md#release-checklist-per-model)). Of the SoundTouch 20s, only the Series III has Bluetooth; Series I and II do not.

To pair a phone, pick Bluetooth on the speaker: with nothing paired yet it opens pairing by
itself, as the Bose software did. Holding the Bluetooth key, or the button on the web page,
opens pairing for another phone.

### Can I pair two speakers as left and right?

Yes, any two speakers, also of different models; no hardware run is recorded yet. In a speaker's settings, open Stereo pair, pick the partner and the side this speaker plays. The pair is a multiroom group in which each speaker plays one channel ([ADR 0107](../decisions/0107-stereo-pair-acts-as-one-speaker.md)).

### Which languages?

The language picker offers 25 languages, including English, Nederlands, Deutsch, Français,
Español and Afrikaans. The page follows a supported browser language and falls back to English
otherwise. Choose another under *Settings → Language*.
Translations are a great first contribution.

### How do I report a bug?

Open *Settings → System → Support bundle* and press any button on the speaker when the page
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

Only if you ask it to, and only through you. Turn on *Settings → System → Offer crash reports*;
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
SoundTap 0.x on a network you trust; see [SECURITY.md](../../SECURITY.md). Published updates are signed. Production images refuse unsigned updates; alpha images
can explicitly allow unsigned development candidates (ADR 0048).

### Can I install my own builds?

Yes. Build SoundTap yourself, sign the build with an ed25519 key of your own
(`tools/release/sign.sh` with `SOUNDTAP_OWNER_KEY_FILE`, see
[RELEASING.md](../../RELEASING.md#signing-your-own-builds)), and paste the key's public half on
the speaker's Updates page under *Your own signing key*; a button held on the speaker confirms
it. The page then shows the key's fingerprint. From then on that speaker installs what you
sign, from the Updates page and from a USB stick, under the same rules as a SoundTap release:
a production speaker takes a production build, and going back to an older version needs the
downgrade confirmation. Other speakers are unaffected. *Remove the key* on the same page, or a
factory reset, takes it away again; a backup never carries it, and neither a restore nor Home
Assistant can set it ([ADR 0048](../decisions/0048-signed-updates.md#the-owners-own-key)).

### Why does the speaker ask me to hold a button?

Resetting, rebooting, changing Wi-Fi, restoring a backup, forgetting the paired Bluetooth
devices, installing firmware, setting or removing your own signing key and reading debug data
only go ahead after someone holds a button
on the speaker for two seconds within a minute. Any button on the speaker itself counts except
volume, which keeps working while it waits; a short press and the remote do not count. A
SoundTouch 300 has no buttons, so there you hold a key on its remote (not volume).
While it waits, the lamps blink (the ST20 display says what the hold will do, such as *Hold a
button to reset*) and the web page shows the same request. If another request comes in
meanwhile, it takes the place of the first, and the page of the first says so.
Everything else, such as volume, sources and presets, works without it.
The latest stable app update is an exception: *Update now* can install it without a hold.
Choosing a specific release still asks for confirmation, as do installing a newer SoundTap Core
and manual firmware uploads.
These are the production rules; alpha bring-up can use a device confirmation token.
See [ADR 0055](../decisions/0055-household-token-and-windows.md) and
[ADR 0118](../decisions/0118-update-button-without-press.md).
