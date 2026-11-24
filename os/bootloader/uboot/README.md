# U-Boot build inputs

These are not documentation. `build-wla-uboot.sh` and `build-wla-product-spl.sh`
copy them into the U-Boot tree at build time, and CI hashes them into the build
provenance:

| file | used by |
| --- | --- |
| `uboot-am335x-soundtap-wla.dts` | the board's device tree, copied into `arch/arm/dts/` |
| `uboot-2026.07-spl-usb-recovery.patch` | the SPL USB fallback, behind `WLA_SPL_USB_RECOVERY=1` |
| `soundtap-sig.c`, `soundtap-sig.h` | the statement verifier soundtapd's tests share (`app/internal/release/uboot_test.go`); the USB stick no longer checks a `.sig` (ADR 0130), so `board.c` does not call it |
| `soundtap-keys.h` | the release keys it trusts, generated from `app/internal/release` (`UPDATE_UBOOT_KEYS=1`) |
| `tweetnacl.c`, `tweetnacl.h` | TweetNaCl 20140427 as upstream ships it, its ed25519 (ADR 0124) |

They lived in `docs/hardware/wla/` until 2026-09-18, next to the notes that describe how they
were arrived at. Those notes stayed there; a file the build reads belongs with the
build.
