# SoundTouch 10

The ST10 (`rhino`, TI AM335x `sm2` module) runs SoundTap Core on its one CPU. It has a serial
console, an infrared receiver and a keypad that userspace polls. Its USB-stick recovery is
**Volume Down + Preset 4** at power-on ([install guide](../../owners/install.md#usb-stick)).

- [hardware-contract](hardware-contract.md): Wi-Fi interfaces, audio link, codec and amplifier
  GPIOs, lamps, keypad and console facts code must keep to.
- [remote-control](remote-control.md): the infrared receiver, and the infrared contract the
  ST20 shares.
- [usb-recovery-and-rom](usb-recovery-and-rom.md): the boot ROM's USB route and the stick
  gesture.
- [boot-splash](boot-splash.md): the front-panel boot pattern.
- [stock-audio-path](stock-audio-path.md): how stock drives the codec and amplifier.
