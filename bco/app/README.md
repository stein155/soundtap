# Own BCO application

The ST20's BCO (DM870) runs this app as the SCM's network card
([ADR 0031](../../docs/decisions/0031-bco-network-card.md)): our own first stage, stage 2 and
WiLD core, with no Bose bytes. The stock BFL and BSL stay untouched. The image ships with
SoundTap Core at `/usr/share/soundtap/bco-app.bcd`, and soundtapd installs it on the BCO
itself, once per image. `bcoflash` is the development tool for a manual flash.

## Build

Buildroot builds it ([`os/package/soundtap-bco-app`](../../os/package/soundtap-bco-app/)): `build-app.sh VARIANT SRC OUT` per variant, with
`link-own` the shipped image (the network card with our WiLD core linked in, `WILD_BIN` from
`bco/wild/fw`). `link-own-trace` is the same card with `-DBCO_DEBUG_TRACE`: the UART diagnostics
of scan, join and the receive rings (per-channel scan lines, a scan summary with every BSS, one
line per join state, a heartbeat every 3 s). Each UART character busy-waits while USB and the
receive rings wait, so it is a bench build only, flashed by hand with `bcoflash`, never shipped;
`WILD_OWN` selects our core, `BCO_DEBUG_TRACE` the traces. `bco/tools/bco-pack` wraps the two stages into a `.bcd` that BFL accepts (header byte
+0x28 = 2, the 0xb0-byte header).

