# ST10 hardware facts (root SSH, read-only, 2026-07-21)

Pulled live off the real speaker via root SSH (old dropbear: needs
`-o HostKeyAlgorithms=+ssh-rsa -o KexAlgorithms=+diffie-hellman-group1-sha1
-o Ciphers=+aes128-cbc,3des-cbc`).

## Identity (info.xml)
- type SoundTouch 10, fw **27.0.6**.46330 (2022-08-04), moduleType **sm2**, variant **rhino**
- deviceID 020000C3D410, margeAccountUUID 0000000, country/region GB

## NAND / UBI geometry  — matches os/installer/build-stu.py exactly
- PEB (mtd erasesize) = **131072**, writesize/min_io = **2048**, oobsize = 64, subpagesize = **512**
- ubi0/ubi1 eraseblock_size (LEB) = **126976**  (= 131072 − 2×2048)
- build-stu.py: UBI_PEB=131072, UBI_LEB=126976, UBI_MIN_IO=2048 → ✓ correct
- NOTE: ubinize uses `-s 2048`; box NAND subpagesize is 512. Non-blocking (VID hdr at
  min_io is the safe default); verify on first flash — Ping/Pong makes a bad attach recoverable.

## Partition layout — A/B Ping/Pong (flash is brick-safe)
NAND.SPL(.backup2/3), NAND.U-Boot, NAND.U-Boot-Env, NAND.Kernel-Ping, NAND.Kernel-Pong,
NAND.File-System-Ping, NAND.File-System-Pong. mtd8 rootfs = 0x6000000 (96 MiB).
→ a failed STU flash falls back to the other slot instead of bricking.

## Kernel bring-up map (read-only SSH, 2026-08-08)
- Boot args: `variant=rhino variant_raw=3630 variant_mode=normal`, root `ubi.mtd=8`.
- AM335x INTC is live (128 IRQs); its kernel virtual address is `0xfa200000`.
- Scheduler tick is IRQ 68 (`gp_timer`); kernel log says timer2 and timer1 run at 24 MHz.
- Active hardware IRQs include ELM 4, EDMA 12/14, AFE 16, USB 18, UART3 44,
  MMC/SDIO 64, I2C1 71, IR 72, UART2 74, GPMC 100 and WL18xx GPIO 175.
- Wi-Fi is an SDIO card on `48060000.mmc`; running firmware identifies `wl18xx` PG 2.2.

## Drivers — 42 .ko on box are BYTE-IDENTICAL to os/stock (md5 diff empty)
vermagic `3.14.43+ mod_unload modversions ARMv7 p2v8`. Our shipped drivers ARE the box's
genuine drivers → they load. Confirmed on hardware, not just static.

## STILL UNKNOWN (needs the audio probe / a flash)
- DAC device path: does `/dev/snd_shelby` exist, or is it an ALSA `hw:X` card? (snd-shelby =
  McASP ASoC card driver). soundtapd/internal/audio assumes /dev/snd_shelby S16_LE 48k exclusive.
- Alpine/musl userland booting on the stock kernel — only a flash proves it.
- soundtapd actually driving the DAC (BoseApp is deleted on poc/soundtap-only) — QEMU only tests mix math.
