# 0115. Shell runs the boot and the recovery paths; decisions live in Go

Status: accepted — 2026-11-07; Wi-Fi moved to soundtapd 2026-11-21

## Context

The speaker image carries 13 shell scripts, about 2,700 lines. `etc/init.d/soundtap` alone
is 1,224 lines. Shell has no unit tests on this project (only `os/tests` greps and a few
`sh -n` checks), hides errors behind `|| true`, and runs as root. Some of it must still
work when soundtapd is broken or missing: bringing up the board, restarting soundtapd, and
giving a person a way in. Other parts make choices that soundtapd could make with tests: when
to release a source, what a Bluetooth event means, which track is playing.

soundtapd is the only Go process on the speaker, and the 128 MB models have a memory
budget. Each move into it has to be measured.

## Decision

Shell stays only where one of these is true:

1. it runs before soundtapd can start, or has to work when soundtapd does not (boot,
   supervision, recovery consoles);
2. a third-party daemon calls it as a hook, and it only passes the event on.

Any other decision (state, timing, parsing, retries) belongs in soundtapd, with tests.
Scripts move one at a time, one PR each. Manual model-specific hardware validation (ADR 0117)
proves each move, and soundtapd's RSS on a 128 MB speaker is measured before and after.

Wi-Fi is not a boot path. The setup AP serves soundtapd's page and gives no lease before
soundtapd answers, so without soundtapd it never was a way back in; a boot whose soundtapd
does not answer is rolled back by U-Boot's boot count ([ADR 0010](0010-ab-slots-boot-confirm-rollback.md)).
Its daemons outlive a soundtapd restart, so a crash does not take the network. `wifi.sh` and
`wifi-event.sh` therefore moved into soundtapd's Wi-Fi manager (`app/internal/wifi`,
[ADR 0063](0063-wifi-lease-and-setup-ap.md)).

Each script that stays runs under `set -eu` where it is a command of its own, and has a test
that runs it (a function sourced with stub commands, or the script against a fake tree);
`init.d/soundtap`'s functions are sourced through its library mode (`SOUNDTAP_INIT_LIB=1`,
`os/tests/boot/init_script.py`) instead of being cut out with regular expressions.

| Script | Lines | Verdict | Reason |
|---|---|---|---|
| `etc/init.d/soundtap` | 1224 | stays, shrinks | Boot and supervision must work without soundtapd. Policy that only matters once soundtapd runs (snapserver choice, codec-script copy, identity) moves out as each part is touched. |
| `opt/soundtap/wifi.sh` | 600 | removed | Join, fallback, setup AP, regulatory country and DHCP are soundtapd's Wi-Fi manager over wpa_supplicant's control socket (ADR 0063). |
| `opt/soundtap/wifi-event.sh` | 21 | removed | wpa_cli's action script; the manager takes the CONNECTED event from the control socket itself. |
| `opt/soundtap/lan-allow.sh` | 90 | stays | The production filter's subnet set, called by the udhcpc hook, the init script's static ST20 `eth0` and the setup AP; `set -eu`, run by `os/tests/wifi/test_lan_allow.py`. |
| `opt/soundtap/bt.sh` | 478 | shrinks | Pairing agent, window, trust and name moved to soundtapd (ADR 0114). Stack start/stop stays. Its watchers go when the helpers below move. |
| `opt/soundtap/bt-source.sh` | 19 | moves | A2DP transport state is a D-Bus property soundtapd can watch over D-Bus (`internal/sysbus`). |
| `opt/soundtap/bt-nowplaying.sh` | 63 | moves | Parses `bluetoothctl player.show` text; soundtapd can read `MediaPlayer1.Track` directly. |
| `opt/soundtap/bt-ctl.sh` | 52 | moves | AVRCP play, pause, next and previous are `MediaPlayer1` calls. |
| `opt/soundtap/spotify-source.sh` | 128 | shrinks | It is a librespot hook, so a forwarder stays. The pause grace period and its stamp file move to soundtapd. |
| `opt/soundtap/airplay-source.sh` | 18 | stays | shairport-sync hook that only forwards. |
| `opt/soundtap/snapclient.sh` | 51 | removed | soundtapd starts snapclient and builds its command line at every start (ADR 0121). |
| `opt/soundtap/clock.sh` | 112 | stays | Sets the clock before TLS and soundtapd need it; the Wi-Fi manager calls it after a join through `init.d/soundtap sync-clock`. A library the init script sources, so no `set -e` of its own. |
| `opt/soundtap/ssh.sh` | 22 | stays | Alpha-only way in (ADR 0011) when soundtapd is broken. |
| `etc/init.d/gadget-console` | 82 | stays | USB recovery console, for when nothing else runs. |
| `udhcpc/default.script.d/soundtap-log` | 30 | stays | udhcpc hook: logs a lease, fixes the `resolv.conf` mode, keeps the lease's NTP servers and subnet; `set -eu`. |

## Consequences

- New behaviour that is a decision goes into Go. A review that finds new policy in shell
  sends it back.
- The Bluetooth helpers (`bt-source`, `bt-nowplaying`, `bt-ctl`) move next, on the D-Bus
  client from the Bluetooth work. After that `bt.sh` keeps only the stack.
- A daemon hook stays a few lines that post an event and exit.
- The boot script stays large, so the `os/tests` checks on it stay too. The Wi-Fi checks that
  cut functions out of `wifi.sh` are Go tests of `app/internal/wifi` now.

## Alternatives considered

- **Rewrite all shell in Go at once.** Rejected. It risks the recovery paths, cannot be
  proven on hardware in one step, and grows soundtapd's memory in one jump.
- **Leave everything in shell.** Rejected. Policy without tests is where regressions
  hide.
- **A second small Go binary for boot and Wi-Fi.** Rejected for now. It costs another
  resident process on a 128 MB board, and the boot path is mostly sequencing, which
  shell does well.
