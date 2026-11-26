# 0110. Home automation goes through one optional MQTT bridge with discovery

Status: accepted — 2026-11-20

## Context

Owners want the speaker in their home-automation hub: volume, power, presets, what is playing,
and the update. Stock offers nothing for this. Hubs differ, but most of them speak MQTT and read
MQTT discovery messages, so one protocol reaches all of them. The speakers are small 32-bit ARM
boards, and every dependency adds to the image and to the code we must review.

## Decision

soundtapd has an MQTT bridge that is off until the owner turns it on in the settings. It is an
outbound client, never a listener, and speaks MQTT 3.1.1 through Eclipse Paho's Go client
(`github.com/eclipse/paho.mqtt.golang`, [ADR 0124](0124-third-party-code.md)): QoS 0 only,
plain TCP or TLS dialled by us so the context can cancel the handshake, a clean session, no
fallback to 3.1, and Paho's automatic reconnect off. The bridge reconnects every 15 seconds and
reads the settings again on each attempt; a settings change drops the connection and reconnects
at once, without restarting the daemon.

The speaker is one MQTT device made of standard discovery entities: a `number` for the volume,
`switch`es for power and mute, a `select` for the six presets, `button`s for the transport keys,
`sensor`s for station, track, artist, play status and version, and an `update` entity that
installs the latest signed SoundTap app the speaker is offered ([ADR 0048](0048-signed-updates.md),
[ADR 0118](0118-update-button-without-press.md)) without a press; it does not install Core.
Discovery goes to the configured prefix; topics live under `soundtap/<deviceID>` unless the owner
sets another base topic. A retained Last Will marks the speaker offline when the connection
drops. Playback state is polled every 5 seconds and only changes are published; update state is
checked at connection and every 6 hours.

The bridge subscribes to its command topics one by one and to `<prefix>/status`, never to
`<base>/#`, so the states it publishes do not come back to it. It ignores a message that arrives
with the RETAIN flag: the broker sets it only when it replays a stored message, and a command a
hub published retained would otherwise run again at every reconnect and boot.

The configuration lives in the speaker's settings file. The settings API and a backup restore
refuse settings the bridge cannot use (`settings.MQTT.Validate`). The broker password is left
out of API responses, backup downloads and diagnostics. The UI and docs say "MQTT", never the
name of one hub.

Alternatives that lost: our own MQTT 3.1.1 client (every corner of the protocol, keepalive, QoS 1
and 2 inbound, framing, was ours to get right and keep right); Paho's MQTT 5 client
(`paho.golang`, smaller, but brokers that speak only 3.1.1 would refuse the speaker); a native
integration per hub (own code and release cycle each, where discovery reaches them all); QoS 1
(acknowledgement tracking and resending for commands the hub or user can simply send again).

## Consequences

- One feature covers every hub that reads MQTT discovery. When it is off, no broker connection
  is made.
- `app/internal/mqtt` is a thin layer of options over Paho; its tests hold Paho to the bytes a
  broker must see against a broker written from the specification. Paho links websocket and
  proxy code the bridge never uses.
- QoS 0 means a command can be lost when the network drops; the state topics show what really
  happened.
- The broker password is stored in plain text on the speaker, readable with root, as the Wi-Fi
  password is.
- A command over MQTT is as trusted as one from the home network
  ([ADR 0055](0055-household-token-and-windows.md)): the broker decides who may publish.

## Code

- `app/internal/mqtt/mqtt.go`: `mqtt.Connect`, `mqtt.Options`, `mqtt.Will`, the Paho setup.
- `app/internal/mqttbridge/mqttbridge.go`: `mqttbridge.Bridge` (`Run`, `Reload`) and the retry
  timings.
- `app/internal/mqttbridge/discovery.go`: the discovery entities and the update state.
- `app/internal/mqttbridge/commands.go`: the command handling and the state cache.
- `app/internal/mqttbridge/topics.go`: the topic layout.
- `app/internal/settings/mqtt.go:MQTT.Validate`: what the settings refuse.
