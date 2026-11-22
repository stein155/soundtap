# The ST10's remote control

Source identity and missing provenance are listed in the
[RE source register](../../../os/stock/PROVENANCE.md#reverse-engineering-source-register).

The mainline kernel has no Bose IR driver. The receiver on gpio1_10 is a plain
`gpio-ir-receiver`. rc-core hands out raw edge timings on `/dev/lirc0`, and soundtapd
decodes Bose's frames itself (`app/internal/hw/ir_lirc.go`) against the 13 codes in
`/etc/soundtap/ir.map`, which came from BoseApp.

## Proven on hardware, 2026-09-23

On the bench ST10, PRESET_1 and then POWER were pressed, and later VOLUME_UP with the speaker in standby.
Every press was decoded and acted on:

```
ir code reading settled reading=reversed raw=0xf8 code=0x1f
button play key=PRESET_1         -> the preset-1 stream started
button key=POWER                 -> standby
button key=VOLUME_UP (x2)        -> volume 8 -> 10, silently, in standby
```

The first frame settles which of the four readings maps onto `ir.map`. On this remote that
is `reversed`. It is kept for the rest of the run.

Two observations from that September build:

- A preset needs a few seconds to buffer. POWER pressed two seconds after it stops the
  stream before any sound arrives, so from the room it looks as if nothing happened.
- That build logged `stream produced no audio after=25s` after standby. Current
  `app/internal/player/decoder.go` checks `StillBuffering` before failing the attempt, and
  `TestStandbyEndsBuffering` covers cancellation. The historical misleading line is not
  expected current behaviour.

A second reader on `/dev/lirc0` (`head -c … /dev/lirc0`) got nothing while soundtapd held
the device. Raw captures need soundtapd's IR stopped, or learn mode (`app/internal/hw/ir.go`).

## The infrared contract

Code that touches the remote control keeps to these rules on the ST10 and the ST20.
[The contract](../../../CONTRIBUTING.md#infrared-st10-st20) points here.

- Mainline ST10 and ST20 read the receiver with `gpio-ir-receiver` on gpio1_10 (uart0_rxd in
  mode 7) and rc-core's `/dev/lirc0`; soundtapd decodes the raw edge timings itself
  (`app/internal/hw/ir_lirc.go`). The measurement above is the ST10's: it is no ST20 proof.
- Receive only. Never add transmission.
- A frame carries two complementary bytes. Bit order is resolved against the key map by the
  first frame, as above. Held-key repeats are suppressed.
- The supplied Bose map is `/etc/soundtap/ir.map`; codes learned for a particular remote go in
  `/mnt/nv/ir.map`. Do not hard-code other consumer remotes' maps.
- A failed IR probe never blocks boot.

### The stock UART interface and the retained driver

Stock Linux reads the receiver differently, and this is not the mainline interface:
`bose,shelby-ir` on AM335x UART0 at `0x44e09000` (called UART1 in stock documentation),
48 MHz, IRQ 72. `/dev/ir` returns one byte per event, nonzero for a press and zero for a
release; an empty nonblocking read returns zero bytes and callers wait with poll/select. DMA
stays disabled on that path.

Our kernel keeps a bounded driver for that interface
(`os/board/rootfs/linux-patches/0008-misc-add-bounded-shelby-ir-driver.patch`); no SoundTap
device tree binds it. Any UART implementation that is kept must hold three invariants: reset
polling is bounded, FIFO draining is bounded and reads only while the LSR data-ready bit is
set, and the misc device, IRQ, timer and runtime-PM lifetimes are torn down in an order no
callback can outlive. The 2026-08 failure combined an inverted LSR loop, unbounded reset
polling and unsafe lifetime handling. A change to the driver adds regression checks around
these invariants; the tree has none yet.
