# Provenance: the stock sources we analysed

SoundTap images are built from source (Buildroot `make os`, `os/installer/build-universal-stu.sh`):
mainline kernel, our SPL and U-Boot, our drivers and a Buildroot userland. This directory
holds no Bose bytes ([ADR 0049](../../docs/decisions/0049-no-stock-bytes.md)); the stock
kernel, U-Boot, device trees, 3.14 modules, CC2540/LED/PRU firmware, the stock STU container
and the tooling that needed them are gone from the repository. Each speaker's codec script is
copied from its own stock firmware at boot (ADR 0012).

## Reverse-engineering source register

This register points to the surviving notes, not to redistributed stock bytes. **Not recorded**
means the note does not identify that field; it is not a new measurement. No local
stock input was configured when this register was assembled on 2026-11-13; no source hashes were re-measured.

| Record | Model and source identity recorded in the note | Evidence and locator | Missing provenance / scope |
| --- | --- | --- | --- |
| [ST10 audio](../../docs/hardware/st10/stock-audio-path.md) | Stock Ping backup dated 2026-09-19; analysis 2026-09-22 | `APConfig-rhino.xml`, `snd-shelby.ko`, APServer tables at `0x2a52d0` / `0x2a5468`; owner listening report, 2026-09-23 | Source-file SHA-256 and complete run capture not recorded; listening did not isolate individual fixes |
| [ST10 boot lamps](../../docs/hardware/st10/boot-splash.md) | `mtd8-File-System-Ping.oob`; source firmware/date not recorded | `rhino-boot-splash`, `led-splash-write`, `shelby-led.ko`; FIFO output measured under QEMU | No source hashes or saved timing capture; 120 in the disassembly summary versus 139 in the emulator table is unreconciled; emulator output is not a physical GPIO measurement |
| [ST10 infrared](../../docs/hardware/st10/remote-control.md) | SoundTap build of 2026-09-23 | Owner key presses and inline decoder log excerpt | Complete log and candidate SHA-256 not recorded; only the named keys and model were observed |
| [ST10 ROM / stock U-Boot](../../docs/hardware/st10/usb-recovery-and-rom.md) | `st10-rhino-20260919`, `mtd4-U-Boot.oob`, stock `2014.07-05722-g93f9891` | Disassembly addresses, SYSBOOT read and ROM enumeration excerpt in the note | Dump SHA-256 and complete USB capture not recorded; not a candidate NAND-restore approval |
| [ST20 AUX](../../docs/hardware/st20/aux-codec-route.md) | Stock `spotty_scm_normal.cfg`, `rhino_sm2_passthrough.cfg`, `snd-shelby.ko`, `snd-shelby2.ko`; version/date not recorded | Config lines 21021–21073, page/register table and driver symbols | Analysis-input SHA-256 not recorded; no working AUX capture measured |
| [ST20 BCO](../../docs/hardware/st20/bco-recovery.md#reading-the-evidence) | `Update_ti_27.0.6.46330.5043500.scm.stu`; analysis date not recorded | Stock script paths and symbol names; two historical BCD SHA-256 values are retained in the note | Container/script/binary hashes and complete analysis captures not recorded; BCD hashes do not identify the other inputs |
| [ST20 standby clock](../../docs/hardware/st20/standby-clock.md) | `Update_ti_27.0.6.46330.5043500.scm.stu` (SHA-256 9cdff040), its BoseApp (SHA-256 8b2b7d19); analysis 2026-11-22 | `CWalkingClockItem` vtable and the clock-screen setup in BoseApp; the clock proto in `libCore.so`; the `ssd_spi` sysfs controls | Static analysis only; whether an ST20 reaches low-power standby by itself was not traced |
| [ST20 ROM rescue](../../docs/hardware/st20/rom-usb-recovery.md) | Board module 412568; dated runs and builds 2026-09-27 | Pad/register values, harness result tables and observed recovery of the then-installed chain | Complete TFTP/serial captures and candidate SHA-256 not recorded; old SPL guard and stock fallback are historical |
| [WLA audio and Bluetooth](../../docs/hardware/wla/bluetooth-and-analog-output.md) | Stock slot mtd8; `binky_sm2.dtb`, `snd-binky.ko`, product XML; rate observation 2026-09-16 | McASP register/pin tables and frame-count calculation | Source hashes and measured build not recorded; configuration does not prove successful playback or pairing |
| [WLA audio configuration](../../docs/hardware/wla/audio-config.md) | Stock `APConfig-binky.xml`, `BinkyOutputMonitor`, `snd-binky.ko`; analysis 2026-09-19 | Config attributes, monitor logic and inline GPIO/mux readings | Source hashes and complete capture not recorded; jack-state interpretation remains incomplete |
| [WLA buttons](../../docs/hardware/wla/buttons.md) | Stock `binky_sm2.dtb`, `binky-keypad.ko`, BoseApp; analysis 2026-09-19 | DT properties, debounce disassembly at `0x810` and BoseApp table at `0x4bb81a` | Source hashes and physical gesture run not recorded |
| [WLA lamps](../../docs/hardware/wla/led-behaviour.md) | Stock `Update_ti_27.0.6.46330.5043500.scm.stu`, BoseApp and `binky_sm2.dtb`; analysis 2026-09-19 | Symbol/log analysis and inline sysfs sample from the build of 2026-09-19 | Source and candidate SHA-256 and complete run capture not recorded; blue-channel labels were still unconfirmed |

For a new or recovered source record, add the actual file SHA-256 and its source identity,
then link the relevant finding. Do not substitute a hash of a Markdown note for the analyzed
binary's hash. Keep capture references and the limits of the observation alongside the finding;
release approvals still use generated `SHA256SUMS` and the per-model hardware report.
