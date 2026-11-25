# Runbook

Using a running speaker: teaching the ST10 an infrared remote, adding it to Apple Home and
connecting Snapcast clients. Installing, the front-panel combinations and the way back to Bose
are in [the install guide](docs/owners/install.md).

## Teaching the ST10 an IR remote

On mainline Linux the ST10 uses `gpio-ir-receiver` and rc-core's `/dev/lirc0`.
soundtapd decodes the raw timings into press/release events; `/dev/ir` is the historical
stock interface. The [dated hardware observation](docs/hardware/st10/remote-control.md) covers
preset, power and volume on one remote. The supplied Bose map is `/etc/soundtap/ir.map`;
`/mnt/nv/ir.map` replaces it for another remote.

The steps below need an alpha (development) image with SSH access. Production images have
no SSH, so this is not an owner procedure. Restarting the daemon or playing a preset while
learning still needs explicit permission.

1. **Capture.** Restart soundtapd with `IR_LEARN=1` and press each key you want. Every code is
   logged and nothing is acted on:

   ```sh
   grep 'ir learn' /tmp/soundtap.log
   ```

2. **Map.** Write `/mnt/nv/ir.map`, one `code = ACTION` per line. Codes are decimal or
   `0x`-hex, and `#` starts a comment:

   ```sh
   printf '0x10 = POWER\n0x11 = VOLUME_UP\n0x12 = VOLUME_DOWN\n0x13 = PRESET_1\n' > /mnt/nv/ir.map
   ```

   Valid codes are `0x01`…`0xFF`. `0x00` is the driver's release marker and never arrives as
   a key, so a line binding it is refused. Valid actions are `POWER`, `VOLUME_UP`,
   `VOLUME_DOWN`, `MUTE`, `PLAY`, `PAUSE`, `PLAY_PAUSE`, `STOP`, `NEXT_TRACK`, `PREV_TRACK`,
   `AUX_INPUT`, `SOURCE_TOGGLE`, `BLUETOOTH` and `PRESET_1`…`PRESET_6`. Anything else is
   refused with a log line rather than bound to nothing.

3. **Run.** Restart without `IR_LEARN`. Unmapped codes stay inert and are logged once each,
   which is also a second way to read codes off the remote.

`MUTE` toggles the master mute. `NEXT_TRACK` and `PREV_TRACK` use the current source's
control path: Bluetooth AVRCP, Spotify session commands, AirPlay sender control where
available, or the speaker's UPnP folder queue. A live radio stream and AUX have no next
track. A sender may refuse a command; a decoded key does not prove the sender acted on it.

The decoder synthesises both edges, so a remote gets the front panel's volume ramp and
1750 ms preset hold. On the mainline path it reports a release after 200 ms without another
frame; soundtapd then waits `IR_RELEASE_GRACE_MS` (150 ms by default) and cancels the pending
release if the same code returns. Tune the grace only with an approved hands-on test. A
second tap of the same key before the release settles can read as one continued hold.
`IR_DEVICE=off` disables the receiver.

The recovery button combinations in the install guide are deliberately *not* available from
the remote: they are the way back in when the network is gone, and they need two keys at once.

## Adding the speaker to the Apple Home app

The speaker is an AirPlay 2 receiver, not a HomeKit accessory: Home adds it through its own
flow, and there is no setup code to scan.

1. In **Home → Settings → Allow Speaker & TV Access**, choose **Anyone on the Same
   Network** and turn the password requirement off. Skipping this is the usual cause
   of a misleading "could not connect to Wi-Fi" error later.
2. **Add Accessory → More options…** The speaker appears under the name set in
   SoundTap's settings. Pick it and assign a room.

Once added it is groupable with HomePods and an Apple TV, Siri can route playback to
it, and the volume slider in Home, Control Center and the phone's hardware buttons
all drive the master volume.

Two things will not work, and are not worth investigating: the Home tile's transport
buttons, and accurate playback status there. Both need vendor features Apple only
grants MFi-certified receivers ([ADR 0003](docs/decisions/0003-apple-home-via-airplay-no-cast-no-sonos.md)).
A secondary device in the home occasionally showing "No Response" has the same cause.

## Snapcast clients

External Snapcast clients connect to `tcp://SPEAKER_IP:1704`. Connected SoundTap speakers,
laptops and other clients appear under **Settings → Multiroom**, where grouping, volume and
mute are controlled. Add a client to this speaker's group to receive its source: discovery
alone does not enable audio distribution. The tap stays quiet without a follower and stops
feeding prolonged digital silence to avoid unnecessary traffic
([ADR 0013](docs/decisions/0013-multiroom-through-snapcast.md)). Docker Desktop clients are
discoverable there but need a separate host audio bridge to play through macOS CoreAudio.
