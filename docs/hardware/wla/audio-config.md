# What stock's audio config says about the Wireless Link

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

`opt/Bose/etc/APConfig-binky.xml`, from the stock rootfs this board shipped with, read on
2026-09-19. It is short enough to matter line by line, and on three points the Wireless
Link is the opposite of the speakers whose config sits next to it.

## AUX is boosted here, not attenuated

```
binky   wifiAttenuation="1"           auxAttenuation="1.22"
spotty  wifiAttenuation="0.39810699"  auxAttenuation="1"
mojo    wifiAttenuation="0.39810699"  auxAttenuation="1"
rhino   wifiAttenuation="0.39810699"  auxAttenuation="1"
burns   wifiAttenuation="0.39810699"  auxAttenuation="1"
```

Every speaker pulls streamed audio down by 8 dB and leaves its line input alone. The
Wireless Link does neither: streamed audio runs at full scale and AUX is lifted by 1.22,
which is +1.73 dB. That follows from what the box is: it feeds someone else's amplifier
at line level, so there is no hot amp to protect and no reason to throw away headroom.

**This corrects the audio gap for this model.** "AUX ~8 dB too loud, BoseApp applies
`wifiAttenuation≈0.398` vs `auxAttenuation=1.0`" is true of the ST20 and false of the
WLA; porting the speakers' numbers here would attenuate the wrong source and then some.

## Volume is done in software, on the ARM

```
useArmVolumeControl="true"      (spotty: false)
```

The speakers hand volume to their DSP. This box has no DSP in the path, so the scaling
is ours to do, which is what `app/internal/audio` already does, and it is why the APServer
coefficient table is the right curve to be applying in software. See
`TestStockVolumeCurveMatchesAPServerTable`.

## No bass control, no ABL, no AIC, no battery

```
bassParameters hasControlCapabilities="false" minimum="0" maximum="0" defaultValue="0"
primaryParameters hasAIC="false" hasABL="false" hasBattery="false" hasAudioInputMux="true"
```

The ST10 and ST20 offer bass over -9…0. Stock offers none here, and neither does SoundTap:
the Wireless Link reports the range 0…0 and its settings page has no Sound section.

## Two output drivers, which are our two cards

```
defaultOutputDriverIndex="0"
 [0] ANALOG  46875 Hz  useDither="true"   audioLatency="20"
 [1] SPDIF   44100 Hz  useDither="false"  audioLatency="29"
```

Both switch on `/sys/devices/virtual/sound/snd_shelby/outputMode`, which is the pair of
GPIOs this board senses the plug with (`binky_output_select_1/2`, see
[Bluetooth and analog output](bluetooth-and-analog-output.md)).

`/opt/Bose/BinkyOutputMonitor` is 8 KB and does one thing, which reads out in full:

```
open /sys/class/gpio/gpio65/value          (gpio2_1,  Linux 545)
open /sys/class/gpio/gpio89/value          (gpio2_25, Linux 569)
loop, once a second:
    read one byte from each, lseek both back
    if the two bytes are equal and differ from the last value written:
        write that same ASCII digit to snd_shelby/outputMode
```

So the mode is the agreed state of the two lines; disagreement writes nothing. A partly
inserted plug is one possible explanation, not a measured interpretation of that state.

Which digit means what is in `snd-binky.ko`, and `snd_shelby_outputMode_store` says it
plainly. It `sscanf`s an int, rejects anything outside 0..1 or equal to the current mode,
logs `"Storing new Output Mode (%d)"`, and then branches on the **previous** mode: coming
from 0 it runs `edma_stop` and `snd_shelby_mcasp1_start_tx`, coming from 1 it takes the
other path. So:

| outputMode | McASP | mode |
|---|---|---|
| `0` | mcasp0 | ANALOG |
| `1` | mcasp1 | S/PDIF |

which agrees with `defaultOutputDriverIndex="0"` and with the order of the two parameter
sets above. **Both lines reading `0` select analog; both reading `1` select optical.**
This establishes the software selection, not the physical jack state for every bit pair.

Both pads are muxed as inputs in our tree now so they can be read at all; until then they
sat in whatever mode the bootloader left and an optical plug was undetectable.

Reading them on the board on 2026-09-19 gave **0 and 1**: a disagreement, which is the
one state stock's monitor refuses to act on. That first reading also caught a real
mistake: the pads had been declared `PIN_INPUT_PULLUP` while stock's `0x27` is
`PIN_INPUT_PULLDOWN`, and the button pads were pulled up where stock's `0x2f` is pull
*disabled* (the pull-ups for those lines sit on the board, the same reason the ST10's
switch-ins are muxed that way).

**Fixing the pulls did not change the reading.** All four pads now come back byte for
byte as stock leaves them, and the two lines still say 0 and 1:

```
gpmc_clk       0x27     lcd_ac_bias_en 0x27      (stock: 0x27, 0x27)
gpmc_ad9       0x2f     gpmc_ad11      0x2f      (stock: 0x2f, 0x2f)
gpio545 = 0    gpio569 = 1
```

The disagreement persisted after the mux/pull settings were made to match stock. That
rejects the simple wrong-pull explanation from the earlier attempt; it does not identify
what drives gpio569 high or establish the physical jack state.

What that leaves is two readings, and no evidence to choose between them: either the jack
is empty or the plug is not seated, or the pair encodes more than the two states the
monitor acts on, with `01` and `10` meaning something other than "half-way in". The next
measurement is a plug going in and out while these two values are watched, and that needs
hands on the speaker.

The rates match what SoundTap already builds: analog first at 46875, S/PDIF at 44100. The dither flag and the per-path latency
compensation are not implemented on our side, and neither is the ASRC constant set
(`invFsamp`, `ki`, `invki`) that stock uses to resample into those rates.
