# 0114. soundtapd is the Bluetooth pairing agent, over godbus

Status: accepted — 2026-11-20

## Context

Bluetooth pairing has a policy ([ADR 0055](0055-household-token-and-windows.md)): pairing only
inside a window, audio profiles only, a device that pairs is trusted. A pairing agent is an
object on the D-Bus system bus that BlueZ calls, so the connection that answers must stay open.
Running the agent by feeding `bluetoothctl` on a FIFO and matching its coloured text, and the
window through `dbus-send`, fails silently: a command BlueZ removed, behind `2>/dev/null`, once
left the speaker opening a pairing window at every boot. A shell test can only grep for the
agent's answers, not check them. `dbus-send` cannot be an agent at all.

## Decision

soundtapd is the pairing agent. It speaks D-Bus through godbus (`github.com/godbus/dbus/v5`, the
usual Go D-Bus library, [ADR 0124](0124-third-party-code.md)).

`app/internal/sysbus` opens soundtapd's own private connections to the system bus: the
handshake (SASL EXTERNAL and Hello) bounded by a timeout; signals by match rule, delivered in
order on one bounded channel that drops when its reader falls behind, so replies never wait for
a slow reader; and the connection closes with its context, ending everything registered over it.

`app/internal/bluez` holds the policy and runs as one goroutine:

- It registers an exported `org.bluez.Agent1` method table as the default `NoInputNoOutput`
  agent; godbus refuses a call whose arguments are not Agent1's.
- Outside the window it answers no to every request. Inside the window it accepts just-works
  pairing and A2DP/AVRCP service requests only (UUIDs 0x110a to 0x110e). A device that pairs
  inside the window is trusted, and the window ends 15 s later.
- It opens and closes the window. Closing writes Discoverable false, then Pairable false, then
  Connectable true, and retries until all three are accepted.
- It sets the adapter alias to the speaker's name and clears all pairings on request.
- When bluetoothd leaves the bus (`NameOwnerChanged`), it registers again as soon as bluetoothd
  is back.
- It also reads the connected phone's AVRCP track and notices a lost link, for the source.

`btctl` (the paired list, and removing one pairing for the API and the factory reset) reads
BlueZ over the same library. `bt.sh` keeps what is a boot script: the TI attach, starting and
restarting bluetoothd, bluealsa and bluealsa-aplay together, and the watchers that wake its loop
when a phone streams, changes volume or changes track. Moving those watchers to Go is a separate
decision, taken when they cause trouble. `bt-ctl.sh`, `bt-source.sh` and `bt-nowplaying.sh` stay
shell.

The agent starts only in firmware mode and only when the model lists Bluetooth among its user
sources.

Alternatives that lost:

- **`bluetoothctl` with hardened text matching.** The approach that failed silently, and still
  untestable.
- **Our own D-Bus client.** avahi's D-Bus API ([ADR 0043](0043-mdns-through-avahi.md)) needs more
  of the protocol than BlueZ does, and two D-Bus stacks in one daemon is one too many; godbus
  covers the whole protocol and is maintained.
- **`dbus-send` for the reads.** One process per read, and its text parsed by hand.
- **A small C agent beside `bt.sh`.** A second language with no tests in CI, for the same few
  hundred lines.

## Consequences

- The agent's answers, the window, trusting, clearing and re-registering after a bluetoothd
  restart have Go tests against a fake BlueZ on a test bus: `app/internal/dbustest` routes real
  D-Bus messages between godbus connections.
- A name a phone chose is a typed string from the bus, never text read as a property.
- `bluetoothctl` is not the agent. The image keeps it for `bt-ctl.sh` and `bt-nowplaying.sh`, and
  `os/support/tool_commands.py` checks only the subcommands those use.
- soundtapd holds one long-lived bus connection for the agent and one per API read; godbus adds
  about 256 KiB to the ARMv7 binary.
- The model gate is no hardware proof: a working BlueZ adapter is still needed, per model, in
  [the hardware checklist](../hardware/hardware-test.md).
- A soundtapd that crashes takes the agent with it; BlueZ then has no agent and refuses
  pairing, which is the safe side. The agent registers again when soundtapd restarts.

## Code

- `app/internal/sysbus/sysbus.go`: `sysbus.Dial` and `sysbus.Conn`.
- `app/internal/bluez/agent.go`: `bluez.Agent` (`Agent.Run`, `Agent.Pair`, `Agent.Clear`) and the
  answers in `Agent.decide`; `bluez.Agent.OnTrack` and
  `app/internal/bluez/linklost.go`.
- `app/internal/btctl/btctl.go`: `btctl.Paired` and `btctl.Remove`.
- `app/cmd/soundtapd/bluetooth.go:bluetoothAgent`: the model gate and start.
- `app/internal/dbustest`: the test bus; `app/internal/bluez/agent_internal_test.go`.
- `os/overlay/opt/soundtap/bt.sh`: the stack and its watchers.
