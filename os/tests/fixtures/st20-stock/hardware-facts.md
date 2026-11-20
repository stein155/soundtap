# ST20 hardware facts (root SSH, read-only, 2026-08-08)

## Identity and boot
- `Linux spotty 3.14.43+ #137`; boot args: `variant=spotty variant_raw=1612`
  and `variant_mode=normal`.
- Same 256 MiB NAND and Ping/Pong partition table as ST10. Running rootfs is Ping
  (`ubi.mtd=8`).

## Kernel map
- AM335x INTC is active. Kernel tick is IRQ 68 (`gp_timer`).
- Active peripherals: ELM IRQ 4, EDMA IRQ 12/14, AFE IRQ 16, USB IRQs 18/19,
  I2C at `4819c000` (IRQ 30) and `44e0b000` (IRQ 70), UART3 IRQ 44, UART4 IRQ 45,
  IR IRQ 72 and GPMC IRQ 100.
- ST20 exposes two `snd_shelby` ranges (`0x48038000`, `0x4803c000`), four LED
  device nodes (`uledy`, `uledw`, `uledg`, `uledr`) and `/dev/shelby-keypad`.
- GPIO evidence: mute 49, amp standby 55, amp fault 56, OLED D/C 59, reset 82,
  chip-enable 88; front-panel strobes 17/20/28 and switch-ins 22/23/26/29.
