# 0052. soundtapd lifecycle: one operations lock, ordered shutdown, watchdog

Status: accepted — 2026-10-21

## Context

Four things rewrite a running speaker: a firmware install (and the soundtapd swap), an app
self-update, a backup restore and a factory reset. Two of them overlapping would put a second
writer on the same NAND. The mtd-utils children of an install can outlive a soundtapd that is
stopped or restarted. A process that leaves without its shutdown skips the fade, the amp-off and
the HTTP shutdown, and loses the volume and bass changes the prefs saver has not written yet. A
panic in any of about thirty goroutines kills the daemon, and its trace goes to a console nobody
reads.

All bootargs pass `omap_wdt.early_enable=1`, so the AM335x watchdog runs from boot. Without
something feeding it on health, a kernel hang, an OOM livelock or a wedged soundtapd needs a power
cycle, which is worst on the Wireless Link: it has no UART.

## Decision

### One operations lock

All four operations take one lock (`internal/oplock`): an `flock` on
`/run/soundtap/operation.lock` behind an in-process mutex. Every NAND writer inherits the locked
descriptor (`ExtraFiles`), so a writer that outlives soundtapd keeps the lock and a restarted
daemon answers 409 until the write is done. Writers are not killed with the daemon: an interrupted
install leaves the inactive slot half-written and unselected, the case A/B already covers
([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)).

### One way out

Code that wants soundtapd to restart sends SIGTERM to its own pid. The signal handler runs the
ordered shutdown: save prefs first (with their own one-second deadline), fade and amp off, cancel,
HTTP shutdown. The app updater's replacement waits for the old pid, at most 30 s.

### Panics recorded, not swallowed

Goroutines start through `internal/safego` (`goSafe` in `cmd/soundtapd`), which logs the panic,
writes the stack to `<home>/panic.log` (one trace, at most 32 KiB) and panics again. The
supervisor's restart is the recovery ([ADR 0121](0121-one-supervisor-per-process.md));
`GET /api/debug` shows the last trace. In `app/internal` every goroutine goes through `safego.Go`,
and a test fails on a bare `go` statement.

### The hardware watchdog stays armed, fed only while healthy

1. The init script never touches `/dev/watchdog`. `omap_wdt` marks the early-enabled watchdog as
   running, and the kernel feeds it until a process opens the device, for at most
   `CONFIG_WATCHDOG_OPEN_TIMEOUT=180` s, so it does not feed forever if userspace never takes
   over.
2. soundtapd opens it on firmware only (`SOUNDTAP_FIRMWARE=1`), sets a 120 s timeout and writes to
   it every 10 s while a health check passes: `GET /api/version` on its own primary listener
   within 5 s, and, when there is a mixer, the mixed byte count moving since the last check (the
   mixer writes silence all through standby). Three failures in a row stop the feeding; the board
   resets 120 s later unless health returns.
3. It never closes the device with the magic `V`. A crashed or restarting soundtapd leaves the
   watchdog counting; init starts it again and the new process takes over within the 120 s,
   including the restart after an app update.
4. A firmware install runs inside soundtapd and is fed for as long as soundtapd stays healthy.
5. A kernel oops is a panic (`CONFIG_PANIC_ON_OOPS=y`), and a panic resets the board (`panic=10`
   on the command line, `kernel/panic=5` from the init script, `CONFIG_PANIC_TIMEOUT=5`). An oops
   otherwise kills only the task that hit it, and the half-dead kernel can still pass the health
   check and be fed.
6. A reboot gives the next boot a fresh window, whenever soundtapd last fed the watchdog, so
   nothing feeds it or changes its timeout before a reboot. On a reboot through `reboot(2)` the
   kernel's `omap_wdt` shutdown hook stops WDT1 before `reboot: Restarting system`; the earlier
   `watchdog did not stop!` line only means soundtapd's descriptor closed without the `V`. A
   panic restarts without that hook, and WDT1 counts on into the reset. Either way the SoundTap SPL's
   `s_init` stops WDT1 and reloads it before it does anything else, for 120 s at a nominal
   32.768 kHz. It runs from the internal RC oscillator, so the real figure varies by board: the
   ST20 note gives about 100 s, and a Wireless Link reset about 90 s in. U-Boot leaves WDT1 alone
   (no `WATCHDOG_AUTOSTART`), so SPL, U-Boot and the kernel share that one window until
   `omap_wdt` probes and takes it over with `early_enable`. A hang anywhere in that window resets
   the board into U-Boot's boot count.

Alternatives that lost: one lock per operation (two could overlap); killing NAND writers with the
daemon (it buys nothing A/B does not cover); recovering a panicking goroutine in place (a speaker
missing a subsystem with no sign of it, where a restart is visible and tested); disarming the
watchdog at boot (nothing then watches the speaker); feeding the watchdog and setting the driver's
maximum timeout (24 h) just before a reboot (the shutdown hook and the SPL both throw that timeout
away, and a shutdown that hung after the ioctl would go unwatched for a day); U-Boot servicing
WDT1 (a hang inside U-Boot would then never reset, as on 2026-09-27).

## Consequences

- An operation started while another, or an orphaned writer, runs gets 409, never a second writer
  on the same NAND.
- `panic.log` is written only on a crash, so it costs no NAND wear in normal running.
- Missed health checks stop the feeding after three strikes; the remaining hardware timeout then
  resets the board. If opening the device or setting the timeout fails, soundtapd logs it and the
  watchdog guarantee does not hold on that board.
- An unconfirmed boot that keeps resetting falls back through U-Boot's boot count
  ([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)). Confirmation clears the count, so a fault
  that happens only after each boot is confirmed keeps resetting the same slot.
- Stopping soundtapd by hand for longer than 120 s resets the speaker.
- `-no-audio` runs and builds without a mixer check HTTP only. `-dev` (`make dev`, on a computer)
  refuses `SOUNDTAP_FIRMWARE=1`, so it never arms the watchdog, confirms no boot, writes no disarm
  file and moves no pstore record.
- An oops leaves a reset and its ramoops record instead of a speaker that answers some things and
  not others. The proof on a speaker is the watchdog route of the rollback check in
  [the hardware checklist](../hardware/hardware-test.md#a-boot-storage-and-rollback).

## Code

- `app/internal/oplock/oplock.go`: `Lock.TryLock`, `Lock.File`.
- `app/cmd/soundtapd/lifecycle.go:orderedShutdown`; `app/cmd/soundtapd/prefs.go:saveAudioPrefs`.
- `app/internal/safego/safego.go:Go`; `app/cmd/soundtapd/panic.go:writePanic`;
  `app/internal/safego/lint_test.go:TestNoBareGoStatements`.
- `app/internal/hw/watchdog.go:RunWatchdog`; `app/cmd/soundtapd/watchdog.go:speakerHealthy`.
- `os/board/image/linux.fragment`: the watchdog open timeout and panic on oops;
  `os/support/kernel_config_check.py` requires them. `os/overlay/etc/init.d/soundtap`: `kernel/panic`.
- `os/bootloader/uboot/uboot-2026.07-spl-board.patch`: `spl_watchdog_start`, the SPL's window;
  `os/bootloader/build-wla-uboot.sh` and `os/package/soundtap-bootloaders/configs/wla-uboot.config`:
  U-Boot without `WATCHDOG_AUTOSTART`.
