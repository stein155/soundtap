# Wireless Link adapter

The Wireless Link (`binky`, model `wla`) shares the ST10's `sm2` module type and STU module
names, but it is a separate model: line out and AUX in instead of a speaker, Bluetooth on the
WL1835's UART side, two buttons and two lamps, and no codec or amplifier GPIOs to drive. It has
no serial port, so its console is the USB gadget. Its USB-stick recovery is **both buttons** at
power-on ([install guide](../../owners/install.md#usb-stick)).

- [buttons](buttons.md): the two buttons, read as gpio-keys-polled.
- [led-behaviour](led-behaviour.md): the two lamps and their timings.
- [bluetooth-and-analog-output](bluetooth-and-analog-output.md): Bluetooth and the line output.
- [audio-config](audio-config.md): stock's audio configuration for this board.
