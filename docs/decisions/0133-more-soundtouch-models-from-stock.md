# 0133. More SoundTouch models, worked out from the stock packages alone

Status: accepted — 2026-11-24

## Context

Bose's update index (27.0.6) names 26 SoundTouch device ids. Behind them are two modules, the
SCM and the SM2 (both AM335x), and twelve boards. Every stock package carries the same kernel,
MLO and U-Boot (byte for byte in the generic sm2, scm, lisa.sm2, burns.sm2, triode.scm and
marconiprod.scm packages), and every stock rootfs holds all twelve board trees. Nobody on the
project owns the boards below; what is known comes from the stock packages, recorded in
[`os/stock/PROVENANCE.md`](../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

## Decision

SoundTap boots more boards as previews, from what stock does on them:

| Variant | Product | Runs as | What is new |
| --- | --- | --- | --- |
| `mojo` | SoundTouch 30 Series I/II | st20 | U-Boot detection only (the board was already there) |
| `spotty3`, `mojo3` | SoundTouch 20/30 Series III | st10 | U-Boot detection, the rear Ethernet port (stock wires it as on the ST300) |
| `taigan` | SoundTouch Portable | st20 | Its tree, a battery monitor ([portable](../hardware/portable/README.md)) |
| `burns` | SoundTouch SA-5 | st10 | Its tree, the amplifier controller ([sa5](../hardware/sa5/README.md)) |
| `bardeen` | Lifestyle 600/650 module | st300 | The ST300's tree and processor link ([ls650](../hardware/ls650/README.md)) |
| `lisa`, `lisa2` | SoundTouch adapter (SA-4, Stereo JC, Wave, CineMate, Lifestyle, VideoWave) | st20 (scm), st10 (sm2) | Their trees and the Bose link master ([lisa](../hardware/lisa/README.md)) |

**Our U-Boot tells the boards apart as stock's does.** On the SCM, the variant resistor on
ADC channel 1 (`variant_raw`, decimal): 100–1024 lisa, 1025–2046 spotty, 2047–2867 mojo,
2868–3481 taigan. On the SM2, the product name at 0x170 of `NAND.Manufacturing-Data`. The
rows live in `os/models.json` (`detect`) and in `rt_boards`.

- `rt_b` is the board, and `rt_dtb` and `rt_kernel` are its own files. The slot recipes
  `rt_set_recipes` writes, and the USB-stick recovery, go by those.
- `rt_v` stays the stem the default bootcmd builds file names from (rhino, spotty, binky,
  ginger). That bootcmd is compiled into the SPL's default environment, and keeping its text
  keeps the SPL byte for byte what it was: no install writes the SPL copies because of this
  change (checked by building both).
- A torn environment on a preview board therefore boots its model's own tree until the
  recovery or soundtapd writes the board's recipes, as before it had a row.
- A count or name no row knows falls back as before: spotty on the SCM, and rhino or binky by
  `variant_raw` on the SM2.

**Writes are bounded by what stock writes.**
- The Portable's monitor drives the charge enable by stock's rules. It follows stock's battery
  protection by default: it sends the pack to SHIP on a pack fault, on DC above 13.000 V for one
  second, and on a critically discharged pack (1 % or less) on battery. There is no opt-in
  file. The SHIP request is the only gauge register it writes. It reads stock's
  `/mnt/nv/charger_active_level` for the charge enable's level and never writes it.
- The SA-5's controller is written at two registers only (0x06, 0x11). Its bootloader
  address 0x1e and the update flag are never touched.

**The SoundTouch adapter** is the master of the Bose link (ABL) to its host, in soundtapd and
not in a kernel line discipline as on stock. That was the smaller step: a userspace master at
19200 baud, with every timing stock's module uses, owns the bus. A kernel driver would only be
needed if userspace timing proves too loose on hardware.
- It announces itself without the speed flags, so a host keeps the link at 19200.
- Stock calls the adapter `lisa` on both modules; SoundTap's SM2 adapter is `lisa2`, as the
  Series III are spotty3 and mojo3.

**Previews install from a preview package of their own.** Every Core release carries two
packages, built together by `os/installer/build-universal-stu.sh`:
- `SoundTap-Core-X.Y.Z.stu`, the universal package. Its installer (module 0) refuses a
  Preview board before it opens NAND.
- `SoundTap-Core-X.Y.Z-preview.stu`, the preview package: the same package with module 0
  swapped for the installer built with `-DSOUNDTAP_PREVIEW_PACKAGE`
  (`WLA_PREVIEW=1 os/installer/module/build.sh`). Only that installer installs on a Preview
  board; on every other model it installs as the main one does.

The two differ in module 0 alone: `stu_check.py same-payloads` requires every byte from module
1's header to the end to be the main package's. The build, `release.yml` and `sign-release.sh`
run that check, so the hardware reports a release is signed against cover the preview
package's payloads too. The installer that differs is not covered by them.

- **Signature.** The preview package has its own `.sig`, the statement of
  [ADR 0048](0048-signed-updates.md) with the same kind, profile and version over its own
  digest. `sign-release.sh` signs it with the main asset and `--publish` requires both
  statements; the `.sig` format is unchanged.
- **Feed.** `updates/soundtouch-preview` names it, in the shape of `updates/soundtouch`, for
  the preview device ids of Bose's index only:
  - ST30 Series I/II 0x0924, ST20 Series III 0x093B, ST30 Series III 0x093C, Portable 0x0925,
    SA-5 0x093A;
  - the adapter on the SCM (0x0932-0x0936, 0x0938, 0x0944, 0x0945) and on the SM2 (0x093D,
    0x093E, 0x0940-0x0942, 0x0946).
  - The SM2 Lifestyle ids (0x093F, 0x0947), the Lifestyle 600/650 module (0x0948) and 0x094B
    are in no feed. `updates/soundtouch` lists no preview id; the ST20 Series III left it.
  - `tools/release/feed_check.py --preview` holds the preview feed to the release's preview
    package as the supported feed is held to the main one, and refuses either feed listing a
    device of the other.
- **Installers.** `install.sh`, `install.ps1` and the browser installer send a speaker to the
  preview feed only when its verdict is preview and its owner confirmed it; every other
  speaker gets `updates/soundtouch`.
- **soundtapd is not involved.** Its update routes install the payloads and never run module
  0, so the main and preview packages install alike on a speaker already on SoundTap.

Alternatives that lost:
- One package for every model, the preview boards included: a stock speaker would install a
  preview through the same feed and file as a supported model, with nothing in the package
  that marks the choice.
- Waiting for owners of each board: nothing in the stock packages is missing to start with.
- Asking the stock kernel for the variant at runtime: our U-Boot must name the tree before any
  Linux runs.

**The trees were checked pad for pad against stock's.** Each new tree, compiled, was compared
with its stock DTB: every pinmux pad (offset, mode, pull) and every GPIO hog against stock's
gpiodev initial value. The same comparison on the trees already proven on hardware gives the
baseline:
- The ST20 and ST300 match stock exactly.
- The ST10 differs in its deliberate IR pins and in the pads it does not use.

The new trees differ from stock only in those same places. Ethernet, UART0 (the adapter), I2C0
(the Portable), the STM32 and McASP1 pads (the SA-5) and the USB mux lines are stock's.

## Consequences

- Every new board is a preview. Nothing here is a hardware observation, and none of it may be
  marked Observed or shipped without that board's column of the checklist.
- **Recovery exists only in code.** The stick and button gestures below are implemented in our
  U-Boot and have never run on any of these boards. An implemented route is not a proven one
  ([the contract](../../CONTRIBUTING.md#recovery-invariant)): nothing in the documentation may present them as a way
  back until each has run on that model.
  - SCM boards and the Series III take the keypad chord (Volume Down + Preset 4) in our
    U-Boot.
  - The SA-5 and the adapter take their stock gesture, their one button at power-on
    (gpio0_27 on the SM2, gpio0_29 on the SCM).
  - The Lifestyle 600/650 module has no recovery gesture, in stock or in ours. The code still
    carries the board, but the installers and the STU installer refuse it: its way back must be
    proven before any write. An SM2 Lifestyle adapter (`lisa2`) reports what the installers
    cannot tell from the module, so they refuse it too; on a Lifestyle only the SCM adapter is
    offered.
- **The installers offer a preview only with explicit confirmation**: the owner picks it by
  name or number and confirms it as a preview (typing `preview`, or ticking the page's second
  box). No installer offers a preview by itself. A confirmed preview is sent to the preview
  feed; the plain package's installer refuses a Preview board even when it reaches one.
- **The Portable follows stock's battery protection by default**: SHIP on a critically
  discharged pack, a pack fault or overvoltage on DC, as stock does. Leaving the pack to drain
  or a faulty pack charging would be the larger risk.
- Things we must not do:
  - SHIP the Portable's pack on anything but stock's three conditions, or write any other gauge
    register.
  - Write any SA-5 controller register other than 0x06 and 0x11, or talk to 0x1e.
  - Send a Lifestyle console op 0x08, 0x0c or 0x8c=1, or any of its STP/ASOC updates.
  - Build an ABL repository (port 16) or staging message.

## Code

- `os/models.json` (`detect`), `os/support/models_gen.py`.
- `os/bootloader/uboot/uboot-2026.07-scm-nand-wp.patch`: `rt_boards`, `rt_scm_variant`, `rt_mfg_variant`; `uboot-2026.07-usb-recovery.patch`: `rt_set_recipes`, `rt_usb_recovery`.
- `os/board/common/dts/ti/omap/am335x-soundtap-portable.dts`, `am335x-soundtap-sa5.dts`, `am335x-soundtap-st20-iii.dts`, `am335x-soundtap-lisa.dts`, `am335x-soundtap-lisa-scm.dts`.
- `app/internal/hw/battery`, `app/internal/hw/stm32amp`, `app/internal/abl`; `app/cmd/soundtapd/battery.go`, `sa5.go`, `adapter.go`.
- `os/installer/module/installer.c` (`model_boots`, `board_variant`, `preview_board`), `os/overlay/etc/init.d/soundtap`.
- The preview package: `os/installer/module/build.sh` (`WLA_PREVIEW`), `os/installer/build-universal-stu.sh`, `os/support/stu_check.py` (`same-payloads`), `tools/ci/run-build.sh`, `.github/workflows/release.yml`, `tools/release/sign-release.sh`, `tools/release/notes.py`.
- The preview feed: `tools/release/mock_update_server.py` (`PREVIEW_MODELS`), `tools/release/feed_check.py`, `.github/workflows/feed.yml`, `docs/installer/install.sh`, `docs/installer/install.ps1`, `docs/installer/index.html`.
