# What stock does with the ST10's audio path

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

Read out of the stock rootfs (`mtd8-File-System-Ping`, backup of 2026-09-19) on 2026-09-22.
Register values and disassembly below are static evidence; the dated listening result is
a hardware observation. Explanations of why a change affected sound are hypotheses unless
isolated by a measurement. Individual source-file hashes were not recorded in this note.

## Getting at the stock rootfs

`ubireader_extract_files` maps no blocks on this image, so the UBI volume is reassembled by
hand. Each PEB is 131072 bytes: `UBI#` erase-counter header at 0, `UBI!` VID header at 2048,
data at 4096, LEB length 126976. In the VID header the volume id and LEB number are two
big-endian words at **offset 8**, not at 4 — reading them at 4 finds no blocks at all, which
is the trap that makes the image look empty:

```python
PEB, VID, DATA, LEB = 131072, 2048, 4096, 126976
for off in range(0, len(image), PEB):
    peb = image[off:off + PEB]
    if peb[VID:VID + 4] != b"UBI!":
        continue
    vol_id, lnum = struct.unpack(">II", peb[VID + 8:VID + 16])
    ...
```

655 LEBs come out; concatenated in LEB order they are a UBIFS image that
`ubireader_extract_files -e 126976` unpacks whole.

## opt/Bose/etc/APConfig-rhino.xml

The audio path's parameters, in Bose's own words:

| attribute | value | Interpretation and limits |
| --- | --- | --- |
| `outputSampleRate` | `46875` | The analog link's rate, from the vendor. Matches the 46876.4 Hz measured off `hw_ptr` on a running ST10, and the McASP dividers below. |
| `useArmVolumeControl` | `false` | **Stock does not scale samples on the ARM.** Volume is the DSP's job; the CPU hands it full-scale PCM. |
| `hasAIC` | `true` | The AIC3256 is part of the path, not an optional codec. |
| `wifiAttenuation` | `0.39810699` | Exactly −8.00 dB on the streaming input. `auxAttenuation` is `1`. |
| `chimeVolume` | `41` | Chimes play at their own level, not the user's. |
| `putDSPinSuspend` | `false` | The DSP stays configured while idle. |
| `bassParameters` | −9 … 0, default 0 | The configured slider range; this XML alone does not establish its signal-processing effect. |
| `muteParams` | `/dev/gpiodev/mute_out`, polarity true, 100 ms each way | The amp mute, with a settling delay on both edges. |
| `asrcConstants` | `invFsamp = 2.1333333e-05` | 1 / 46875, again. |
| `dspImages` | `rhino_sm2_normal.cfg` | The 9976-write blob SoundTap loads; separate left/right images exist for stereo pairing. |

A later PCM measurement of stock on an ST20 found a first-order bass shelf at about 260 Hz,
1.22 dB per step. That is separate hardware evidence on an ST20, not a result obtained from this
ST10 XML. Current ST10/ST20 software implements that shelf
([technical design](../../contributors/design.md#6-audio)).

`useArmVolumeControl="false"` identifies the configured volume path. In the September
SoundTap build described below, the mixer instead scaled PCM with `stockVolumeGain`,
checked against APServer's table at 0x2a52d0, and wrote one fixed DSP coefficient at boot.
Current SoundTap moves volume to the codec DSP when supported, with a PCM fallback
([ADR 0012](../../decisions/0012-single-dac-writer.md#the-master-volume-lives-in-the-codec-dsp-as-on-stock)). The table analysis established
stock's volume-100 words; equal loudness was reported by ear on that build. That result is
not a measurement of every current candidate or every source.

## McASP0, from snd-shelby.ko

`snd_shelby_start_tx` writes: PDIR `0x1C000003`, AHCLKXCTL `0x8001`, ACLKXCTL `0xA3`,
AFSXCTL `0x112`, XFMT `0x80F0`, XCLKCHK `0x00FF0007`, XMASK full. `mcasp0_fck` is
`sys_clkin_ck`, the 24 MHz crystal, so AUXCLK / 2 / 4 = 3 MHz over two 32-bit slots = 46875 Hz.

PDIR bit 27 (AHCLKX) is **set** here and clear in the Wireless Link's `snd-binky.ko`
(`0x14000002`). `ti,ahclkx-not-driven` therefore belongs on the WLA tree only.

## Closed: maximum volume was lower than stock

**Closed on 2026-09-23.** Listening to the duo build of that day on the bench ST10 confirmed
that it is as loud as stock. That build carries the volume-100 words in the DSP
block, the adaptive-buffer flip and the `psm_disable` hog described below. Which of the three
closed the gap was not isolated, because isolating it would mean playing audio.

Reported from the bench on 2026-09-21. What was ruled out first:

- the software curve — `stockVolumeGain(100)` is unity, matching APServer's table entry;
- the sample rate — both run 46875 Hz;
- the codec configuration — the same 9976-write blob, readback-verified, not blind.

What APServer does, read out of its disassembly on 2026-09-22:
`APDigitalAudioDeviceDSP::SetVolumeAction(const std::vector<int>&)` stores a vector of
32-bit words, and `SendVolumeAction()` seeks the codec device to **0x2C10** (page 0x2C,
register 0x10, the block the September SoundTap build wrote once at boot) and writes that whole
vector, every time the volume changes. So the block is stock's volume control, not a
one-time setup, and SoundTap's is a constant sitting where stock puts a variable. Whether that
constant matched stock's value at volume 100 was initially unresolved; the later table
analysis below answered it.

**Answered on 2026-09-22 (later the same day):** the vector's first two words come out of
two 101-entry tables in APServer, at 0x2a52d0 and 0x2a5468, indexed by the volume clamped
to 0..100. Volume 100 is `0x400000` / `0x000000`; volume 10 is `0x0007d9` / `0xbd9c28`.
Those volume-10 words are exactly what `aic3256-loader` wrote before the corrected block, so on those
builds the ST10 ran at stock's volume 10 at full slider. The corrected September block
carried the volume-100 words. The blind pass also never flipped the miniDSP's adaptive
buffer, so the level it wrote stayed in the idle buffer; that pass was corrected too.

Words 2 to 7 need no table. The block first arrived in 25875224 as `set_volume_10`, a copy
of stock's own write at volume 10. The table code in APServer sets only the two words it
indexes, so the rest of the block is stock's at any volume. The software curve,
`stockVolumeGain(100)`, is exactly 0 dB. At full volume the ST10 now writes what stock
writes, and there is no software gain in front of it. Any gap that is still left has to
be established by an authorised audio measurement; this analysis alone does not settle it.

One interpretation remains unisolated: words 5 and 6 are `0x32D645` (0.39717), close to
`wifiAttenuation`. That resemblance suggests input attenuation; it does not prove every
word's meaning or maximum safe value.

The other retained register evidence is stock's `gpiodev` entry **`psm_disable`, flat
0x4D = gpio2_13 (lcd_data7), output, initial 1**. `scmmond` opens it alongside `mute_out`,
`ampstby`, `dac_reset` and `dm870pwr`. The September candidate added a GPIO hog held high.
The listening result did not isolate its contribution from the DSP coefficient and
adaptive-buffer changes. The same table names `bt_enable`, flat 0x3F = gpio1_31,
output, initial 0, matching the Wireless Link's line.

The maximum-loudness issue was closed by the 2026-09-23 observation above. It is not an
outstanding instruction to adjust DSP words or perform another audio test.
