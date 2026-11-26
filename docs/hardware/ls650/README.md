# Lifestyle 600/650 SoundTouch module (`bardeen` / sm2)

Preview. Nothing on this page was seen on a Lifestyle. It comes from the stock packages
([ADR 0133](../../decisions/0133-more-soundtouch-models-from-stock.md)).

The installers refuse it: it has no way back ([Open](#open)). The code still carries the board.

**The ST300's module.** Stock's `bardeen_sm2` and `ginger_sm2` trees hold the same properties.

**Same link.** Stock starts the same A4VServer: UART0 at 921600, 128-byte frames, the same ops
and source list. Only the device type differs, 8 here and 9 on the ST300.

SoundTap runs it as the ST300 under its own name: the ST300's tree, kernel, boot chain and
`internal/lpm`, with `bootdelay=-2`. SoundTap's U-Boot knows it by the manufacturing name `bardeen`.

## What the console adds

- Op 0x8c `{bonded}`: bonded mode with a Bose Music console (`linus`/Eco2). Stock enters it
  when `ProductEco2ControlInfo.dat` exists. **Never send 1.**
- Op 0x83: update display. Op 0x82: the paired Bluetooth device, for the console.
- Its own source list, sent in bulk (ops 0x20–0x26, 0x30–0x39). `internal/lpm` logs these and
  ignores them.
- `usb0`: IP to the console's second processor ("ASOC"). The console's own package
  (`Update.avu`, a ZIP) shows what it is: Android 5.1.1 on a Marvell Berlin SoC with an aarch64
  3.14 kernel and the on-screen UI. The package also carries this module's stock STU and the
  console's DSP, HDMI and LPM images.
  - The module is 203.0.113.1/30 and the console 203.0.113.2, over g_ether. dnsmasq hands out
    the address, and stock NATs it out.
  - ASOCUpdate flashes that processor over HTTP on :8080. SoundTap does not set up `usb0`; what
    the console does without it is unknown.

## Never

- STP_Update, or A4VServer's STP commands (the console processor's `bardeen_*.bos`).
- ASOCUpdate, or `/perform_ota_update`.
- Op 0x08 (reboots the processor), op 0x0c, or factory default (source 28).
- Writing `ProductEco2ControlInfo.dat`.
- Exposing ports 8890–8900.

## Open

- **The ASOC's network.** Giving the console's Android processor internet over `usb0` as
  stock does takes several things:
  - an Ethernet gadget in production;
  - a DHCP server;
  - NAT in the kernel (`NFT_NAT`/`NFT_MASQ`, not in the image);
  - a firewall rule that forwards the console's traffic.

  That opens a path through the speaker for a device SoundTap does not control (ADR 0113), for
  a need nobody has seen. It waits for an owner who can say what the console does without it.
- **Recovery.** Stock U-Boot has no recovery gesture for bardeen, and the console drives no
  GPIO lines the way the ST300's processor does. A way back must be proven before any write.
- Whether the console's own firmware answers every op as the ST300's processor does.
- The TV return path.
