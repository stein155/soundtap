# The Wireless Link's Bluetooth radio and its second audio output

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

Everything here was read off this board: Bose's own `binky_sm2.dtb` and `snd-binky.ko`,
taken from the stock slot in mtd8 with a read-only UBIFS mount, plus the product XML in
`/opt/Bose/etc`. Nothing in it is inferred from the product page.

## The connectors are a 3.5 mm pair: aux in and aux out

This corrects an earlier reading of this document. Bose's product XML has no aux source
for binky and no input driver section, and the two GPIOs that look like jack detection
are called `binky_output_select_1` (gpio65) and `binky_output_select_2` (gpio89) in the
`gpiodev` table, with `/opt/Bose/BinkyOutputMonitor` doing exactly one thing with them:

    /sys/class/gpio/gpio65/value
    /sys/class/gpio/gpio89/value
    /sys/class/sound/snd_shelby/outputMode

From that alone the conclusion was "no input". The box itself says otherwise: it has an
aux in and an aux out and nothing else. The register reading agrees: `snd-binky.ko`
sets mcasp0's serializer 0 to receive and serializer 1 to transmit, so that link is full
duplex, and the `dac_adc_reset` line in the same table is one part doing both jobs.

What the two GPIOs select is therefore not *which socket* but *what kind of plug is in
the output one*. `APConfig-binky.xml` carries two output driver parameter sets:

| mode   | sample rate | McASP  |
| ------ | ----------- | ------ |
| ANALOG | 46875 Hz    | mcasp0 |
| SPDIF  | 44100 Hz    | mcasp1 |

One jack, two electrical modes: the same trick as a combined headphone/optical socket.

## Why nothing came out of it at first

`am335x-soundtap-wla.dts` used to enable `&mcasp1` only, so with an ordinary analog cable
the firmware was driving a pin that goes nowhere. It now enables both McASPs.

And the S/PDIF path was not working either. Measured on the running board on 2026-09-16,
passively, from `/proc/asound/card0/pcm0p/sub0/status`: 703929 frames in 30.03 seconds,
**23440 Hz**, while `hw_params` said `rate: 48000`. The machine driver asked AUXCLK for
24.576 MHz and the AM335x gives 24 MHz whatever is asked; 24 MHz / 4 / 2 is a 3 MHz bit
clock, and DIT spends 128 of those per frame because biphase-mark coding doubles the
rate. 3 MHz / 128 = 23437.5 Hz, which is what the board measured to within 0.015%.

That is below the S/PDIF floor of 32 kHz. No receiver could ever have locked to it.

The clock was never the SoC's to make. Bose leaves `AHCLKXCTL` at 0 on mcasp1, which
means the high-frequency clock is an input, and `RMII1_REF_CLK` in mux mode 6 is
`mcasp1_ahclkx`, not `aclkx` as this tree's comment said. There is an oscillator on
the board feeding it: 11.2896 MHz, halved by `CLKXDIV` to 5.6448 MHz, which is
128 x 44100. Bose's declared SPDIF rate is 44100. Our driver was not only clocking it
wrongly, it was driving that pin as an output against the oscillator.

## What mcasp0 is

`snd-binky.ko` is not an ASoC driver: it is a character device that programs the McASP
registers directly and moves samples with EDMA. That makes it a precise specification.
From `snd_shelby_mcasp0_start_tx`:

| register  | offset | value        | meaning                                        |
| --------- | ------ | ------------ | ---------------------------------------------- |
| XFMT      | 0xa8   | `0x80f0`     | 32-bit slots, MSB first, no data delay          |
| AFSXCTL   | 0xac   | `0x112`      | internal frame sync, word-wide, XMOD = 2 slots  |
| ACLKXCTL  | 0xb0   | `0xa3`       | **CLKXM = 1, internal bit clock**, CLKXDIV = ÷4 |
| AHCLKXCTL | 0xb4   | `0x8001`     | **HCLKXM = 1, internal**, HCLKXDIV = ÷2         |
| XTDM      | 0xb8   | `3`          | slots 0 and 1                                   |
| SRCTL0    | 0x180  | `2`          | serializer 0 receives: aux in                   |
| SRCTL1    | 0x184  | `1`          | serializer 1 transmits: aux out                 |

`SRMOD` is 1 for transmit and 2 for receive, which is the opposite way round from a first
reading of it. mcasp1 settles it: its S/PDIF output sits on serializer 2 and that one is
set to 1, and the device tree binding agrees: `serial-dir` uses 1 for transmit and 2
for receive, and the ST10's existing `serial-dir = <2 1 0 0>` matches Bose's registers
exactly.

The clock chain closes on the published rate: AUXCLK 24 MHz ÷2 = 12 MHz AHCLKX, ÷4 =
3 MHz bit clock, 2 slots × 32 bits = 64 bits per frame, 3 MHz / 64 = **46875 Hz**, which
is exactly the `outputSampleRate` in `APConfig-binky.xml`. So the AM335x is the master on
this link, not the slave, and the odd rate is a consequence of the divider chain rather
than an external oscillator.

Full duplex, and now with an obvious reading: AXR1 carries what comes out of the aux out,
AXR0 carries what goes into the aux in.

## The pins, and the one that is deliberately left out

From the AM335x pin table, the eight pads Bose gives mcasp0:

