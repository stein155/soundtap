# What the Wireless Link's LEDs do, read out of BoseApp

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

Read on 2026-09-19 out of `opt/Bose/BoseApp` from the stock rootfs this board shipped
with (`Update_ti_27.0.6.46330.5043500.scm.stu`, module `ubi.img`), plus `binky_sm2.dtb`
from the same image. The binary is stripped of local symbols but keeps its dynamic
symbol table and, more usefully, keeps a log line in front of nearly every LED
transition — Bose's own description of what the lamp is about to do. Where a line is
quoted below it is quoted exactly.

## The hardware: two lamps, four channels

`binky_sm2.dtb` node `shelbyleds` (`compatible = "bose, shelby-led"`) declares four
channels. Bose numbers GPIOs across the four AM335x banks as one flat space; Linux
numbers from each gpiochip's base in registration order, which on this board puts bank 0
at 610 and bank 2 at 544 (see `wla-gpio-numbering` — `mute_out` 72 → 552 is the same
arithmetic).

| channel | pad | Bose gpio | Linux gpio | kind |
|---|---|---|---|---|
| `uledw` | timer4 pad | — | — | dmtimer PWM, `ti,timer-pwm` on `timer@48044000` |
| `uledy` | `lcd_data11`, gpio2_17 | 81 | 561 | on/off |
| `rled6` | `lcd_data10`, gpio2_16 | 80 | 560 | on/off |
| `rled3` | `spi0_d1`, gpio0_4 | 4 | 614 | on/off |

Those four channels are two bi-colour LEDs. `BoseApp-binky.xml` says
`hasStatusLed=true` and `hasWifiLed=true`, and the CLI command the binary registers —
`binkyled blink 0-63 [amber,white,blue] [wifi or bt]` — names both lamps and all three
colours. The command's own parser is what pins the mapping down; at `0x46c5fa` it
compares the colour word, then the lamp word, and picks a device pointer:

| colour + lamp | member | meaning |
|---|---|---|
| `white` + `wifi` | `this+0x14` | Wi-Fi lamp, white channel |
| `amber` + `wifi` | `this+0x18` | Wi-Fi lamp, amber channel |
| `blue` + `bt` | `this+0x1c` | Bluetooth lamp, blue channel |
| `white` + `bt` | `this+0x20` | Bluetooth lamp, white channel |

Only the Wi-Fi white channel takes an intensity: every call on `this+0x14` passes 5 or
100, while `this+0x18`, `this+0x1c` and `this+0x20` are only ever handed `0xff` or
turned off. That matches the device tree exactly — `uledw` is the PWM channel and the
other three are plain GPIOs — so:

- Wi-Fi white = `uledw` (timer4 PWM)
- Wi-Fi amber = `uledy` (gpio 561)
- Bluetooth white and Bluetooth blue = `rled6` (gpio 560) and `rled3` (gpio 614)

**The one thing not established here is which of `rled6` and `rled3` is the blue one.**
Nothing in the binary or the device tree distinguishes them; it takes one look at the
speaker with one of the two driven. Everything else below is read, not inferred.

## The rest of the CLI

`binkyled` also registers `status`, `intensity`, `pulsing`, `blink`, `off` and
`disable` (the last one disables a PRU backend that other products use — the Wireless
Link does not: no init script loads `pruss_remoteproc`, and `rproc-pru1-fw` points at
`RhinoLed.out`, which belongs to a different product). `binkyled status` prints the
lamp's current state, and its vocabulary is the complete set of appearances:

```
WIFI LED:   blinking white | dim white | solid white | solid amber | off
BT LED:     solid white | pulsing blue | blinking white | off
SHELBY LED: blinking white | super-fast blinking amber | solid white | off
AUX LED:    solid white | off
```

The last two are views of the same two lamps, not extra hardware.

## Intensity and the standby rule

Every Wi-Fi-white transition reads a flag at `this+0x10` first and picks the intensity
from it:

- **100** when the speaker is on
- **5** when it is in standby

Both branches log, which is why the same transition appears twice in the strings, once
"on intensity" and once "standby intensity". Stock treats standby as dimmer, not off.

## The patterns, with their real timings

A pattern is a list of steps, each `(duration_ms, intensity)`, handed to the device with
a repeat count of `0xffff` — that is, forever. Read from the helpers at the addresses
given.

