# 0107. A stereo pair is a multiroom group with a channel per speaker, and acts as one speaker through its leader

Status: accepted — 2026-11-03

## Context

Owners asked for two speakers as left and right, of any two models (an ST10 left and an ST20
right, not only two of the same model). Multiroom already keeps two speakers in step
([ADR 0013](0013-multiroom-through-snapcast.md)): the follower plays the leader's stream. What it
lacks is that each speaker plays only its own side, and that the two behave as one: in a plain
group a follower's preset leaves the group, its power key switches only itself, a leader in
standby releases its followers when the owner chose "dissolve groups", and the group volume
slider moves the followers only when "sync volume" is on. Stock treats the transport keys as the
group's and the rest as the pressed speaker's, which suits a living-room group, not a pair meant
to be one speaker.

Stock Bose ships separate left and right DSP images for the ST10. They are not reused: an
unpaired speaker must sound exactly like the stock stereo tuning, and a channel choice in the
Mixer works the same on every model.

## Decision

### A pair is a group plus a channel

A pair is a multiroom group of two speakers in which each speaker has a channel setting:
`stereo` (default), `left` or `right`. It is stored per speaker in the settings file, set
through `GET`/`PUT /api/channel`, and applied in the Mixer after the multiroom tap and before
bass, so the leader still sends both channels to its follower. A `left` speaker copies the left
channel over the right, a `right` speaker the reverse.

There is no pair state beyond the group (`multiroom-server`) and the channel, and both survive a
reboot. The settings page pairs by inviting the partner to follow this speaker and setting
opposite channels on both; unpairing makes the follower leave and sets both back to `stereo`.

### A pair acts as one speaker

A speaker is half of a pair when its channel is `left` or `right`. For such a speaker:

- A follower forwards `POWER` and `PRESET_1`..`PRESET_6` to its leader as well as the
  transport keys, through `POST /api/key` (the leader accepts them only when it is paired
  too). Volume and mute keys stay on the speaker pressed: balance between the halves is the
  owner's to set.
- The leader going to standby does not release its follower, whatever the owner's "keep
  groups" preference says; the follower already follows the leader's power.
- The group volume slider moves the follower by the same step, whatever "sync volume" says.

The UI shows a group as one card named after its speakers, so a pair is one room there.
AirPlay, Spotify and Snapcast see the leader as the one endpoint.

Alternatives that lost: reusing stock's left and right DSP images (an unpaired speaker would not
sound like stock, and it works only per model); a pair record on both speakers with partner id
and side (a second state to keep consistent with the group, which carries the same fact);
forwarding every key (volume and mute are per speaker on purpose).

## Consequences

- Any two models pair, with no new endpoint and no model-specific branch. A pair inherits
  multiroom's timing, so left and right are as close as multiroom keeps two speakers.
- A speaker left on `left` or `right` after its partner disappears keeps playing one side; the
  settings page shows that state and offers to end it.
- Powering on a follower that sits in standby wakes it alone (no forward from standby); it
  rejoins when its leader plays.
- Left/right synchronisation still has to be measured on two real speakers; the
  [hardware test](../hardware/hardware-test.md) has no pair run yet.

## Code

- `app/internal/audio/channel.go`: `audio.Channel` and `audio.Mixer.SetChannel`.
- `app/internal/web/channel.go`: `getChannel` and `putChannel` (`/api/channel`);
  `settings.Store.SetChannel` stores it.
- `app/internal/web/transport_key.go`: `paired`, `keyAllowed` and `Server.ForwardKey`, the keys
  a paired follower forwards.
- `app/internal/web/groupprefs.go:volumeSyncWanted`: the group volume slider.
- `app/internal/multiroom/followers.go`: `multiroom.Group.ReleaseFollowers` and
  `multiroom.Group.SyncFollowerVolume`.
- `app/internal/web/multiroom_leader.go:WatchLeaderPower`: the follower follows the leader's
  power.