| pad          | mux    | signal        |
| ------------ | ------ | ------------- |
| lcd_data12   | mode 3 | mcasp0_aclkr  |
| lcd_data13   | mode 3 | mcasp0_fsr    |
| lcd_data14   | mode 3 | mcasp0_axr1   |
| lcd_data15   | mode 3 | mcasp0_ahclkx |
| mii1_tx_clk  | mode 6 | mcasp0_aclkx  |
| mii1_rx_clk  | mode 6 | mcasp0_fsx    |
| mii1_rxd3    | mode 6 | mcasp0_axr0   |
| mii1_col     | mode 6 | mcasp0_axr2   |

The ST10's `mcasp0_pins` is the same eight pads with the same modes (it is one System
Module 2 design), so this is not a guess that happens to fit.

`mii1_col` is the one to leave out. Bose muxes it as `mcasp0_axr2` and then immediately
re-muxes it as `mcasp1_axr2` in the group applied after, which is what it ends up as and
what our `mcasp1_pins` already claims. `serial-dir` leaves mcasp0's serializer 2
inactive, so nothing wants it, and two pinctrl nodes claiming one pad is a failed
request.

## What is still unproven

The DAC. `aic3256@1` exists in `binky_sm2.dtb` but is `status = "disabled"` on this
variant, so it is not that chip, and whatever is there has no control bus in the device
tree, only a reset line. `dac_adc_reset` is gpio74 and `dsp_reset` is gpio86, both
driven 0 by Bose at init. Neither name carries the trailing `n` that `auth_resetn` in
the same table does, and that one is driven the other way, so 0 reads as released rather
than held. `init.d/soundtap` now sets both the same way Bose does.

Line out has since played on the bench WLA (see the
[hardware checklist](../hardware-test.md#release-checklist-per-model)). If the jack is silent on
a board, those two GPIOs are the first thing to check; on an alpha image they can be changed
over SSH without rebuilding anything (production has no SSH).

## Bluetooth

The radio is the other half of the WL1835 that already carries Wi-Fi. From
`binky_sm2.dtb`:

- `serial@48024000` (`&uart2`) is the only enabled four-wire UART on this variant,
  status `okay`, with `uart2_pins_default` claiming `spi0_sclk` (rx, mode 1), `spi0_d0`
  (tx, mode 1), `i2c0_sda` (ctsn, mode 2) and `i2c0_scl` (rtsn, mode 2). I2C0 is
  `disabled` on binky, so nothing else wants those two pads.
- the enable line is `bt_enable` in the `gpiodev` table, gpionum 63 = **gpio1_31**, the
  `GPMC_CSN2` pad, output, initial 0.
- `BoseApp-binky.xml` says `<isBluetoothAudioSourceSupported>true</isBluetoothAudioSourceSupported>`.

Bose reaches it with TI's deprecated shared transport (`/usr/bin/uim`, `st_kim`). The
mainline path is `hci_ll` over serdev, which instantiates the HCI from the device tree at
probe time, with no `hciattach`. It asks for `ti-connectivity/TIInit_11.8.32.bts` by the
version it reads off the chip after reset. linux-firmware does not have that file: it
ships 6.2.31, 6.6.15 and 7.2.31, all WiLink6/7.

The obvious candidate was the copy in `os/stock`, which is byte-identical to the one
on this board's own stock slot, and an earlier version of this document called it "one
unmodified TI script redistributed by two Bose products". That was wrong. Identical across
two Bose products only shows Bose ships one file. Held against TI's own, from TI's GitHub
organisation, it is a **different file**: 75439 bytes against 70313, header dated
24-Jul-2018 against 17-Sep-2014. Same description and the same ROM compatibility, so it is
almost certainly a later TI revision rather than anything Bose wrote. But "almost
certainly" is not provenance, and the only source citable for it is a Bose rootfs, which
[ADR 0049](../../decisions/0049-no-stock-bytes.md) rules out.

So the build ships TI's own revision from `os/upstream/ti-connectivity/`, with its
provenance recorded next to it, and the profile check fails if anything under
`os/stock` is ever installed into the image again.

The userspace (`bluetoothd`, `bluealsa`, `bluealsa-aplay`, `bluealsa-cli` and `bt.sh`) was
already in the image, switched off, when this was written, and had not been seen working on
any model. Bluetooth has since played on the WLA, the ST10 and the ST300
([hardware checklist](../hardware-test.md#release-checklist-per-model)).

Enabling it also uncovered a bug that was already live: `/etc/asound.conf` does not exist
on this board. `init.d/soundtap` copies `asound.conf.bus` when `AUDIO_MIX=1`, that file was
installed by the Shelby ALSA plugin package (removed since), and the WLA profile did not
build it, so the copy failed silently (`2>/dev/null || true`) and ALSA fell back to its built-in
default, the raw card. librespot has been opening `hw:0,0` behind soundtapd's own `aplay`.
The WLA overlay now ships its own `asound.conf.bus`, without the `pcm.shelby` block it
has no plugin for.

Two observations on this board shape how soundtapd closes the pairing window
([ADR 0055](../../decisions/0055-household-token-and-windows.md)). BlueZ's own
`DiscoverableTimeout` left the controller answering inquiries after the timer ran out, while it
reported `Discoverable: no`; so `main.conf` keeps the timeout at 0 and the close is a real
write. And writing `Discoverable` false also turned connectable off, so a paired phone could not
connect until `Connectable` was set back on.