`make test-bco` (part of `make test`; CI's `bco` job) runs the host unit tests and the first
stage's bring-up trace. The tests cover the app (`tests/`) and our WiLD core
(`bco/wild/fw/tests/`). The core's sources build unchanged on the host, under ASan and UBSan:
`bco/wild/fw/tests/host.h` maps its register block and mailbox at their own addresses (linked
without PIE, so its 32-bit addresses reach every global), and `bco/wild/fw/tests/core_sim.h`
puts a simulated host (rings, DMA), MAC (receive area, FIQ, TX-done, ACKs) and RF bus around
them. `air_fuzz_test.c`, in both test directories, fuzzes every parser of frames from the air
with a fixed seed, so a failure repeats.

## First stage

[src/first.c](src/first.c) does the clock (PLL 480 MHz, AHB 80 MHz) and SDRAM bring-up before
copying stage 2 to 0x60008000. BFL copies the image from +0x18 to SRAM 0x40da00 and runs it
there, so the first stage links at 0x40d9e8. The register values are hardware facts read from
the stock first stage; the code is ours. `check-first-init.py` runs the built `first.bin` under
Unicorn and asserts the exact write sequence in [first-init-trace.json](first-init-trace.json),
also for a BFL that left the PLL set differently. Stage 2 programs the switch matrix (words
A..E), as stock does. Proven on the ST20 2026-10-16.

The UART runs at a 24 MHz crystal: UART1 +0x08 = 0x56a = 2*AHB/115200-2. Its status bit 2 is not
"busy" on hardware, so each character waits out the poll budget (~42 ms); never use the UART on
a data path.

## Flashing while soundtapd runs

Never pause soundtapd (`kill -STOP`) for a flash: it is the only thing feeding the
hardware watchdog, and the speaker resets about 120 s later, mid-transfer. Instead, a flash
creates `/run/soundtap/bco-flash` for as long as it runs. soundtapd's BCO LAN watch then does
not power-cycle the BCO, which otherwise aborted XMODEM transfers about 45 s in. `bcoflash`
does this itself. Other tools must
`touch /run/soundtap/bco-flash` before and `rm -f` it after. A hold left by a dead tool
stops counting after 15 minutes.

## IRQ tick on hardware (2026-10-02, ST20)

Proven on hardware with a `-DTICK` hello build:

- Vectors are high: SCTLR = 0x00052078 at our entry (V=1, MMU/caches off). The vector slots
  at 0xffff0000 are writable; we point 0x18/0x1c at an SRAM trampoline (0x400100), as stock
  points them into SRAM 0x400000.
- The INTC stays dead (0x90 always reads 0) until the stock init words are written in
  stock order: 0x84 = 0xf0000, 0x80 = 0x100000, 0x84 = 1. Then IRQs arrive.
- IRQ entry: read 0x90 = line, EOI = write the line to 0x98. Lines: enable 0x180+4n,
  disable 0x190+4n (write-1).
- Timer1 = IRQ 6: 0x204 = 2 (stop), 0x214 = 0, 0x204 = 0x200 (no prescale), 0x224 = AHB/1000,
  0x204 = 0x20 (bit5 is the IRQ *mask*; set = no IRQ), 0x200 = 2 (run). No timer-side ack
  needed: 79 000 ticks in ~80 s, steady.
- With all lines enabled, lines 26..41 fire without a handler (devices BFL left armed);
  never enable lines blindly.
- UART status bit 2 is not "busy" on hardware (see the first stage above); the TX readiness
  bit is still to settle (the map says bit 1).

## WiLD (WLAN core) boot on hardware (2026-10-02, ST20)

These facts were proven with the stock WLAN firmware (one-shot bring-up app `src/wild.c`, in git
history); the app now loads our own core from `bco/wild/fw` the same way.

The app repeats the stock sequence (SuluWlanDevice::create + adapter init): clear 0xe0007040
bit 15 and 0xe0013028 bit 0, clock cycle 0xe0007020 = 2 / 0x80000, 6 ms, 0xe0007024 = 0x80000,
copy the blob to WILD SRAM 0x30000000, unmask 0x30200584 bit 15, release with
0xe0007024 = 2. It polls the mailbox at 0x3001fde4 without IRQ 15.

On hardware the core answered right away: type 4, argument 0x1d6bc (the descriptor
table offset predicted from the blob). The table holds ids 1..18 and 20..21; id 15 (TX ring
header) = 0x1ed9c and id 16 (RX ring header) = 0x1f988. The core consumed the host's cmd 4
(mb+2 back to 0). It sent no type 8 within 3 s. Before release, 0xe0007040 read 0x00f08000 and
0xe0013028 read 1. Stock was restored afterwards.

### Command rings, MIB, echo and passive scan (2026-10-02, ST20)

`src/wring.h` ports the stock ring helpers.
Each ring is a word buffer split into channels: a shared header in WILD SRAM plus a host copy,
with the other side's fields poisoned (0xaaaaaaaa / 0xbbbbbbbb). Hardware results:

- UMR0 at 0x500000 is a physical alias of SRAM 0x400000. Stock keeps the ring buffers there
  (TX 0x508600, 1000 words; RX 0x507220, 1250 words) and so do we.
- The MIB is shared memory: core groups 0 and 13..25 are read and written directly in WILD SRAM
  (descriptor ids 1..17) under a Peterson lock. FirmwareVersion read back 0x00a70506 through
  the stock metadata table at 0x603bc15c. Host groups 1..12 (ME/MM/HD/DP_STA) belong to the
  host: the BCO runs the 802.11 management layer and the core is the lower MAC.
- Command records are `{1, 0, len+8} {type, 0} {sub, flag} payload`; the doorbell is
  0x30200210 bit 2. The core runs modules 18/19/23 and echoes every other module back on the
  RX ring: type 0x7777 to module 3 came back unchanged.
- Passive scan, type 0xc1 to module 19 `{channel, 0, u16 time TU, phy 6, 0}`: the core answers
  0xc3 for every channel 1..13 and RxCorrectFrameCount (MIB 20:16) climbed 75 to 192. The
  frames were dropped (RxErrFullBuf 209) because the RX buffer rings are not fed yet.
- RX supply ring (stock 0x6003e23c / 0x60067c04): four 5-channel frame rings (descriptor 12,
  28 entries of 24 bytes) and a 4-channel supply ring (descriptor 13, 8 entries) in UMR0. The
  host publishes 0x6b8-byte buffers on channel 0 (entry word 1 = buffer) and takes them back
  on channel 3 (entry word 0 = buffer, u16 @+10 = length). The 0x38-byte descriptor sits at
  +0x94 (header offset 0x18, so the 802.11 header is at +0xac, the body at +0xcc, body length
  = length - 0xc4). Hardware: 105-134 frames per 13-channel scan reach the host, RxErrFullBuf 0.
  Only counters are printed; frame contents are never logged.

## Wi-Fi station

`src/wifi.c` runs the WiLD core as a WPA2-PSK station on 2.4 GHz and passes its data to BCO
link channel 1 as Ethernet frames under the device's own MAC (PersParam MAC0; without it the
radio stays off). `src/mgmt.c` serves the control messages of ADR 0031 on channel 0: SCAN
(async, poll until status 0), SET_PROFILE (SSID, PMK, 32 random bytes for the SNonce), FORGET
and STATUS, plus the setup AP of ADR 0031. On the SCM, `soundtap_bco` turns that into a
cfg80211 `wlan0` for `wpa_supplicant`; the PSK never reaches the BCO, only the PMK. The 4-way
and group handshakes run in `src/wpa.c`. The link events, roaming within the SSID, the regulatory
domain, the SCAN's SSID and the read-only PersParam listing (EVENTS, SET_PROFILE's tail,
SET_REGDOM, NV_INFO) are ADR 0135's; `tests/wifi_roam_test.c` runs them against the real
`wifi.c`.

The card's clock is the host's 1 ms USB frame number (`usblink_ms`): the timer IRQ does not fire
after `usblink_init`'s clock replay. USB starts before the radio because that replay also
resets the WiLD core. No monitor or injection mode, no caller-chosen frames, no deauth on air;
logs carry counters and types only.

Hardware 2026-10-06 (ST20): `wpa_supplicant` COMPLETED in 12 s, DHCP on `wlan0`, SoundTap
answers on :80 from the LAN, 1500-byte frames pass. Data frames carry the AP's rates fastest
first (MIB 09:09 StaRateSetData). With our own core (2026-10-16): RTT 10-15 ms, TCP ~23 KB/s.

These October figures are not a current performance guarantee: the own core has since gained
rate adaptation (ADR 0031), so assess RSSI reporting and performance against the specific app
and core build. `src/wired.c` implements the MACB wired port; the wired report of 2026-11-10
records DHCP at boot and after a BCO reflash, with cable unplug/replug still untested.
