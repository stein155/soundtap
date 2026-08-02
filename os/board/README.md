# SoundTap boards

The board files of the one profile, `os/configs/soundtap_defconfig`
([ADR 0011](../../docs/decisions/0011-one-buildroot-pipeline.md), [ADR 0040](../../docs/decisions/0040-repository-layout.md)):

- `common/`: device trees for every model and the global patch directory.
- `image/`: the kernel fragment, the per-model kernels in `/boot`, post-build and post-image steps.
- `rootfs/`: rootfs overlay, busybox fragment, kernel patches, UBI layout and module list.
- `nand-geometry.json`: the NAND layout every check and image step reads.
