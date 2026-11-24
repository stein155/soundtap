# SoundTouch 300

The ST300 (`ginger`) runs the Wireless Link's module, kernel and boot chain with its own device
tree. soundtapd talks to the soundbar processor (LPM) over UART0
([ADR 0109](../../decisions/0109-st300-soundbar-processor-link.md)); U-Boot never listens there.
The processor's firmware is never written.

It has no proven recovery route: the USB stick's remote combo
([ADR 0130](../../decisions/0130-usb-stick-recovery.md)) has never run on a bar, and no way into
its ROM over USB is known.
[The contract](../../../CONTRIBUTING.md#soundtouch-300-ginger--sm2) lists what that rules out.

Its facts are spread over the shared pages:

- [Compatibility: SoundTouch 300 only](../../owners/compatibility.md#soundtouch-300-only): what
  works on the bar.
- [Install records: SoundTouch 300](../install-records.md#soundtouch-300): what has run on one.
- [Installing: SoundTouch 300](../../owners/install.md#soundtouch-300): what an owner needs to know.
- [Hardware test: SoundTouch 300 candidate validation](../hardware-test.md#soundtouch-300-candidate-validation):
  what a release must prove.