| state | helper | lamp | pattern |
|---|---|---|---|
| Wi-Fi connected | `0x46acbc` | Wi-Fi | amber off; white solid at 100 (or 5 in standby) |
| Wi-Fi connecting | `0x46b974` | Wi-Fi | white: 350 ms on, 350 ms off, repeat — "fast blink" |
| Wi-Fi disconnected | `0x46b8c8` | Wi-Fi | amber: 700 ms on, 700 ms off, repeat — "slow blink" |
| Update downloading | `0x46b830` | Wi-Fi | white: 700 ms on, 700 ms off, repeat |
| Update installing | `0x46b788` | Wi-Fi | white: 3000 ms on, 250 off, 250 on, 250 off, repeat |
| Wi-Fi setup (AP) mode | `0x46c0f8` | Wi-Fi | solid amber at full intensity; all source LEDs off |
| Setup (Homer/Gabbo) | `0x46ad0c` | Wi-Fi | white off, both BT channels off, amber `0xff` |
| Bluetooth on | `0x46ae14` | BT | blue off, white `0xff` — solid white |
| Bluetooth pairing | `0x46ae46` | BT | white off, blue handed a pulse animation |
| Bluetooth connected | `0x46aea0` | BT | blue off, white `0xff` |
| Aux on, and every Shelby state | `0x46ad6c` … `0x46ae2e` | BT | both BT channels off |
| Off | `0x46ac68` | both | all four channels off |
| Wi-Fi off | `0x46aca4` | Wi-Fi | amber off, white off; the BT lamp is left alone |
| **Standby** | `0x46af7c` → `0x46af58` | BT | both BT channels off, **and nothing else** |
| Bluetooth disconnected | `0x46af70` → `0x46af58` | BT | the same two lines |
| Wakeup, and countdown | `0x46aefe`, `0x46aed2` | both | blue and amber off, both whites `0xff` |
| Thumbs up/down | `0x46af40`, `0x46af88` | BT | both BT channels off |
| Play/pause/stop, next/prev, action unavailable, mute | `0x46b368`, `0x46b296`, `0x46b100`, `0x46b53a` | BT | one 250 ms off / 250 ms on blink of the white channel, **repeat count 1** |

`LED_IDLE_STATE` is the one entry that is not a pattern at all. It compares the view's
current source and, when that is Bluetooth, branches on the link sub-state at
`this+0x44` — 1, 2 and 4 each re-enter the switch at a different Bluetooth state rather
than touching a lamp. So "idle" on this box means "show whatever the Bluetooth link is
doing", which is what SoundTap already does by reporting the source directly.

Two of the others are worth reading twice.

**Standby does not darken this box.** `LED_STANDBY_STATE` tail-calls the same two
instructions as `LED_BT_DISCONNECTED`: turn off the two Bluetooth channels. The Wi-Fi
lamp is left exactly as it was, which is why every Wi-Fi transition carries the second,
dimmer intensity instead of an off — a sleeping Wireless Link still shows its network at
5%. SoundTap does the same thing, arrived at independently from the intensity flag.

**The Bluetooth lamp doubles as the acknowledgement lamp.** Play, pause, stop, next,
previous, mute and "that did nothing" all build the same pattern — 250 ms off, 250 ms
on — and hand it to the white Bluetooth channel with a repeat count of **1** rather than
the `0xffff` the steady states use. So it is a single blink, not an animation, and it is
gated on a `SHELBY_SOURCE` comparison, so it only fires while Bluetooth is the source.
SoundTap does this now, using the mechanism the ST10's empty-preset feedback already
used: `applyKey` opens a 500 ms window in `speakerstate` — the length of the blink — and
the LED loop shows the pattern while the window is open. No event plumbing was needed
after all, which is why it got built rather than left on this list. Volume and the
presets are deliberately not in the set: they have their own LED states, and on a box
with two lamps those states do nothing.

Bose's own words for the four Wi-Fi ones, from the binary:

```
SetLedWifiConnected - set WiFi LED to solid white, on intensity
SetLedWifiConnecting - set WiFi LED to white, fast blink, on intensity
SetLedWifiDisconnected - set WiFi LED to amber, slow blink, on intensity
SetLedUpdateInstalling - set Status LED to white, solid for 3 seconds, then double-blink, repeat
SetLedWifiSetupMode - set WiFi/Status LED to solid amber, On intensity
```

## The states that drive it

