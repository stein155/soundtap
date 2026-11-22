# The Wireless Link's two buttons, and why they need care

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

Read on 2026-09-19 out of `binky_sm2.dtb`, `binky-keypad.ko` and BoseApp, all from the
stock image this board shipped with. What follows is what a port has to respect.

## What they do in SoundTap

A tap on AUX toggles the source and a hold picks Bluetooth; a tap on Bluetooth selects it
and a hold opens pairing. Holding both for ten seconds enters the setup network, or leaves
it again. Stock's runtime gesture for this unit could not be read, so this one is ours: it
follows the setup chord on the keypad models.

## There are two, and they are not the ST10's keypad

`binky_sm2.dtb` disables the node SoundTap already knows:

```
shelby_keypad { compatible = "bose, shelby-keypad"; ... status = "disabled"; };
binky_keypad  { compatible = "bose, binky-keypad";
                pinctrl-0 = <&keypad_pins_default>;
                switchins  = <0x17 0x1b>;
                interrupts = <0x17 3 0x1b 3>; };
```

`switchins = <23 27>` are gpio0_23 and gpio0_27 — Linux 633 and 637 — on
`keypad_pins_default = <0x24 0x2f 0x2c 0x2f>`, which is `gpmc_ad9` and `gpmc_ad11` in
mux mode 7, input, internal pull disabled (bit 3 set). The pull-ups are on the board. So a button reads low when pressed, and there are no strobes
at all: the ST10's 3x3 matrix does not exist here, just two lines.

Those two pads are free on this board because its NAND is 8 bit and uses `ad0..ad7` —
the same reason `ad15` could be taken for the analog bias.

`binky-keypad.ko` registers its character device under the name `shelby-keypad`, and
BoseApp opens `/dev/shelby-keypad` on every variant (`ConsoleButtonsDevice uses %s`), so
the wire format is the one already recorded for the ST10: one ASCII letter per event,
case carrying the edge.

## The trap: these are gpio0 lines

`app/internal/hw/keypad.go` is worth reading before touching this. Every
earlier attempt to put the ST10's keypad in the kernel — `gpio-keys`,
`gpio-matrix-keypad`, a custom scanner — stopped Wi-Fi from coming up, because they all
request GPIO interrupts on switch-in lines that share bank 0 with the WL1835's own
interrupt on gpio0_31. The WLA's two switch-ins are gpio0_23 and gpio0_27: the same
bank, the same hazard.

The current WLA port uses kernel `gpio-keys-polled`, not interrupt-driven `gpio-keys`
(`os/board/common/dts/ti/omap/am335x-soundtap-wla.dts`). The ST10 matrix remains
userspace-polled. Both avoid requesting button IRQs on bank 0.

The stock module has a debounce thread and registers interrupts (see below). The original
claim that those interrupts are *only* wake sources was an interpretation, not a recorded
runtime measurement. Retain the register and disassembly evidence without treating that
interpretation as proof of the stock driver's complete IRQ behaviour.

## The wire format, read out of both sides

`binky-keypad.ko` keeps a ring of one byte per event. `debounce_thread` stores
`index | 0x20` on a press and `index` on a release; `keypad_read` adds `'A'` on the way
to userspace. So one ASCII letter per event, the letter naming the button and the case
naming the edge:

- **lower case is a press**, `'a' + index`
- **upper case is a release**, `'A' + index`

BoseApp reads it back the same way. At `0x4bb81a` it logs `"Read of raw button device
gives value of 0x%02X"`, subtracts `'A'`, and runs a 46-entry `tbb` jump table; then
`cmp r6x60` decides the state field — above `` ` `` (lower case) is
`KEY_STATE_PRESSED`, at or below is `KEY_STATE_RELEASED` — and `bfi r3`
stamps the producer as `KEY_PRODUCER_CONSOLE`.

The table itself is shared across the whole product family, and its eleven live entries
are exactly an ST10 front panel (the gaps at `D`, `H` and `L` are the matrix positions
with no switch behind them):

| letter | code | key | | letter | code | key |
|---|---|---|-|---|---|---|
| `A` | 0x0A | `VOLUME_UP` | | `J` | 0x10 | `PRESET_5` |
| `B` | 0x0E | `PRESET_3` | | `K` | 0x0F | `PRESET_4` |
| `C` | 0x08 | `POWER` | | `M` | 0x12 | `AUX_INPUT` |
| `E` | 0x0B | `VOLUME_DOWN` | | `N` | 0x1B | `BLUETOOTH` |
| `F` | 0x0D | `PRESET_2` | | | | |
| `G` | 0x0C | `PRESET_1` | | | | |
| `I` | 0x11 | `PRESET_6` | | | | |

Anything else logs `"IGNORING unknown button 0x%02X"`.

## Which button is which

Not by position — `binky-keypad.ko` hard-codes it. `debounce_thread` reads the row the
interrupt reported and picks the button number from it:

```
810: ldr r3, [r9]   @ keypad_row
818: cmp r3
824: moveq r4       @ row 0 -> button 13
828: movne r4       @ row 3 -> button 12
```

The module registers exactly two interrupts, named `switchin0` and `switchin3` — the
positions gpio0_23 and gpio0_27 hold in the ST10's `switchins = <23 26 22 27>`, which is
why the WLA reuses those two lines and those two names. Through the letter table above:

| line | row | button | letter | key |
|---|---|---|---|---|
| gpio0_23 | 0 | 13 | `n` / `N` | `KEY_VAL_BLUETOOTH` |
| gpio0_27 | 3 | 12 | `m` / `M` | `KEY_VAL_AUX_INPUT` |

Which is a sensible pair for this box, and it also explains the three dead letters: 12
and 13 are the two directly-wired keys that sit past the end of the ST10's 3x3 matrix,
where `D`, `H` and `L` are the matrix positions with no switch behind them.

## What is still missing

What a *hold* does. BoseApp carries a single-button concept for this variant:

```
BoseApp-binky.xml:  <hasSingleButton>true</hasSingleButton>  <hasKeys>true</hasKeys>
HandleSingleButtonLowPowerStandby -- Enter Low Power Standby on control key release
```

so one of these two is the "control key", whose press-and-hold crosses
`EVT_SYSTEM_SINGLE_BUTTON_PnH_THRESHOLD` and whose *release* then sleeps the speaker.
Neither of them is `KEY_VAL_POWER`, so the control key is one of these wearing a second
job, and which one has not been read yet.
