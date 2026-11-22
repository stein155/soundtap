# Music Assistant

[Music Assistant](https://music-assistant.io) plays Deezer, Tidal, Qobuz, YouTube Music,
Apple Music, your own library and radio to a SoundTap speaker. It needs no add-on and no
SoundTap setting. Music Assistant finds each speaker on its own, twice: as a **DLNA**
player and as an **AirPlay** player. SoundTap does not need Music Assistant to run.

## Which player to pick

| You want | Use | Music Assistant player settings |
| --- | --- | --- |
| One speaker, best quality | the DLNA player | Flow mode **on**, output codec **FLAC** |
| Several speakers playing the same | SoundTap's own multiroom, played to the group's leader over DLNA | as above |
| A Music Assistant group across SoundTap and other brands | the AirPlay player | the defaults |

### DLNA, flow mode on (recommended)

Historical results with Music Assistant 2.6.3 on WLA, ST20 and ST10 are summarised under
[Recorded Music Assistant checks](#recorded-music-assistant-checks). The summary does not
identify a date, SoundTap build or complete capture; these are not current-candidate passes.
It reports:

- Play, pause, stop, seek, next and previous, volume and mute all work.
- A pause, stop or volume change made on the speaker (a button, the remote, the SoundTap
  app) shows in Music Assistant within a second.
- The progress bar runs.
- FLAC goes to the speaker as FLAC, not transcoded to MP3.

In flow mode, Music Assistant sends one continuous stream, so tracks follow each other
without a gap. The speaker itself shows "Music Assistant" with Music Assistant's logo, not
the track. The track shows in Music Assistant.

### DLNA, flow mode off

With flow mode off, each track is its own stream. The speaker then shows the title, the
artist and the cover of each track. The trade-offs:

- About 2 s of silence between tracks.
- No seeking within a track.

### AirPlay

AirPlay works, and Music Assistant keeps AirPlay players in sync with each other. That makes
it the choice for a Music Assistant group that mixes SoundTap with other speakers. Music
Assistant sets each member's volume when it starts a group, so check the volume after
grouping.

## Groups

Group SoundTap speakers in the SoundTap app, not in Music Assistant. Then play to the
group's leader from Music Assistant over DLNA. Every speaker that follows the leader plays
along, in sync, through SoundTap's own multiroom.

Do not put the same speaker in a Music Assistant group and a SoundTap group at once. They
are two systems driving one speaker.

## When a speaker does not show up

In the recorded Music Assistant 2.6.3 setup, discovery ran every five minutes and did not
react to a speaker announcement. After a speaker restarts or updates, it can take up to five minutes
before Music Assistant shows it as available again.

## When a speaker shows up but does not respond

Sometimes, right after a speaker restarts or updates, Music Assistant 2.6 keeps the player
listed but cannot control it. Play does nothing, or changes made on the speaker no longer
show in Music Assistant. Reload the DLNA provider in Music Assistant's settings, or restart
Music Assistant.

## Recorded Music Assistant checks

Music Assistant 2.6.3 ran in Docker on a Raspberry Pi against the ST10, the ST20 and the WLA.
It found each speaker twice without any setting: as a DLNA renderer and as an AirPlay
receiver. The record has no date, SoundTap build or capture; it is a historical summary, not
a pass for a later candidate ([hardware checklist](../hardware/hardware-test.md)).

Measured first, then fixed in the DLNA renderer: GENA eventing (before it, a pause made on the
speaker showed in Music Assistant about 25 s late), a pause that keeps the source,
`SetNextAVTransportURI` for Music Assistant without flow mode, concurrent NOTIFYs, a running
`RelTime`, the DIDL's artist and cover, and FLAC in the rootfs's ffmpeg.

With flow mode on and FLAC output, all three speakers passed the same twelve checks: play and
the progress bar running; pause, resume and seek; next and previous; volume, mute and unmute
from Music Assistant; a volume change and a stop made on the speaker showing in Music
Assistant. Playing to a SoundTap multiroom leader made its followers play along.

Not working in that run: with flow mode off, about 2 s of silence between tracks and no seek;
in flow mode the speaker shows "Music Assistant" instead of the track; after a speaker restart,
Music Assistant can keep a player it cannot control.

Why SoundTap speaks DLNA and AirPlay to Music Assistant instead of shipping a dedicated
Music Assistant provider: [ADR 0003](../decisions/0003-apple-home-via-airplay-no-cast-no-sonos.md).
