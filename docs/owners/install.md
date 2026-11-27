# Installing SoundTap

This guide takes a SoundTouch from Bose's software to SoundTap, gets it on your Wi-Fi, keeps
it up to date, and takes it back to Bose if you ever want to. It is written for owners, not
developers; you need a phone or a computer and about twenty minutes. The
[installer page](https://stein155.github.io/soundtap/installer/) walks you through the same
steps one screen at a time, in the same order and in the 25 languages of the speaker's own page;
this guide adds the detail and the troubleshooting.

> [!WARNING]
> **SoundTap is alpha software (0.x).** Installing it replaces everything on the speaker,
> including the part that starts it (the bootloader). There is a way back: the speaker's
> Updates page takes Bose's own update file and puts Bose's software back. Keep that file
> before you start ([step 2](#2-what-you-need)). A speaker that no longer starts at all needs
> a technical rescue. Read
> [Going back to Bose](#going-back-to-bose) **before** you start. If returning to Bose matters
> to you, wait for 1.0, and do not make your only speaker your first test.

| Step | What it is |
| --- | --- |
| [1. Which speaker you have](#1-which-speaker-you-have) | The supported models and the Bose software each needs |
| [2. What you need](#2-what-you-need) | The speaker, a computer or phone, and Bose's own update file for the way back |
| [3. Install automatically](#3-install-automatically) | The easiest route: one line on a computer on the same Wi-Fi; the speaker downloads SoundTap itself |
| [4. Without internet: upload the file](#4-without-internet-upload-the-file) | When the speaker has no internet or step 3 did not work: a phone and the package |
| [5. What happens next](#5-what-happens-next) | Leave it powered; it joins your Wi-Fi and you open `http://soundtap.local` |
| [6. Going back to Bose](#6-going-back-to-bose) | Bose's own update file on the speaker's Updates page |

A USB stick is the last resort when steps 3 and 4 both failed, and is untested: see [USB stick](#usb-stick).

Also in this guide: [Updating](#updating) · [Buttons that always work](#buttons-that-always-work) ·
[SoundTouch 300](#soundtouch-300) · [USB stick](#usb-stick) ·
[When something goes wrong](#when-something-goes-wrong). What has run on which speaker, and
when, is in the [install records](../hardware/install-records.md).

## 1. Which speaker you have

<a id="before-you-start"></a>
<a id="which-bose-software"></a>

Pick your model on the installer page; it then shows that model's setup-mode gesture and what
it shows while it starts.

The installers do not check which Bose software the speaker runs. SoundTap has been installed
from these versions; older ones have simply not been tried. Bose's last release, 27.0.6, works
on every supported model. If an install from older software does not start, update Bose in the
Bose app and try again.

| Model | Oldest Bose software installed from |
| --- | --- |
| SoundTouch Wireless Link adapter | 20.0.6 |
| SoundTouch 10 | 22.0.3 |
| SoundTouch 20 (Series I or II) | 27.0.6 |
| SoundTouch 300 | 27.0.6 |

The SoundTouch 20 Series
II shares the Series I's row and installs the same way, but its hardware validation is still
pending: nobody has recorded a run on one. The command-line installers tell a Series III
SoundTouch 20 by the module its Bose software reports and treat it as a preview (below); in
the browser you pick the model yourself. A model that is neither in the table nor a preview is
not supported, whatever its software.

**Previews.** The package also carries more models, none of them ever run on hardware:
- the SoundTouch 30 (Series I, II and III) and SoundTouch 20 Series III;
- the Portable and the SA-5;
- the SoundTouch adapter of the SA-4, Stereo JC, Wave, CineMate, VideoWave and Lifestyle
  135/235/525/535 (the older adapter only for a Lifestyle: see below).

They are worked out from Bose's own software alone ([compatibility](compatibility.md#previews)).
Their recovery gestures exist in code only and have never been tried, so a preview may have no
working way back. The installers list them as previews and need Bose software 27.0.6:
- the command-line installers include one only when you type its number, then ask you to type
  `preview`;
- the installer page shows them as "(preview)" and needs a second box ticked.

Once confirmed, the installers send the speaker to the preview feed, which names the release's
preview package, `SoundTap-Core-X.Y.Z-preview.stu`: the same system with an installer that also
installs on previews, while the plain package refuses them.

The installers refuse the Lifestyle 600/650 module. The code carries it, but it has no
recovery gesture at all, in Bose's software or SoundTap's. A Lifestyle with the newer (Bluetooth)
adapter reports the same module, and the installers cannot tell the two apart, so they refuse
that adapter too; only a Lifestyle with the older adapter is offered as a preview.

## 2. What you need

- a SoundTouch 10, SoundTouch 20, SoundTouch 300 or Wireless Link adapter, or one of the
  [previews](#1-which-speaker-you-have), still running
  Bose's software (or already running SoundTap: then skip to [Updating](#updating)), with the
  Bose software of [step 1](#which-bose-software) or newer. Other models are listed in the
  [compatibility matrix](compatibility.md#previews);
- the speaker plugged in, with its power cable within reach;
- for [step 3](#3-install-automatically): a computer (macOS, Windows or Linux) on the same Wi-Fi,
  and a speaker that can reach the internet;
- for [step 4](#4-without-internet-upload-the-file): a phone or computer with a web browser;
- for [the way back](#6-going-back-to-bose): Bose's own update file for your model, saved
  before you start (below).

<a id="save-boses-update-file"></a>

### Save Bose's update file for your model

SoundTap does not ship Bose's software, and the way back to Bose needs Bose's own update file
for your model (`Update.stu`). Get it before you install and keep it somewhere safe: without
it, you have no way back to Bose's software. No copy yet? Bose's update files are kept in a public
[archive on archive.org](https://archive.org/download/bose-soundtouch-software-and-firmware/Firmware/), run by neither Bose nor SoundTap: take
the file for your own model.

### Know what happens

Every route ends at Bose's own updater, which installs SoundTap. Once SoundTap runs, every
later update installs from the speaker's own web page: *Update* installs a new release in one
tap, and the speaker restarts into it by itself. A file you upload there waits for one press on
the speaker.

## 3. Install automatically

<a id="route-1-the-installer"></a>
<a id="install-from-the-update-server"></a>

The speaker stays on your Wi-Fi and downloads SoundTap itself. You do not download a package.

1. Open the [installer page](https://stein155.github.io/soundtap/installer/) on a computer on
   the same network: its route "With a computer" shows the command for that computer.
   - **One command.** Paste one line into Terminal (macOS, Linux) or PowerShell (Windows):

     ```sh
     curl -fsSL https://stein155.github.io/soundtap/installer/install.sh | sh   # macOS, Linux
     irm https://stein155.github.io/soundtap/installer/install.ps1 | iex        # Windows
     ```

     It lists every speaker on your network that still runs Bose's software, asks which ones
     get SoundTap and changes only those; nothing is installed on the computer. It skips
     models that are not supported yet, saying so.
   - **In the browser, by IP address** (the route *From this page*). Chrome and Edge ask once
     to reach devices on your local network; Firefox and Safari cannot reach the speaker from a
     web page. Pick the model in step 1, enter the speaker's IP address and click install. The
     page applies the same [table](#which-bose-software), and says so when the speaker never
     starts the install.
2. Go on with [What happens next](#5-what-happens-next).

> [!NOTE]
> The update server answers with the newest published SoundTap Core, an alpha until the first
> release is out.

## 4. Without internet: upload the file

<a id="route-2-upload-to-port-17008"></a>
<a id="fallback-upload-to-port-17008"></a>

Use this when the speaker has no internet or step 3 did not work. A phone with a browser is
enough. The installer page's download button offers the current release's package and shows
its SHA-256 checksum.

### 1. Download and check the package

SoundTap comes as one file for every model, `SoundTap-Core-X.Y.Z.stu`. The speaker picks the
parts that belong to it. A [preview](#1-which-speaker-you-have) takes
`SoundTap-Core-X.Y.Z-preview.stu` from the same release instead, with its own `.sha256`.

1. Open the [newest release](https://github.com/stein155/soundtap/releases) and
   download `SoundTap-Core-X.Y.Z.stu` and `SoundTap-Core-X.Y.Z.stu.sha256`.
2. Check that the download is complete and unchanged:

   ```sh
   sha256sum -c SoundTap-Core-*.stu.sha256          # Linux
   shasum -a 256 -c SoundTap-Core-*.stu.sha256      # macOS
   ```

   On Windows, `certutil -hashfile SoundTap-Core-X.Y.Z.stu SHA256` prints the hash; compare it
   with the one in the `.sha256` file.

> [!NOTE]
> Until the first release (`os-v0.1.0`) is out, the newest package is an alpha: a prerelease,
> signed like a release, that has not yet passed every model's hardware test.

### 2. Put the speaker in setup mode

Bose's software opens its own Wi-Fi network in setup mode, with an upload page on port 17008.
No cable and no computer are needed: a phone with a browser will do.

1. On a SoundTouch 10 or 20, hold **Preset 2 + Volume −** until it opens its setup network
   (Bose's *AP mode*). On a Wireless Link, start setup mode as Bose's manual describes.
2. Join that network (named *Bose …*) with your phone or computer.
3. The speaker is the network's gateway: `192.168.1.1` on a SoundTouch 20 (its network is
   called *Bose SoundTouch Wi-Fi Network*), `192.0.2.1` on the other models.

### 3. Send the package

1. In a browser on that phone or computer, open the upload page: on a SoundTouch 20
   `http://192.168.1.1:17008/update.html`, on the other models
   `http://192.0.2.1:17008/update.html`. If it does not open, use the gateway address your
   phone or computer shows for that network.
2. Bose's update page shows an upload form. Choose `SoundTap-Core-X.Y.Z.stu` and send it.

Uploading takes about two to three minutes; keep the page open until it answers.

A speaker that is already on your Wi-Fi under Bose's software has the same page at
`http://<speaker address>:17008/update.html` (a SoundTouch 20 on 27.0.6 accepted it that way);
then setup mode is not needed.

From a terminal the same upload is one command (on a SoundTouch 20 use `192.168.1.1` instead
of `192.0.2.1`):

```sh
curl -X POST -H 'Expect:' -F 'filename=@SoundTap-Core-X.Y.Z.stu;type=application/octet-stream' http://192.0.2.1:17008/update.html
```

If it answers HTTP 500, do not send the package again straight away: wait until you are sure
the speaker is not still installing.

Then go on with [What happens next](#5-what-happens-next).

<details>
<summary>The same over a USB cable</summary>

If the setup network does not work for you, Bose's updater also listens on the speaker's USB
service port.

| Model | Port to use |
| --- | --- |
| SoundTouch 20 | the micro-USB port on the back labelled **SETUP A** (not the large USB-A port) |
| Wireless Link adapter | its micro-USB port |
| SoundTouch 10 | its micro-USB service port |

Bose's software opens the USB link only after a **cold start with nothing plugged in**, and
the speaker's chip prefers to boot from USB when a computer is attached at power-on. So:

1. **Unplug the USB cable** and **unplug the speaker's power**. Wait ten seconds.
2. **Plug the power back in** and wait about **one minute**, until the speaker has fully started.
3. **Now plug the USB cable** into the speaker and your computer.

The speaker appears as a new network adapter (*SoundTouch System* or *Bose SoundTouch System*).
It gives your computer the address `203.0.113.2` and is itself `203.0.113.1`.

- **macOS:** nothing to do; the adapter comes up by itself.
- **Linux:** the new `usb0`/`enx…` interface gets its address by DHCP. If it does not:
  `sudo ip addr add 203.0.113.2/30 dev <interface> && sudo ip link set <interface> up`.
- **Windows:** the adapter should appear under *Network adapters* as *SoundTouch System*.
  Reports on this route from Windows are welcome.

Check the link: `ping 203.0.113.1` should answer. Then send the package as above, to
`http://203.0.113.1:17008/update.html`.

</details>

## 5. What happens next

**Leave the speaker alone and keep it powered.** Whichever route you took, it takes about four
to six minutes to come back as SoundTap. A low progress number during the download is not a
reason to interrupt it.

It writes SoundTap next to Bose's software, restarts, and comes up as SoundTap. **Do not pull
the power while it is busy**, and do not send the package again. A SoundTouch 20 shows the
SoundTap splash on its display about a second after it restarts, then updates its network chip
and restarts once more, so allow extra time; a SoundTouch 10 sweeps its lights from side to
side while it starts.
If it has not come back after ten minutes (twenty on a SoundTouch 20), keep it powered and see
[When something goes wrong](#when-something-goes-wrong).

The speaker can come back with a different IP address than it had under Bose's software; a
SoundTouch 20 often does, because its network chip is replaced. The installer command and the
installer page look for it on your network by themselves. If you cannot find it, look for the
speaker's name in your router's list of devices, or open `http://soundtap.local`.

### Put the speaker on your Wi-Fi

<a id="4-put-the-speaker-on-your-wi-fi"></a>

After the install the speaker tries to join the Wi-Fi Bose's software was on. That usually
works, but not on every network. A SoundTouch 10 or Wireless Link takes over WPA, WEP or open
networks; a SoundTouch 20 joins 2.4 GHz networks that are open or use WEP, WPA or WPA2. Every
speaker also joins WPA3 networks once you choose them on its setup page. If the speaker does not
show up on your network, use its setup network:

**SoundTouch 10 and Wireless Link.** A speaker without Wi-Fi settings opens its own network,
named after the speaker, for example **`SoundTap ST10 A1B2C3`** (on older builds:
`SoundTap-Setup`). On a SoundTouch 10 the Wi-Fi light turns amber.

1. Join that network with your phone or laptop.
2. The setup page opens by itself. If it does not, browse to **http://192.168.42.1**.
3. Check the speaker's name and tap **Next**.
4. Pick your home network and type its password.

On a SoundTouch 10 the light blinks white while it connects and stays dimly white once it is on
your network. The setup network then disappears.

**SoundTouch 20.** The same as above. Bose's software keeps the network in a different
place on this model, and SoundTap reads it from there on its first start: the speaker comes up
on the cable or on the network Bose's software used, and about two minutes later restarts once
and joins your Wi-Fi by itself, as long as it is a 2.4 GHz network. Taking over the network can
fail on this model;
the speaker then opens its setup network, and you choose your Wi-Fi there once more. Its
temporary setup network is open, as on the SoundTouch 10.

**What comes over from Bose's software.** The speaker keeps its name. Of the presets, only
TuneIn radio stations come over. Presets for Spotify, other music services, or music from your
computer or a media server stay empty: store them again in SoundTap.

### Start playing

<a id="5-start-playing"></a>

Open **http://soundtap.local** on any phone, tablet or computer on the same network. Every
speaker also has its own address, such as `http://soundtap-st10-a1b2c3.local` (its name in
lower case, with dashes). Your router's list of devices shows its IP address under the
speaker's name: a speaker called *Kitchen*, for example, appears as `kitchen` (the name Bose's
software gave it comes over). With more than one speaker, `soundtap.local` reaches one of them;
use the speaker's own address for anything that changes it.

Add the page to your home screen and it behaves like an app; no app store needed. From there:

- **Presets:** search a station, then hold a preset tile to save it. The buttons on the speaker
  play the same six presets.
- **Spotify:** the speaker appears in the Spotify app under *Devices*.
- **AirPlay:** it appears in the AirPlay menu of your iPhone, iPad or Mac.
- **MQTT:** *Settings → Integrations → MQTT*, enter your broker, and the speaker publishes its
  state and takes commands from any MQTT client. Topics are in the [FAQ](faq.md#mqtt).

## 6. Going back to Bose

<a id="going-back-to-bose"></a>

You need Bose's update file for your model, the one you saved
[before you started](#save-boses-update-file).

1. Open the speaker's *Settings → Updates* page.
2. Choose Bose's `Update.stu` on its own (it has no `.sig` file).
3. Confirm the return to Bose, then hold a button on the speaker for two seconds when it asks.
4. Leave it powered while it writes and restarts. It comes back with Bose's software.

What to know first:

- **It is not a factory reset.** Bose's software goes back on the speaker, including the part
  that starts it, but not every byte SoundTap wrote is removed. A file that contains any part
  of SoundTap is refused before anything is written.
- **SoundTouch 20:** this puts Bose's software back on the main processor. The separate
  network chip still runs SoundTap's app at first; Bose's software then puts its own app back
  there by itself when it finds another one (seen on a test speaker). Bose comes back without
  its Wi-Fi network, because Bose kept that on the network chip: set up Wi-Fi again in the Bose
  app. It may also come back without its presets.
- **SoundTouch 300:** the soundbar's own processor keeps the firmware it has, so this is not a
  full soundbar restore either.
- **With a USB stick, also when SoundTap does not start any more:** SoundTap's bootloader has
  a USB stick route built in, but no install from a stick has run on any model yet, so do not
  count on it. It is meant to work like this: put a file on the stick named `Update.stu`, then
  hold **Volume − and Preset 4** (SoundTouch 10, 20) or **both buttons** (Wireless Link) while
  powering on. A SoundTap release's `.stu` puts SoundTap back, any version of it. Bose's own
  update file for your model puts Bose's software back, its bootloader included; on a
  SoundTouch 20 Bose's software then updates the network module itself. Holding the keys is the
  permission, so no `.sig` is needed. Only the gesture itself has been seen, on a SoundTouch 10
  without a stick. The SoundTouch 300 has no proven rescue at all.
- **When the speaker does not start at all:** that needs a developer's rescue over USB from a
  computer, and on a SoundTouch 20 opening the speaker. See the
  [developer recovery tools](../hardware/install-records.md#developer-recovery-tools).

The details behind each of these, and what has been tested, are in the
[install records](../hardware/install-records.md#going-back-to-bose). If you are stuck, open an
[install or recovery issue](https://github.com/stein155/soundtap/issues/new?template=install.yml)
with exactly what you did.

## Updating

SoundTap updates in two parts, each from the speaker's *Settings → Updates* page:

- **SoundTap Core** (`SoundTap-Core-X.Y.Z.stu`): the whole system. It is written into the *other*
  half of the speaker's storage while the current one keeps running. The new version has to
  stay up for a minute before the speaker keeps it (a SoundTouch 20 also waits until its
  network chip runs SoundTap's app, longer when that app is new); if it does not, the
  speaker goes back to the previous version after a few restarts. The check is designed not to
  need your network; that has not been tested on a speaker yet.
- **SoundTap app** (`SoundTap-app-X.Y.Z-armv7.bin`): the player and this web page, without
  touching the system.

When a newer SoundTap Core or app is out, the Updates page says so and shows what is new. One
tap on *Update* is all it takes: the speaker downloads the signed release itself, with no button
on the speaker, writes it into the other half and restarts into it by itself. A page over the
app follows it until it is back. This takes several minutes; keep the speaker plugged in. If
the install fails, the speaker does not restart and keeps running what it had.

To install a file you downloaded, or to go back to an older version, upload it instead.
Every upload waits for you to hold a button on the speaker, with or without its signature.
Every release file comes with a signature (`.sig`): select both together, and the speaker checks
it; a `.sig` that does not match the file or the SoundTap release key is refused. A file
without one, such as your own build, installs too: the page says it is not signed. After the
upload, *Reboot into the new firmware* starts it, with one more hold on the speaker.

<details>
<summary>The same from the command line</summary>

Use the speaker's own address, never the shared `soundtap.local`, so the update reaches the
speaker you mean:

```sh
SPEAKER=soundtap-st10-a1b2c3.local
curl --fail -X POST --data-binary @SoundTap-Core-0.1.0.stu \
     -H 'Content-Type: application/octet-stream' \
     -H 'X-SoundTap-Confirm: install' \
     -H "X-SoundTap-Signature: $(cat SoundTap-Core-0.1.0.stu.sig)" \
     "http://$SPEAKER/api/firmware?install=1" && \
curl --fail -X POST -d '{"confirm":true}' "http://$SPEAKER/api/reboot"
```

Both requests wait for a button held on the speaker. The reboot runs only if the install
request succeeds. If the request fails or is interrupted,
find out why before you retry or reboot. The install answer names the slot it wrote and the one
still running. The package holds everything
the speaker needs: bootloaders that differ from it are written too, and ones that match are
not touched ([ADR 0008](../decisions/0008-one-universal-stu.md)).

</details>

## Buttons that always work

On the SoundTouch 10 and 20 these are built to work without network and without the web page;
they have been seen working on the SoundTouch 10, while the SoundTouch 20's keypad is built but
has no recorded run. Hold both buttons for **ten seconds**:

| Hold | What happens |
| --- | --- |
| **Preset 1 + Volume −** | Factory reset: presets, settings and Wi-Fi are cleared and the setup network comes back. |
| **Preset 2 + Volume −** | Setup mode: opens the setup network so you can move the speaker to another network. It keeps the saved one: hold again, restart the speaker, or leave the setup network unused for 5 minutes to go back. |

Letting go early cancels. These gestures clear user state or change setup mode; they do
not replace firmware. They are not available from the remote. On the Wireless Link, hold
**both buttons for ten seconds** to enter or leave setup mode. Bluetooth pairing is separate
([FAQ](faq.md#bluetooth-and-aux)).

## SoundTouch 300

The SoundTouch 300 takes the same package as the other models and installs the same way. What
has run on it is in the [install records](../hardware/install-records.md#soundtouch-300).

- After the install the bar takes over the Wi-Fi Bose's software was on. If it opens its own
  setup network (`SoundTap ST300 …`) instead, connect it again from there.
- The soundbar's own processor and its firmware are never touched. The bass module, rear
  speakers, HDMI, ADAPTiQ and the remote keep their pairing and settings. The bar has no
  buttons: SoundTap is controlled from the app and the bar's remote.
- When SoundTap starts playing it asks the bar to switch to its input. Starting something on
  the TV while SoundTap plays does not switch back by itself: pick TV in the app or on the remote.
- If the bar does not come back after an install or update, keep it powered and open an
  [install or recovery issue](https://github.com/stein155/soundtap/issues/new?template=install.yml)
  with what you did and how long it has been, before you try anything else.

## USB stick

<a id="route-3-usb-stick"></a>
<a id="fallback-usb-stick"></a>

Use this only when steps 3 and 4 both failed. No install from a stick has run on any model
yet, as a first install or later; only the gesture itself has been seen, on a SoundTouch 10
without a stick. On a SoundTouch 10 or 20:

1. Put the package on a FAT USB stick, named `Update.stu`. No `.sig` is needed: neither Bose's
   software nor SoundTap's own stick route asks for one (see
   [Going back to Bose](#going-back-to-bose)).
2. Plug the stick into the micro-B port through an OTG adapter.
3. Hold **Volume − and Preset 4** while powering on.

The speaker should then install the package and start it; this is untested. The Wireless
Link's Bose software has no such gesture; once SoundTap runs, its bootloader is built to take
**both buttons** instead, also untested.

On the SoundTouch 300 the stick starts from the remote, as Bose's own USB update does: put the
stick in the bar's USB port, hold **SoundTouch** and **CC** on the remote while power comes back.
With Bose's update file for the bar it goes back to Bose; with a SoundTap release it puts
SoundTap back. This is untested on a bar, and if writing its bootloader fails the bar has no
known way back: do not rely on it yet.

## When something goes wrong

| What you see | What to do |
| --- | --- |
| `curl` answers `500` | Sometimes a first attempt is refused, but it can be a real failure. Make sure no install is running before you try again. |
| `curl` cannot connect at all | Check you are on the speaker's setup network and use its gateway address. Over USB: the link is only offered after a cold start with no USB attached. |
| No new network adapter appears (USB) | Use a data cable, not a charge-only one. Power first, USB after one minute. |
| The speaker does not come back after installing | Keep it powered: it may still be writing, and a SoundTouch 20 also updates its network chip. Note the model, the route, how long it has been and the last answer you saw, and open an [install or recovery issue](https://github.com/stein155/soundtap/issues/new?template=install.yml). |
| The speaker's network does not show up | On a SoundTouch 10, hold **Preset 2 + Volume −** for ten seconds. |
| The setup page does not open | Browse to `http://192.168.42.1` yourself. |
| `soundtap.local` does not open | Use the IP address from your router. Some Android phones do not resolve `.local` names. |
| An update is refused | Select the `.sig` file together with the package, or pick the `.stu` alone and hold a button on the speaker when it asks. |
| Anything else | *Settings → Maintenance → Help and support → Support bundle* downloads the diagnostics; attach it to an [issue](https://github.com/stein155/soundtap/issues/new). |

## Where to read next

The [FAQ](faq.md) answers the everyday questions, the [compatibility matrix](compatibility.md)
says what works on which model, the [install records](../hardware/install-records.md) say what has run on
which speaker, the [runbook](../../RUNBOOK.md) covers teaching a remote, Apple Home
and Snapcast clients, and the [road to 1.0](roadmap.md) lists what is still open.
