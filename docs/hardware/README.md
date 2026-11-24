# Hardware

What we know about each speaker, what a release must prove on it, and how a board comes back.
[The contract](../../CONTRIBUTING.md#the-contract) says what may be done to a speaker; these pages hold the facts.

## Per model

| Model | Codename | Page |
| --- | --- | --- |
| SoundTouch 10 | `rhino` / sm2 | [st10](st10/README.md) |
| SoundTouch 20 | `spotty` / scm | [st20](st20/README.md) |
| Wireless Link adapter | `binky` / sm2 | [wla](wla/README.md) |
| SoundTouch 300 | `ginger` / sm2 | [st300](st300/README.md) |

Previews: SoundTap boots them from what the stock packages show; nothing has been seen on one
([ADR 0133](../decisions/0133-more-soundtouch-models-from-stock.md)).

| Model | Codename | Page |
| --- | --- | --- |
| SoundTouch 30 Series I/II | `mojo` / scm | the ST20's board: [st20](st20/README.md) |
| SoundTouch 20/30 Series III | `spotty3`, `mojo3` (stock `spotty`, `mojo`) / sm2 | the ST10's module: [Series III tree](../../os/board/common/dts/ti/omap/am335x-soundtap-st20-iii.dts) |
| SoundTouch Portable | `taigan` / scm | [portable](portable/README.md) |
| SoundTouch SA-5 | `burns` / sm2 | [sa5](sa5/README.md) |
| Lifestyle 600/650 module | `bardeen` / sm2 | [ls650](ls650/README.md) |
| SoundTouch adapter (SA-4, Stereo JC, Wave, CineMate, Lifestyle, VideoWave) | `lisa` / scm, `lisa2` / sm2 | [lisa](lisa/README.md) |

Which features work on which model is in the [compatibility matrix](../owners/compatibility.md).

## Across models

- [Hardware test](hardware-test.md): what a release candidate must prove on each model, and the
  open rows.
- [Install records](install-records.md): what has run on which speaker during install and restore,
  and the developer recovery tools.
- [Infrared contract](st10/remote-control.md#the-infrared-contract): receive only, for the ST10
  and the ST20.
