# SoundTouch SA-5 (`burns` / sm2)

Preview. Nothing on this page was seen on an SA-5. It comes from the stock packages
([ADR 0133](../../decisions/0133-more-soundtouch-models-from-stock.md)).

## The board

The ST10's SM2 module on an amplifier board. Stock's `burns_sm2` tree is `rhino_sm2` with
these changes:

- **Rear Ethernet port:** wired as on the ST300 and the Series III.
- **Amplifier controller:** an STM32 at 0x1d on I2C1 (`bose,snd_burnspa`), beside the NCP6922.
  `stm32reset` is gpio2_6.
- **Lamps:** two, the Series III's. White is PWM on DM timer 4, amber is gpio2_17.
- **Codec scripts:** `burns_sm2_normal.cfg`, plus `burns_sm2_mono.cfg`.

SoundTap's U-Boot knows it by the manufacturing name `burns`. Its stock package is the SA-5's own
(device id 0x093A).

## Audio

Same as the ST10: McASP0 is the master at 46875 Hz (24 MHz /2/4 bit clock, 2×32-bit slots) into
the AIC3256, which feeds the amplifier board. The output is mono or bridged (left DAC only, left
inverted). The STM32 is not in the digital audio path.

## The controller (stock snd-burnspa, APServer)

**Framing:**
- Write: one message, `[reg, value...]`, little endian.
- Read: `[0x00, reg]`, then a separate read of the register's size.
- 200 µs between steps, up to five rounds.

| Register | Size | Meaning | Stock |
| --- | --- | --- | --- |
| 0x01 | 64 | Firmware version | read |
| 0x04 | 1 | ampState, 1 = on | polled every 100 ms |
| 0x06 | 1 | i2sClocksValid | **written**: 1 after the clocks run, 0 before they stop |
| 0x10 | 4 | Thermal attenuation, an IEEE-754 single | polled every second; the host applies it |
| 0x11 | 1 | sourceSelect, `'1'`..`'3'` | **written** |
| 0x12 | 1 | Interrupt status: bit 0 state changed, bit 4 fault | polled |

The interrupt line (gpio0_16) sits on a pad stock muxes elsewhere, so stock polls instead.

**Stock order:**
1. Clocks valid = 1.
2. ampstby = 1.
3. Wait for ampState 1.
4. Unmute 100 ms later.

Stopping runs the other way: mute, wait 1 s, standby, wait 1 s, clocks valid = 0.

**SoundTap** (`internal/hw/stm32amp`, `cmd/soundtapd/sa5.go`):
- sets clocks valid when it starts;
- polls the state and fault, and mutes on a fault;
- unmutes only 100 ms after ampState reads 1, as stock;
- trims the source bus by the thermal gain, as stock did in the miniDSP.

`Write` refuses every register except 0x06 and 0x11.

## Never

- I2C address 0x1e. That is the STM32's bootloader: a half-run flash leaves the amplifier
  without firmware.
- The driver's `update` flag, and `STM32SoftwareUpdate`.
- Registers 0x04, 0x05, 0x10, 0x12, 0x14, 0x15 or any other unknown register.
- Toggling stm32reset at runtime.

## Recovery

Its one front button (gpio0_27, stock's bit 9) held at power-on runs the USB-stick update in
SoundTap's U-Boot, as stock's does: two reads 50 ms apart must agree (`rt_single_button_held`).

## Open on hardware

- That the button reads low when held, and that the gesture runs.
- Which input sourceSelect 1–3 picks.
