# 0047. WiLink8 firmware from linux-firmware, board configuration from wlconf

Status: accepted — 2026-10-21

## Context

Every model's image needs the WiLink8 (WL1835MOD) radio firmware, the Bluetooth init script and the
radio's board configuration `wl18xx-conf.bin`. None of them is code we compile, and a file lifted
from a Bose image may not ship ([ADR 0049](0049-no-stock-bytes.md)). A binary nobody here compiles
comes from upstream with its source recorded.

The board configuration is not code either: it is the module's board values (front-end types,
antennas, trace loss, interrupt coalescing) and the per-region TX power limits, in the binary layout
TI's `wlconf` reads and writes. Decoded with `wlconf -i <bin> -g` and compared with TI's
`wl18xx-conf-default.bin` from 18xx-ti-utils R8.8, the values the speakers run differ from TI's
default in 17 elements shared by all regions and 4 power-limit elements per region; KR and US are
identical.

## Decision

### Radio firmware and Bluetooth init script from upstream

`BR2_PACKAGE_LINUX_FIRMWARE_TI_WL18XX` supplies `wl18xx-fw-4.bin`. Post-build removes the older
revisions and the WiLink6/7 files that package brings, and fails unless `wl18xx-fw-4.bin` has the
upstream digest. The Bluetooth init script `TIInit_11.8.32.bts`, which linux-firmware does not
carry, comes from TI's own repository and is committed under `os/upstream/ti-connectivity/` with
its provenance.

### Board configuration regenerated from text

`os/package/soundtap-wl18xx-conf` commits the differences from TI's default as text: `board.conf`
(the 17 board elements, commented) and `DF.conf`, `JP.conf`, `KR.conf`, `US.conf` (the 4 region
elements). The build compiles `wlconf` from 18xx-ti-utils R8.8 for the host only (Buildroot's own
download of it, whose package stays disabled because it would put `calibrator` on the target),
applies each text file over TI's default with `wlconf -C`, and checks the output against pinned
digests: byte-identical to what the speakers ran, so the power limits stay exactly as they are. One
patch fixes `wlconf`'s exit status (an uninitialised return value after a successful write). The
package installs the four files and links `wl18xx-conf.bin` to DF; the Wi-Fi bring-up points the
link at the boot's region.

Alternatives that lost:

- **TI's generic `official_inis/WL1835MOD_INI_C2PC.ini` with `wlconf -I`**: documented, but not the
  speakers' values; changing radio power limits and front-end settings would need its own hardware
  and regulatory check.
- **Committing the full 428-line decode**: complete, but it hides the 21 values that matter among
  TI defaults.

## Consequences

- The radio firmware has upstream provenance and a redistributable licence; the radio runs TI's
  upstream revision rather than the one in Bose's image.
- Changing a power limit or a board value is a reviewed one-line text change.
- The generated files match the bytes the speakers ran under Bose by design. `os/upstream/MANIFEST.json`
  records them as generated, with their digests, and the provenance check fails if one drifts.
- The build needs the host compiler for one small C tool.
- No codec script is shipped or regenerated: the speaker keeps its own tuning
  ([ADR 0012](0012-single-dac-writer.md)).

## Code

- `os/configs/soundtap_defconfig`: `BR2_PACKAGE_LINUX_FIRMWARE_TI_WL18XX` and `BR2_PACKAGE_SOUNDTAP_WL18XX_CONF`.
- `os/board/image/post-build.sh`: removes the other firmware files and checks the `wl18xx-fw-4.bin` digest.
- `os/package/soundtap-wl18xx-conf/soundtap-wl18xx-conf.mk`, `os/package/soundtap-wl18xx-conf/board.conf`,
  `os/package/soundtap-wl18xx-conf/wl18xx-conf.sha256`: the board configuration, its build and its digests.
- `os/upstream/ti-connectivity/`, `os/upstream/MANIFEST.json`: the Bluetooth init script and the recorded provenance.
- `os/support/check_provenance.py`: fails a blob without recorded provenance or with a drifted digest.