`CLedViewBinkyInterface::SetLed` is a switch over these, each case logging its own name
before calling the helper above. The full set, in the order the switch tests them:

```
LED_FACTORY_DEFAULT_STATE   LED_WAKEUP_STATE             LED_OFF_STATE
LED_WIFI_OFF_STATE          LED_WIFI_CONNECTING_STATE    LED_WIFI_CONNECTED_STATE
LED_WIFI_DISCONNECTED_STATE LED_UPDATE_DOWNLOADING_STATE LED_UPDATE_INSTALLING_STATE
LED_WIFI_SETUP_MODE_STATE   LED_STANDBY_STATE            LED_SETUP_HOMER_GABBO_STATE
LED_VOLUME_STATE            LED_VOLUME_MIN_MAX_STATE     LED_IDLE_STATE
LED_MUTE_STATE              LED_SHELBY_ON_STATE          LED_STATE_PRESET_SELECT_STATE
LED_SHELBY_LOADING_PLEASE_WAIT_STATE                     LED_SHELBY_PRESET_SET_STATE
LED_SHELBY_PRESET_EMPTY_STATE                            LED_SHELBY_ERROR_STATE
LED_SHELBY_SKIP_LIMIT_STATE LED_BT_ON_STATE              LED_AUX_ON_STATE
LED_BT_PAIRING              LED_BT_CONNECTING            LED_BT_CONNECTED
LED_BT_DISCONNECTED         LED_BT_CLEAR_PAIRED_LIST_STATE
LED_PLAY_PAUSE_STOP_STATE   LED_NEXT_PREV_STATE          LED_COUNTDOWN_STATE
LED_COUNTDOWN_OVER_STATE    LED_THUMPS_UP_DOWN_STATE     LED_ACTION_UNAVAILABLE_STATE
```

`SetLed` refuses two ways before it touches a lamp, and both matter for a faithful
copy: "ignoring due to same state" (no restart of a running animation when the state
does not change) and "ignoring due to counting down" (a countdown animation owns the
lamp until it finishes).

## What SoundTap does with it

Implemented and running. `am335x-soundtap-wla.dts` declares the four channels,
`app/internal/hw/led_wla.go` drives them from the same states as above, and `internal/hw/model` keeps
`LEDDir = ""` because none of this goes through the ST10's `shelby-leds` path.

Proven on the board on 2026-09-19, with a package built that day:

```
wla:amber:wifi  max=1    cur=0
wla:blue:bt     max=1    cur=0
wla:white:bt    max=1    cur=0
wla:white:wifi  max=100  cur=5
```

`max=100` on the white Wi-Fi channel is the PWM probing; `cur=5` is this speaker
sitting in standby with its network up, at exactly the intensity stock would use.

Still open: which of `rled6` and `rled3` is blue. The labels are a guess that costs one
look at a lit speaker to confirm or swap.

## The rate and the scale, from the driver rather than from the help text

The CLI line quoted above says `0-63`, and it is wrong. `shelby-led.ko` is the thing
BoseApp writes to, and its `led_write` settles both numbers:

```
ldrb   r3, [fp, #-29]        ; the intensity byte from userspace
cmp    r3                ; 0 is off
cmp    r3              ; 0x64
addls  r3, r3, r3, lsl #2    ; r3 * 5
movwhi r1xfc18           ; above 100: clamp to -1000
movthi r1xffff
lslls  r1, r3            ; r3 * 10
rsbls  r1, r1            ; match = -(intensity * 10)
bl     led_set_timer
```

`led_set_timer` calls `omap_dm_timer_set_prescaler(timer, 255)`, which disables the
divider, and `led_probe` calls `omap_dm_timer_set_source(timer, 0)` for the 24 MHz
system clock. The load is `-1000`. So:

- **the period is 1000 ticks of 24 MHz, which is 24 kHz** — `am335x-soundtap-wla.dts`
  now asks for 41667 ns to match, where it used to ask for 1 kHz;
- **the scale is 0-100**, because the match is `-(intensity * 10)` against a `-1000`
  load and anything above 100 is clamped. The 100 and 5 read out of `SetLed` are
  percentages, `max-brightness = <100>` is right, and the help text's `0-63` would
  make a reader "fix" a correct value into a dimmer one.

Both numbers come from the driver, which is what runs; the help string is the only
place `63` appears anywhere in this path.
