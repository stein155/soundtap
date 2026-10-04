# 0132. The kernel loads modules only while the speaker boots

Status: accepted — 2026-11-20

## Context

The WiLink and `hci_uart` drivers are built in, with `TIInit_11.8.32.bts` in
`CONFIG_EXTRA_FIRMWARE`. The image keeps four modules, the ST300's CPSW switch (`ti_cpsw_new`,
`cpsw-common`, `davinci_mdio`, `phy-gmii-sel`): built in, they would cost every model about
100 KB of the 4.75 MiB kernel limit. The kernel has `CONFIG_MODULES=y`, so without a guard any
root process could load a module of its own.

Module signing needs a key. A key the build generates makes every build different (the kernel
embeds its public half and every module carries a signature by it), against the reproducible
build of [ADR 0011](0011-one-buildroot-pipeline.md).

## Decision

The kernel is built without `MODULE_SIG`. The modules are files of the root filesystem and are
covered as every other file there: the signed package names its digest
([ADR 0048](0048-signed-updates.md)), the installer reads the written volume back, and `/` is
mounted read-only ([ADR 0066](0066-production-hardening.md)). `os/board/rootfs/post-build.sh`
removes every module but the four.

After `start_wired`, the step that loads the ST300's modules, the init script writes `1` to
`/proc/sys/kernel/modules_disabled` on a mainline kernel (`lock_modules`), which nobody can undo
before the next boot. Every `modprobe` of a mainline boot belongs before that step.

Alternatives that lost: signing with a per-build key (no two builds the same, so nobody could
rebuild a release and compare it); a fixed key in the repository (public, so anyone could sign
a module every speaker would load); a long-lived signing key in CI secrets (key handling, the
risk of a leaked key signing modules for every speaker, and a release only the key holder
could rebuild); building the four modules in with `CONFIG_MODULES=n` (about 100 KB of kernel
on every model for the ST300 alone).

## Consequences

- After boot a root process cannot load a module, from the image or from anywhere else. It can
  still do anything else root can do: this closes one route and does not replace the read-only
  root.
- Before the lock only the init script runs, from the read-only root, and it sources nothing
  user-controlled.
- A driver that a later step needs as a module loads before `lock_modules` or is built in. A
  failed write is logged and the boot goes on, with loading left open.
- A stock (3.14) kernel is left open: it loads the WiLink modules later.

## Code

- `os/overlay/etc/init.d/soundtap`: `lock_modules`, called after `start_wired`.
- `os/board/image/linux.fragment`: no `CONFIG_MODULE_SIG`.
- `os/support/kernel_config_check.py`: refuses `MODULE_SIG`.
- `os/board/rootfs/post-build.sh`: keeps only the ST300's modules.
- `os/tests/boot/test_lock_modules.py`: every `modprobe` comes before the lock.
