# The boot pattern stock plays, and where it comes from

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

Stock runs its front-panel boot animation from its own init script, before anything else
of Bose's userland is up:

```
# /etc/init.d/rhino-boot-splash
# Invoke led-splash-write binary which will display knight rider LED pattern.
# If the process remains active, Rhino process will eventually kill the
# process. ...
# Note the rhino-specific implementation for led-splash-write does not use data from
# the persistence partition and should be invoked as early as possible in init processing
/opt/Bose/led-splash-write &
```

So the pattern is a **knight rider sweep**, it starts as early as init can start it, and it
runs until BoseApp ("Rhino process") kills it — which is exactly the window SoundTap has
nothing to show in.

`/opt/Bose/led-splash-write` (ELF, ARM, stripped, 48 KB) opens the LED character devices
directly:

```
/dev/shelby-leds/uledw   /dev/shelby-leds/uledy
/dev/shelby-leds/rled3   /dev/shelby-leds/rled4
/dev/shelby-leds/rled5   /dev/shelby-leds/rled6
/dev/shelby-leds/rled7   /dev/shelby-leds/rled8
```

and writes one byte per change: the write seen in the disassembly is a single `0x78`
(120), which `shelby-led.ko` caps at 100 % duty — full brightness, not a PWM ramp. The
LED device classes are `LedPWMDevice`, `LedControlDevice`, `LedMultiDevice` and
`PruLedDevice`; the last one talks to `/dev/rpmsg_pru31`, which is the PRU path a
different model uses and not this one.

## The pattern, measured under QEMU

Neither the order nor the timing is a string in the binary, and the script says it reads
nothing from the persistence partition, so both are compiled in. Rather than disassemble
the sweep loop, run the binary: the stock root filesystem carries all the libraries it
needs, so `qemu-arm-static` in a chroot runs it on any machine. Give it a `/proc/variant`
holding `rhino` (it opens that file O_RDWR and exits -1 without it), make
`/dev/shelby-leds/*` FIFOs, and read them with timestamps.

The original note records these emulator byte values (the complete capture is missing):

| step | lamp lit | value | others |
|------|----------|-------|--------|
| 1 | `uledw` (gpio0_19) | 139 | 0 |
| 2 | `rled3` (gpio0_4) | 139 | 0 |
| 3 | `rled7` (gpio2_23) | 139 | 0 |
| 4 | `rled4` (gpio0_12) | 139 | 0 |
| 5 | `rled7` | 139 | 0 |
| 6 | `rled3` | 139 | 0 |

then back to step 1, forever. **350 ms** per step, measured at 350.0-353 ms over dozens of
cycles. Every step writes all four lamps -- the lit one and three zeros -- and the run
opens with a single 0 to `uledw`, `uledy`, `rled3`, `rled4` and `rled7`.

The disassembly summary above names `0x78` (120), while the emulator table records 139
(`0x8b`). With no identified binary hash or full capture, these are two unreconciled byte
claims; do not treat them as a matched trace of one instruction. Both exceed the documented
100 cap, so they imply the same saturated software request, not a measured physical duty.

139 is above the driver's 100 % cap, so the requested lamp level is full brightness; there is no
ramp and no PWM step anywhere in the sequence. Four lamps sweep and bounce: 1-2-3-4-3-2,
which is what "knight rider" means here, and the endpoints are not repeated.

One thing this measurement says that the device tree did not: `rled7` is **gpio2_23**, the
pad this tree currently gives to the NCP6922 enable line. Stock's own tree claims it twice
-- once in `shelbyleds` as a lamp, once in `ncp6922_pins_default`. The emulator records
writes to the lamp device; it does not measure gpio2_23 or prove the physical role of that
pad. Any electrical/audio investigation needs its own approved test plan. The historical
suggestion to toggle this line during playback is not a standing instruction.

Extracting the binary: the stock root filesystem is in the NAND backup as
`mtd8-File-System-Ping.oob`. Strip the 64-byte OOB from every 2048-byte page, then
reassemble the UBI volume by hand -- `ubireader_extract_files` maps no blocks on this
image, but the volume is plain: each PEB carries `UBI#` at 0 and `UBI!` at 2048, data
starts at 4096, and LEB n of volume 0 goes at offset n * 126976. Feeding that UBIFS image
to `ubireader_extract_files` gives the whole tree.
