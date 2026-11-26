# SoundTouch 20

The ST20 has two CPUs. The **SCM** (TI AM335x, Linux) is the main CPU and runs SoundTap
Core, like the ST10. The **BCO** (BridgeCo DM870) is the speaker's network card: on stock
it owned Wi-Fi and Ethernet and NATed the LAN to the SCM; under SoundTap it runs SoundTap's own
application image and passes frames to the SCM over USB
([ADR 0031](../../decisions/0031-bco-network-card.md)). Boot, install and update follow the
ST10 ([ADR 0009](../../decisions/0009-mainline-linux-own-bootloaders.md)).

- [hardware-contract](hardware-contract.md): the display, codec, amplifier and keypad facts
  code must keep to.
- [aux-codec-route](aux-codec-route.md): how stock routes AUX through the codec.
- [rom-usb-recovery](rom-usb-recovery.md): bringing back an ST20 whose boot slot hangs,
  through the mask ROM's USB boot.
- [bco-network-card](bco-network-card.md): the BCO link protocol, the setup access point and
  the Wi-Fi data path in SoundTap's WiLD core, and their bench measurements.
- [bco-recovery](bco-recovery.md): BCO recovery limits and the supporting stock-script evidence.
- [standby-clock](standby-clock.md): how stock's standby clock walks against burn-in, and that SoundTap's does the same.
