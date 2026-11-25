# Road to 1.0

> [!NOTE]
> This is the pre-1.0 tracking page. It is removed after the 0.1.0 release; from then on the
> [changelog](../../CHANGELOG.md) says what each release brings.

SoundTap starts again at 0.1.0 ([ADR 0035](../decisions/0035-releases.md)). This page lists what
stands between today's alpha and a first release a stranger can install on their own speaker. A
ticked item names its evidence: the date and the tested artifact (version and SHA-256) of the
result. A design choice is closed by an ADR, not by this page.

Status on 2026-11-24. The models are the ST10, the ST20, the Wireless Link and the
SoundTouch 300: the ST300 ships in the first release (project decision 2026-11-12), the signing
gate requires its hardware report since 2026-11-15, and since 2026-11-20 it has full support
like the other three ([install records](../hardware/install-records.md#soundtouch-300)). The
package also carries previews for other SoundTouch models, worked out from Bose's software and
never run on hardware ([ADR 0133](../decisions/0133-more-soundtouch-models-from-stock.md)); a
preview becomes a supported model once an owner has run its hardware checklist.

## Blocks the first release

### Release route

- [ ] One `build.yml` candidate from `main`, installed unchanged on the ST10, the Wireless Link,
      the ST20 and the ST300, and published by `release.yml` as `os-v0.1.0` and `app-v0.1.0`
      ([RELEASING.md](../../RELEASING.md)). So far: a build of `main`, built on the build host
      because Actions are off, installed unchanged through `/api/firmware` on the ST10, the
      Wireless Link and the ST20 on 2026-11-10. Since 2026-11-23 `build.yml` builds `main` on
      the public repository and `release.yml` published 0.1.0-alpha.1 to 0.1.0-alpha.3 from it.
      The alpha.3 Core STU (SHA-256
      `dab9664708ee1dca8e6ffa46692caf688def568f28bb51ac326dde65911a7f76`) installed unchanged on
      the SoundTouch 300 on 2026-11-24; the ST10, the Wireless Link and the ST20 run alpha-profile
      builds of 0.1.0-alpha.3. Still open: `os-v0.1.0` and `app-v0.1.0` from one candidate on all four.
- [ ] The hardware test ([hardware-test.md](../hardware/hardware-test.md)) on each model from that candidate,
      kept as one checklist per model with the tested artifact's version, SHA-256 and date of each row.
      The ST20 part runs without audio. The signing gate requires all four reports, `hardware-st300.json`
      since 2026-11-15.
- [x] **Signed updates.** Published Core STUs and app binaries are signed; a release the speaker fetches updates after one confirmation in the app, and every upload waits for the press on the speaker
      ([ADR 0048](../decisions/0048-signed-updates.md)). `release.yml` publishes a draft
      without the key; `sign.yml`, the one job with the release key, in a GitHub environment
      limited to `main`, signs it, and after the maintainer's bench speakers took it publishes it
      ([ADR 0035](../decisions/0035-releases.md)). For an alpha `release.yml` calls `sign.yml`
      itself: 0.1.0-alpha.2 and 0.1.0-alpha.3 were signed that way on 2026-11-23.

### Security

- [x] **No SSH.** Project decision 2026-11-11, recorded in
      [ADR 0119](../decisions/0119-no-ssh-in-production.md): production images carry no SSH server
      and no root login, and nothing replaces it; support goes through the web UI's support
      bundle (*Settings → Maintenance → Help and support*), and recovery is the USB update and the web UI. Release
      images are production images; alpha images keep root SSH without a password. Done: `os/configs/production.fragment` drops Dropbear and
      locks root; `rootfs_check.py --profile production` rejects any SSH server, client, key
      tool, host key, `authorized_keys`, avahi SSH advert, script that sets one up and filter
      rule for port 22; owner docs no longer tell anyone to ssh into the speaker, and
      `tools/hil/` says it needs an alpha image. A production build of `main` on the build host,
      2026-11-12, passed that check on the build tree and again on the rootfs unpacked from the
      package: no file named after SSH or Dropbear, root's password field `*`, no avahi
      service advert, nothing on 22 in the input filter, and its soundtapd from that rootfs
      (qemu-user, no init) listens on TCP 80 only. Installed on the Wireless Link: production
      build of `main`, 2026-11-14, scanned on every TCP port from the home network and from a
      client on the setup network: nothing on 22; open are 80, 7000 and 41547 (librespot).
- [x] **Security audit** of a production image. The threat model
      ([ADR 0113](../decisions/0113-threat-model.md)); a route classification test
      (`app/internal/web/route_access_test.go`); a production build of `main` on the
      Wireless Link, 2026-11-14, probed and port scanned from the home network and from a
      client on the setup network, with only the listeners ADR 0113 accepts. The USB shell is
      absent by build; it was not measured from the cable end on a production image. The last
      gap, clearing Bluetooth pairings from the LAN, waits for a press on the speaker. Audit closed
      2026-11-15.
- [x] **Public home.** The repository is public under GPL-3.0. The scan of 2026-11-09 found
      the current tree clean and the history not; project decision 2026-11-10: a fresh public
      history. `main` was rebuilt without stock paths on 2026-11-15 and published as
      [stein155/soundtap](https://github.com/stein155/soundtap) on 2026-11-23.
- [x] **Authenticated changes.** The speaker trusts its home network; factory reset, restore,
      reboot, Wi-Fi changes, every firmware or app upload and debug routes wait for a key held on
      the speaker (on a SoundTouch 300, which has no buttons, on its remote); every signed release the speaker fetches itself is press-free ([ADR 0118](../decisions/0118-update-button-without-press.md))
      ([ADR 0055](../decisions/0055-household-token-and-windows.md)).
- [x] **Dependency audit in CI** for Go and npm: `make vuln` runs `govulncheck` on soundtapd and
      `npm audit --omit=dev --audit-level=high` (only what ships in the web UI; build tools such as
      vite and eslint never reach a speaker), as CI's `vuln` job on every pull request and push to `main`. It runs in Actions
      since 2026-11-23; that day it caught GO-2026-6278 in `gorilla/websocket`, fixed by the bump
      in 0.1.0-alpha.3.

### Install and recovery

- [ ] **First install from Bose's software** through the update feed, started from the browser
      installer or the app:
  - Wireless Link: 2026-11-06 from Bose 27.0.6 with a LAN feed; 2026-11-11 again,
    started from the browser installer in Chrome ([install records](../hardware/install-records.md#install-records-per-model)).
  - ST20: 2026-11-11, from Bose 27.0.6, `/api/version` after 336 s.
  - ST10: 2026-11-12, from Bose 27.0.6, `/api/version` after 262 s; 2026-11-14 from Bose
    22.0.3, after 259 s.
  - SoundTouch 300: 2026-11-15, started from the browser installer.
  All four outside a candidate; to repeat from it.
- [ ] **Wi-Fi taken over from Bose's software** on that first install:
  - Wireless Link: 2026-11-11: came back on the home network at the
    same address after 259 s, without the setup network.
  - ST20: 2026-11-11: network read from the Bose radio module,
    back on the home network after the reboot, without the setup network.
  - ST10: 2026-11-12, with SoundTap's own Wi-Fi files moved aside first: back on
    the home network at the same address, files rewritten identical. Its setup network ran for
    about 8 s before the profile was taken over; the reboot after came up without it.
    2026-11-14 from Bose 22.0.3: taken over as well.
  - SoundTouch 300: 2026-11-15, taken over from Bose's software.
- [ ] **USB stick recovery** (Volume Down + Preset 4, or both buttons on the Wireless Link, at
      power-on) installs the stick's `Update.stu` with settings kept, on each model: a SoundTap
      package, and Bose's file back to stock with its bootloaders; no `.sig` (ADR 0130).
      So far only the trigger is proven, without a stick: the ST10 on 2026-11-08
      (`soundtap.recovery=-19`). The SoundTouch 300 has no proven trigger. An install from a stick, and the refusals, are still
      to run on every model.
- [ ] **Back to Bose's software and back again** on each model from the candidate.
      Done outside a candidate on every model: the Wireless Link and the ST20 on 2026-11-11,
      the SoundTouch 300 on 2026-11-15 and the ST10 on 2026-11-17 went to Bose 27.0.6 and
      returned through the update feed, the ST10 with name, presets, volume and Wi-Fi kept.
- [ ] **Boot and Wi-Fi reliability** from the candidate: three cold boots and three warm
      reboots, the first warm reboot after repeated installs (it used to stall on the ST10 and
      the Wireless Link), Wi-Fi setup, a wrong password and a reconnect, per model.
      Outside a candidate: 20 warm reboots and 5 installs on each of the four models
      without a failure (2026-11-13); `tools/hil/boot-soak.sh` with 5 installs, each followed
      by the first warm reboot, on the Wireless Link, the ST10 and the ST20 (2026-11-17, no
      stall before init); three cold boots on the ST10 (2026-11-18) and the ST20 (2026-11-15);
      a wrong password, an access point off for 30, 120 and 300 s, and the join through the
      setup network on the Wireless Link and the ST10 (2026-11-18). Still open: cold boots on
      the Wireless Link, the ST20 on the same candidate and the SoundTouch 300, and the whole
      set from the candidate.
      On 2026-11-23 the ST10's first boot after an install of 0.1.0-alpha.2 never reached
      init; U-Boot's next try of the same slot booted (boot count 2, 145 s in all). Where the
      first try stopped needs the serial console.
- [x] The bootloader write must not cross into the spare U-Boot when it skips bad blocks.
      Each U-Boot copy is written only into its own half of `mtd4`: `main`,
      2026-11-12, with a test that puts bad blocks in the primary half.
- [ ] Rollback after a crash loop from the release candidate on ST20. Historical
      pass: 2026-11-12, three unconfirmed boots and retained fallback after
      reboot (run), at `bootlimit=3`; the limit is 4 since 2026-11-20
      ([ADR 0010](../decisions/0010-ab-slots-boot-confirm-rollback.md)).
- [ ] **Bluetooth** proven end to end on each model that has it. Decided 2026-11-12:
      tested with the Pi's own Bluetooth controller as the phone. Of the ST20s only the Series
      III has Bluetooth, so the shared kernel keeps it. So far: pairing from the web UI, 10 minutes of play
      without underruns, link loss and return, wake on Bluetooth and forgetting on the
      Wireless Link and the ST10 (2026-11-12 to 2026-11-20); an iPhone playing on the
      SoundTouch 300 (2026-11-17). Still open: title and volume from a real phone, a second
      phone, and the Wireless Link and the bar from the candidate.
- [x] **ST20 amplifier pop** when a multiroom follower starts. Decided 2026-11-12: blocks
      the release, and is proven by the order in the log, without a listening test. A follower
      now starts in a local source's order: measured on 2026-11-13
      on the ST10 following the Wireless Link, and on 2026-11-17
      on the ST20 following the Wireless Link, from standby (amp unmuted 3.06 s after power)
      and from a playing preset (no amp mute line). The join chime made `source sound reaches
      the DAC` fire during the hold; it is now logged once the hold ends.
- [ ] **Bootloader findings from the alpha review** (2026-11-23), each a bootloader or
      first-install change that is RAM-booted and run on a speaker before it ships:
  - Done: our U-Boot set `board_name`, `board_rev`, `board_serial` and `serial#` from TI
    EEPROM data that nothing initialises on these boards and saved that SRAM with the boot
    count (an ST10, 2026-11-23). soundtapd reads such values since 0.1.0-alpha.2, and U-Boot
    no longer sets them since 0.1.0-alpha.3 (booted on the Wireless Link, 2026-11-23).
  - The USB-stick recovery picked its target slot from `bootcmd` alone. Changed in the code
    2026-11-24, still to RAM-boot and run on a speaker: it takes the slot `altbootcmd` names
    once the coming boot passes `bootlimit`, points `bootcmd` and `altbootcmd` at the booting
    slot (saved) before it erases the other, and resets when its final `env_save` fails
    (`os/tests/boot/test_uboot_usb_recovery.py`).
  - The first-install module treated an unreadable module type as a match, and mapped the
    Preview models onto the ST10's and ST20's boot chains. Changed in the code 2026-11-24: an
    unreadable module type is refused, and a Preview board installs only from the preview
    package, whose installer is built for it (ADR 0133); still to run from a candidate.
- [x] **AUX latency**: about 0.19 s since 2026-11-12, with a clean 10-minute run on
      the ST10 and the ST20; stock is about 30 ms. Project decision 2026-11-12: about 190 ms
      is acceptable for the first release.

## Wanted before the first release

- [ ] First install from Bose software older than 27.0.6. The Wi-Fi key search was
      checked offline against every stock release on 2026-11-11: found from 9.0.41 on; older
      Series I software has none and falls back to the setup network. The 9.0.41 finding
      concerns only that NetManager Wi-Fi key search, not the install. The installers' minimums
      stay the oldest recorded install per model: Wireless Link 20.0.6, ST10 22.0.3, ST20 and
      SoundTouch 300 27.0.6 ([versions.json](../installer/versions.json)).
- [ ] Tests that fail by timing under load. On 2026-11-24 the Go suite ran three times on one
      CPU at low priority: the only failures were four tests that kept process state between
      runs (`bcoflash`, `multiroom`, `supervise`), now fixed. Still to do: the same run on the
      CI runner, and the Python suite under load.
- [x] Executed tests for the init script and the installer instead of text checks. The init
      script, the installer and the boot chain's checkers run against fakes; the
      [test map](../contributors/testing/README.md) says why each remaining text check stays one.
- [ ] User docs claim no more than the hardware evidence shows. Owner docs checked against the
      checklist and the install records on 2026-11-24 and reworded. Where the two disagreed
      (ST300 Spotify and Bluetooth playback, Wireless Link AUX), the owner's report of
      2026-11-24 settled it in the checklist.
- [ ] **Spotify presets** are slow to start. The timing logs from an earlier fix say where the time
      goes; measure on the Wireless Link and fix.
- [ ] **Multiroom sync** ([ADR 0013](../decisions/0013-multiroom-through-snapcast.md)): followers
      within about 20 ms of the leader across all three models, also after an underrun.
      Measured 2026-11-14: the modelled chain is the same whichever model leads, and
      every follower reports in sync within a few windows; there is no capture path to measure
      the sound itself. By ear: with the ST20 leading, the ST10 trailed (2026-11-13); after the
      fixes to the leader's delay and the tap's pipe, the ST20 and the SoundTouch 300 play in
      step (owner, 2026-11-22).
- [x] **Multiroom leader CPU**: measured again on 2026-11-14 (table below). The
      ST10 as leader is down from 76% to 67%; every model has headroom as leader, so any model
      can lead.
- [x] **Known CVEs in the OS packages.** `make os-cves` asks NVD about each package in the
      SBOM, no mirror needed (it replaces `make sbom-cves`). Its first run, on 2026-11-12
      against the SBOM of the build host's build tree (Buildroot 2026.05.3), found 206 open CVEs scoring 7.0 or more: 165 in the
      kernel 6.12.113, the rest in ffmpeg, expat, OpenSSL, libplist, GLib and PCRE2. Each one gets
      fixed by a newer point release or a patch, or goes into `<PKG>_IGNORE_CVES` with the reason.
      Triaged: kernel 6.12.117, expat 2.10.0, OpenSSL 3.6.5, PCRE2 10.49, the
      GLib fixes patched in, the rest in `os/ignore-cves.mk`. ffmpeg subsequently moved to the
      shared 9.0.2 package ([ADR 0041](../decisions/0041-radio-one-ffmpeg-one-url-rule.md)). The
      accepted nf_tables exception is recorded in `os/ignore-cves.mk`. On 2026-11-14,
      `main` with its license-hash fixes had no open CVE scoring 7.0 or
      more. That is not an audit of the candidate: re-run the SBOM audit for release. The
      release route's first run on the production profile (Buildroot 2025.02.19) found
      five more: two kernel CVEs in code our kernel does not build (F2FS, Arm FF-A), and three
      in the LTS's GLib 2.82.5, two of them now patched and one in GLib's Windows-only spawn
      code. On 2026-11-22 a production SBOM of `main` (Buildroot 2025.02.19) had no open CVE
      scoring 7.0 or more, or not yet scored. The release runs `make os-cves` again on the
      candidate.

## After the first release

- [x] **Standby CPU**: idle speakers are 11% (Wireless Link) to
      12% (ST10, ST20) busy. soundtapd itself is 4% to 6% of that. Measured 2026-11-14 with
      development builds of 2026-11-13 and 2026-11-14, 30 s per phase, all three at
      once, idle against wall clock; see "Measuring CPU" in [hardware-test.md](../hardware/hardware-test.md):

      | Phase | ST10 busy / soundtapd | ST20 busy / soundtapd | Wireless Link busy / soundtapd |
      |---|---|---|---|
      | Standby | 12% / 6% | 12% / 6% | 11% / 4% |
      | TuneIn, each on its own | 36% / 16% | 38% / 20% | 40% / 19% |
      | ST10 leads the other two | 67% / 20% | 36% / 21% | 40% / 18% |
      | ST20 leads the other two | 41% / 17% | 61% / 25% | 42% / 18% |
      | Wireless Link leads the other two | 39% / 17% | 35% / 21% | 77% / 26% |

- [x] **API stability tooling** ([ADR 0108](../decisions/0108-http-api-compatibility-for-1-0.md)):
      `openapi.json` lists the internal routes in `x-soundtap-internal`; `GET /api/version` lists
      `capabilities`; `TestAPIContract` compares the API with the level's recorded contract
      (`app/internal/web/testdata/api-contract-1.json`) and fails a removed or retyped path or
      field without a raised `web.APILevel`. Level 1 is re-recorded until 0.1.0.
- [x] Every blob in the image has recorded upstream provenance, checked in CI:
      `check_provenance.py --rootfs` judges every binary file of the image but ELF, as part of
      `make os` (`image-check`); what the build makes itself is covered by generated entries.
      Checked against the rootfs of a build of 2026-11-22.
- [ ] PR CI under 15 minutes; a cold `build.yml` under an hour on a GitHub-hosted runner, 30
      minutes as the goal. So far: a build without any cache took about 142 minutes on a hosted
      runner (2026-10-07); on the build host a cold image build that also built gcc took 53
      minutes (2026-11-17), and a warm tree takes 8 to 13 minutes. On the public repository
      since 2026-11-23: CI takes 6 to 13 minutes per push to `main`, and `build.yml` 29 to 82
      minutes with its caches. Still to see: a cold `build.yml` there, and the weekly
      reproducibility check ([ADR 0011](../decisions/0011-one-buildroot-pipeline.md)), which has
      not run yet.
- [ ] One README per audience: the root README for owners, `docs/contributors/design.md` for developers,
      and nothing in between that repeats them.

## Done

- [x] **AUX in** on the ST10 and the Wireless Link, captured through the mixer since the mixer
      capture landed. ST10, 2026-11-08: the Wireless Link's line out into the ST10's AUX in; AUX owned the
      output, 0 underruns. Wireless Link, 2026-11-09: a loopback cable played 30 minutes with no
      underrun.
- [x] **ST10 IR remote** after the kernel media trim; the ST20's remote works too
      (2026-10-10).
- [x] **ST20 codec configuration**: project decision 2026-10-21, the speaker's own codec script is
      read from its stock slot at boot so the sound stays the same; it is not in the repository
      or the package ([ADR 0012](../decisions/0012-single-dac-writer.md)).
- [x] The BCO question: SoundTap runs its own BCO app on the ST20 ([compatibility](compatibility.md)).
- [x] Release notes generated from the commits since the previous tag.
- [x] An owner's guide: install, set up, update, go back to stock, report a bug
      ([install.md](install.md), [faq.md](faq.md)); whether it claims too much is still open.

