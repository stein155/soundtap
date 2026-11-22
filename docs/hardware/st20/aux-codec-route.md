# AUX analog input — codec register evidence

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

Recovered from stock firmware artifacts. Read-only RE; no hardware writes performed for
this note. The driver-path conclusions below describe the analyzed stock software, not
the current mainline audio design or a completed AUX capture test.

## Source evidence

- stock `/lib/firmware/spotty_scm_normal.cfg` (not in the repository, ADR 0012) — full ST20 codec init with
  comments naming every register (lines 21021–21073 for the AUX ADC section).
- stock `/lib/firmware/rhino_sm2_passthrough.cfg` — same ADC setup applied
  in the ST10 passthrough (AUX) configuration.
- stock `/lib/modules/3.14.43+/kernel/sound/bose/snd-shelby.ko` — ST20 driver
  strings confirm: `snd_shelby_mcasp1_dma_rx_callback`,
  `snd_shelby_mcasp1_rx_get_buffer_on_deck`, McASP1 at `0x4803C000`, named `mcasp1`,
  dual EDMA RX ping/pong — McASP1 is the AUX capture path on the ST20.
- `snd-shelby2.ko` — ST10 driver; module param: `default_mode: aux, passthrough, wifi or airplay`.
  Source sysfs at `/sys/class/sound/snd_shelby/source`.

## AIC3256 input routing for AUX (page 1 registers)

Register meanings can be checked independently in TI's
[TLV320AIC3256 Application Reference, SLAU306A (January 2013)](https://www.ti.com/lit/ug/slau306a/slau306a.pdf):
sections 5.2.18–20 (ADC clocks), 5.3.26–34 (input routing/gain) and 5.4.2 (adaptive filtering).
That reference establishes the chip's register semantics, not this speaker's board wiring.

These writes configure ADC power and input routing in the analyzed scripts; they do not
alone prove clocks, capture or audible output. They are identical in
`spotty_scm_normal.cfg` and `rhino_sm2_passthrough.cfg`.

```
Page 0, reg 81 = 0xc0   ; Power up LADC and RADC
Page 0, reg 82 = 0x00   ; Unmute LADC and RADC

Page 1, reg 52 = 0x04   ; Route IN3L to LEFT_P  with 10K input impedance
Page 1, reg 54 = 0x10   ; Route IN2R to LEFT_N  with 10K input impedance
Page 1, reg 55 = 0x40   ; Route IN1R to RIGHT_P with 10K input impedance
Page 1, reg 57 = 0x10   ; Route IN1L to RIGHT_N with 10K input impedance
Page 1, reg 59 = 0x00   ; Enable MicPGA_L Gain Control, 0 dB
Page 1, reg 60 = 0x00   ; Enable MicPGA_R Gain Control, 0 dB
```

Context registers (already written by normal codec init; confirmed same values in passthrough):

```
Page 0, reg 18 = 0x02   ; NADC = 2, divider powered off
Page 0, reg 19 = 0x88   ; MADC = 8, divider powered on
Page 0, reg 20 = 0x80   ; AOSR = 128
Page 8, reg  1 = 0x04   ; adaptive mode for ADC miniDSP
```

The passthrough LOL/LOR output level differs from normal: `+5 dB` on Page 1 reg 18/19 vs `0 dB`.
That suggests the passthrough boosts AUX output to match perceived volume of streamed sources.

## ST20 kernel-level AUX path

```
Analog input pin (IN1L/IN1R/IN2R/IN3L)
  → AIC3256 ADC / miniDSP page 8 (configured by above registers)
  → McASP1 receive (0x4803C000, EDMA channels)
  → snd-shelby.ko  snd_shelby_mcasp1_dma_rx_callback
  → /dev/snd_shelby  (read side for AUX)
```

On ST10 the same analog path ends at McASP0 RX via snd-shelby2 (`mcasp` passthrough mode).

## Source routing sysfs

Both drivers expose `/sys/class/sound/snd_shelby/source` with a `snd_shelby_source_store`
write handler. Valid values: `aux`, `wifi`, `airplay`, `passthrough`.

Writing `aux` selects the McASP1 RX EDMA chain. Writing `wifi` switches back to McASP0 TX
(streamed PCM). This is what `initAudio()` in `app/cmd/soundtapd/player_setup.go` currently writes; `runSourceRouting`
now returns immediately on mainline because the path does not exist there.

## Current implementation and remaining evidence

The old plan to add McASP1 and a new passthrough script is not the current implementation.
`os/board/common/dts/ti/omap/am335x-soundtap-st20.dts` enables McASP0 with capture and
playback serializers. `app/cmd/soundtapd/aux.go` captures ALSA into the common Mixer, and
`internal/hw/model` selects the model's source gain. The speaker retains its own normal codec
script under [ADR 0012](../../decisions/0012-single-dac-writer.md); this note is not an
instruction to ship, regenerate or replace Bose's miniDSP tuning.

The stock analysis still leaves these questions for model-specific evidence:

- Which exact stock driver/config inputs produced the McASP1 conclusion above? Their
  versions and hashes are missing; that conclusion cannot override the current DTS.
- Are the ADC clock, channel routing and physical capture correct on the candidate? Register
  writes and an ALSA node are not a completed line-input test.
- Do source gain and latency match stock on that model? A +5 dB codec output setting alone
  does not isolate AUX loudness through the whole chain. Follow the AUX rows in
  [hardware-test.md](../hardware-test.md).

Further hardware work requires the session's explicit permission. The static register list
is evidence to investigate, not a safe-to-run codec procedure.
