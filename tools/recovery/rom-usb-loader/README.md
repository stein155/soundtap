# ROM-USB loader

`rom-usb-loader` boots an AM335x board from its mask ROM over USB: it serves the SPL (`MLO`) to the
ROM and a RAM U-Boot (`u-boot.img`) to that SPL, both checked against SHA-256 digests, over RNDIS
implemented in userspace with libusb 1.0. It writes no NAND itself.

Two scripts drive it; neither is meant to be replaced by calling the loader by hand:

- [`../rom-usb-restore.py`](../rom-usb-restore.py) repairs a boot chain on any model: `--usb-spl`
  with the SPL and the RAM U-Boot it builds for the run, and `--watch-file soundtap-rom-usb` with
  the one blob that U-Boot accepts.
- [`../rom-usb-read.sh`](../rom-usb-read.sh) reads one NAND range back with `--upload-dir`.

What is proven per model, and how each board reaches its ROM, is in
[Developer recovery tools](../../../docs/hardware/install-records.md#developer-recovery-tools). The ROM
route was developed on the Wireless Link; on the ST20 the ROM needs the busy pad held and gets the
ST20's own SPL. The U-Boot patches every build applies live in `os/bootloader/uboot/`.

## Build and test

Install libusb 1.0 (and, off macOS, OpenSSL) so `pkg-config` resolves them, then as an ordinary
user:

```sh
make -C tools/recovery/rom-usb-loader clean all test
```

Or in a container, which is the image `rom-usb-restore.py` runs the loader in by default:

```sh
tools/recovery/rom-usb-loader/build.sh test
```

The tests cover SHA-256 text handling, the IPv4/UDP, BOOTP/DHCP, TFTP and ARP parsing, the stage
logic and the SPL hand-off timing, against a fake frame layer. They do not open libusb or touch
hardware. They are not part of `make test`.

## Native Linux host

The loader runs as root, or with the exact udev rules for the ROM (`0451:6141`) and our SPL
(`0451:d022`), which give only members of `soundtap-usb` access:

```sh
getent group soundtap-usb >/dev/null || sudo groupadd --system soundtap-usb
sudo usermod -aG soundtap-usb "$USER"
sudo install -o root -g root -m 0644 \
  tools/recovery/rom-usb-loader/70-soundtap-rom-usb.rules /etc/udev/rules.d/70-soundtap-rom-usb.rules
sudo udevadm control --reload-rules
```

Start a new login session after the group change. `LIBUSB_ERROR_ACCESS` names the rule path.

## Receiving files from the board

`--upload-dir DIR` lets U-Boot's `tftpput` write into `DIR` during `--watch`. Without it the loader
answers no write request at all. Accepted names are plain file names: no directory separator,
nothing that walks upward. The board is not a trusted peer.

## Limits it enforces

- The ROM must enumerate as `0451:6141` and our SPL as `0451:d022`, each with the expected RNDIS
  interface layout. More than one matching device, or a changed physical port, stops the run.
- `MLO` is at most 128 KiB and must match the built-in approved digest, or with `--usb-spl
  --mlo-sha256` exactly the digest given. `u-boot.img` is at most 2 MiB and must match the
  mandatory `--uboot-sha256`. `--check` verifies both files and exits before any USB access.
- Every wait is bounded. TFTP retransmits only the current block, at most five times. The SPL
  hand-off waits at most 60 s after the ROM disconnects.
- Only exact BOOTP/DHCP and `octet` TFTP requests for the current file are answered.
- Logs carry elapsed time, stage, port path, blocks, byte counts and verified hashes, never
  artifact bytes.

The other modes in `rom-usb-loader.c` (`--stock-wla`, `--uart-handoff`, `--legacy-uboot` and the
USB/IP modes) are left from bringing the ROM route up against stock firmware. No script uses them.
