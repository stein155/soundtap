# Upstream TI connectivity blobs

Binary firmware nobody here compiles. ADR 0047 allows that only with recorded
upstream provenance, and ADR 0049 rules out anything extracted from Bose NAND.

## TIInit_11.8.32.bts

The WiLink8 Bluetooth ROM initialization script. `hci_ll` requests it by the version it
reads off the chip after reset, as `ti-connectivity/TIInit_11.8.32.bts`, and without it the
controller comes up and fails its setup.

    source   https://github.com/TI-ECS/bt-firmware  (Texas Instruments' own organisation)
    path     TIInit_11.8.32.bts
    fetched  2026-09-16
    size     70313
    sha256   f7dfa9d8072eacf492414fabd35fb6287b18eb0c40bad8022d4aa0802dedfb78
    header   BTSB, "185x/189x PG2 ROM Initialization Script", ROM 8.0.32,
             last updated 17-Sep-2014

**It is not in linux-firmware.** That package ships TIInit_6.2.31, 6.6.15 and 7.2.31, all
WiLink6/7, and nothing for WiLink8, so Buildroot's `BR2_PACKAGE_LINUX_FIRMWARE_TI_WL18XX`
cannot supply it.

### Why not the copy on the speaker

Stock `/lib/firmware/TIInit_11.8.32.bts` is the one Bose ships, and it was
the obvious candidate: same name, and byte-identical between two different Bose products.
It is **not** the same file as this one -- 75439 bytes against 70313, and a header dated
24-Jul-2018 against 17-Sep-2014. Same description and the same ROM compatibility, so it is
almost certainly a later TI revision rather than something Bose wrote, but "almost
certainly" is not provenance and the only source we could cite for it is a Bose rootfs.
That is exactly what the Bose-free rule excludes, so this one is used instead.

If the 2014 revision ever turns out not to initialize this chip, the answer is to obtain
the later revision from TI, not to lift it off the speaker.
