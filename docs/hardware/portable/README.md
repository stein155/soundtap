# SoundTouch Portable (`taigan` / scm)

Preview. Nothing on this page was seen on a Portable. It comes from the stock packages
([ADR 0133](../../decisions/0133-more-soundtouch-models-from-stock.md),
[source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register)).

## The board

The ST20's SCM board. Stock's `taigan_scm` and `spotty_scm` trees differ in four things:

- **Battery gauge:** `batmon@0b` on I2C0 at 100 kHz, with no compatible. Stock reads it from
  userspace over `/dev/i2c-0`.
- **RTC alarm interrupt:** stock's sleep naps wake on it.
- **Codec script:** `taigan_scm_normal.cfg`.
- **Sync-out:** none.

SoundTap's U-Boot knows it by the variant resistor: `variant_raw` 2868–3481. It installs from the
ST20/30/Portable package (device id 0x0925) and recovers like the ST20.

## Battery and charger (stock BatteryMonitor)

**Gauge.** SMBus at 0x0b, words little endian, no PEC. Stock polls every second and tries each
read up to four times, 5 ms apart.

| Register | Meaning | Note |
| --- | --- | --- |
| 0x08 | Temperature | 0.1 K |
| 0x09 | Voltage | mV |
| 0x0a | Current | mA, signed |
| 0x0d | RSOC | % |
| 0x16 | Status | 0x20 full, 0x40 discharging, low nibble an error |
| 0x36 | Bose fault code | |
| 0x20 | Manufacturer name | block |
| 0x3f–0x3d | Cell voltages | three cells |

- Stock monitors only a pack whose manufacturer name is `BOSE_ICC` or `BOSE_SANYO`.
- **SHIP** is an SMBus block write: command 0x30, data `BOSE`. It cuts the speaker's power
  until DC comes back.
- Stock never writes ManufacturerAccess (0x00), BatteryMode (0x03) or PEC.

**DC present.** gpio0_27, low while DC is present.

**Charge enable.** gpio3_8. Its active level is in `/mnt/nv/charger_active_level`: `1`, which
stock writes when the file is missing, or `0` on some units.

**Stock's rules:**
- Charging stops on a pack fault, on any read failure and when the pack is full.
- SHIP follows a pack fault, DC above 13.000 V for one second, or a critically discharged pack
  (1 % or less) on battery.
- In deep sleep (30-minute RTC naps), stock ships after 8 days on battery, or after a day when
  the voltage is below 10.300 V.
- Low-battery warning at 20 %.

**SoundTap** (`internal/hw/battery`, `cmd/soundtapd/battery.go`, `battery_ui.go`) follows the
charge rules and reports the pack on `/api/health` (`battery`).
- The status bar carries a battery icon in stock's bands (20/40/60/75/100), with a plus
  while it charges.
- The panel shows "Charging", "On battery NN%" and, at 20 % or less on battery, "Battery
  low NN%".
- Idle on battery for stock's 20 minutes (OnBatteryTimeout), it goes to standby.
- It follows stock's battery protection by default, with no file to turn it on: it sends the
  pack to SHIP on a pack fault, on DC above 13.000 V for one second, and on a critically
  discharged pack (1 % or less) on battery. SHIP is the only gauge write.
- There is no deep sleep, so the 8-day rule does not apply.

## Amplifiers and audio

APConfig-taigan has no PowerAmp object, and stock never puts the amplifiers in standby. SoundTap's
device tree holds ampstby (gpio1_23) high from boot.

Stock settings:
- volume 50 by default, range 30–70;
- chime 50;
- mute delays 1000/1000 ms;
- stereo pairs possible.

## Open on hardware

- The gauge reads.
- The charger polarity.
- That charging runs with SoundTap's rules.
- The RTC wake, which SoundTap does not use.
