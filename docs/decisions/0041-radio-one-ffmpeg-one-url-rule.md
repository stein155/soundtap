# 0041. Radio: one ffmpeg and one media URL rule

Status: accepted — 2026-11-12

## Context

Internet radio, DLNA and announcements play a URL through ffmpeg on the speaker, and
shairport-sync links against libavcodec for AirPlay's AAC and ALAC. Two separate ffmpeg builds
would be two versions, hashes and configure lines to keep current. The project prefers popular,
widely used upstream projects where they exist, and Buildroot's ffmpeg package is one, but the
Buildroot recipe at the pinned `BUILDROOT_COMMIT` cannot build ffmpeg 9, which the security
fixes need.

ffmpeg runs as the unprivileged `radio` user ([ADR 0066](0066-production-hardening.md)) and
opens `file:`, `concat:`, `pipe:` and many other protocols as readily as http. A URL that
reaches it from the DLNA renderer, `/api/upnp/play`, `/api/speaker/notify` or a station could
name the speaker's own API on loopback, or a playlist that names a local file.

Radio over a speaker's Wi-Fi stalls now and then for longer than the few seconds of decoded
audio between the network and the DAC. On the reconnect, icecast servers send a burst of
already-played audio, which ffmpeg decodes from the start, so pieces repeat
([ST20 observation](../hardware/st20/hardware-contract.md#observations-behind-audio-choices)).

Stock played internet radio from a URL of the owner's own. `POST /api/upnp/play` treats a stream
as a file: a clean end stops playback, there is no read-ahead, recents cannot start it again,
there are no ICY titles and the display says UPnP. The TUNEIN source has all of that.

## Decision

### One ffmpeg build for radio and AirPlay

`os/package/soundtap-ffmpeg` builds ffmpeg once, at Buildroot master's version and hash. Radio
runs `/usr/bin/ffmpeg`; shairport-sync links against the same build's libavcodec. The package
lists the codecs, formats, protocols and filters it enables under `--disable-everything`, and
those lists are the source of truth: the player's filters, PCM output and the radio and DLNA
formats (FLAC and WAV included). Video, devices, hardware acceleration and swscale stay out.
When `BUILDROOT_COMMIT` carries ffmpeg 9, the package goes back to Buildroot's recipe while
keeping one shared build. `host-soundtap-plistutil` stays, because Buildroot packages libplist
for the target only and shairport-sync needs `plistutil` on the build host.

### One media URL rule, and ffmpeg speaks only http(s)

`upnp.MediaURL` is the one rule: http or https, a host, and every address the host resolves to
is neither loopback nor link-local. Private LAN addresses are allowed, because that is where a
DLNA server lives. The DLNA renderer, `/api/upnp/play`, `/api/speaker/notify`, stations and the
player all call it. Redirects are followed by soundtapd under the same rule on every hop
(`player.FFmpegURL`), and ffmpeg follows none itself. Every ffmpeg input gets
`-protocol_whitelist http,https,tcp,tls,crypto` (crypto for AES-128 HLS) before `-i`, so it
binds to the input and everything the input opens. Clips uploaded to `/api/speaker/notify` get
a loopback URL that soundtapd builds itself and does not pass through the rule.

### soundtapd reads a plain radio stream ahead and feeds ffmpeg on stdin

For a station that is plain, endless HTTP(S) audio, soundtapd opens the connection itself
(`app/internal/readahead`) and ffmpeg reads `-i pipe:0` with `-protocol_whitelist pipe`:

- The compressed stream goes through a queue bounded at 1 MiB. When it is full the reader stops
  and TCP pushes back. Playback starts as soon as ffmpeg has its first bytes.
- No byte for 15 s drops the connection; the reader reconnects with a 250 ms to 5 s backoff.
  After 60 s without data it ends the stream, ffmpeg exits, and the player's supervisor starts
  over, resolving the station URL again.
- After a reconnect the first bytes are held back until the last 4 KiB already queued turns up
  in them, and everything up to it is dropped, so playback resumes byte-continuous. The scan
  gives up when the burst goes quiet for 500 ms or after 1 MiB, and then queues the bytes as
  they are.
- Only a first response of HTTP 200 without Content-Length and with an audio/mpeg, audio/mp3,
  audio/aac, audio/aacp, audio/ogg or application/ogg type is read ahead. HLS, playlists, files
  with a length, ICY's non-HTTP greeting, non-public addresses and any connect error keep the
  direct-URL ffmpeg path, and so does UPnP, whose media are files.
- The connection sends no `Icy-MetaData`, so the body is pure audio; now-playing titles come
  from their own connection (`app/internal/icy`). Decoding (filter, rate, format) is the same on
  both paths.

### A stream URL of the owner's own is a station

A stream URL plays as source `TUNEIN`, with the URL as both its location and its station id;
`speaker.OwnStream` tells it from a TuneIn guide id, and the player opens the URL instead of
asking TuneIn. A `.pls` or plain `.m3u` is read for the streams it lists, because ffmpeg opens
neither. Everything that takes a station id takes such a URL (`POST /api/play`,
`PUT /api/presets/{slot}`, a backup), held to `upnp.MediaURL` at the API and again before it
reaches ffmpeg, and to 512 bytes at most.

Alternatives that lost:

- **Separate radio and AirPlay builds.** Two versions of the same library to maintain.
- **Buildroot's pinned ffmpeg recipe.** It cannot supply the ffmpeg 9 fixes.
- **Raising ffmpeg's `-rw_timeout` or reconnect options.** No buffer, and the burst still
  replays.
- **A bigger FIFO or Mixer buffer.** It would hold decoded PCM, ten times the bytes per second,
  which a 128 MiB board cannot spare.
- **ffmpeg's own `-buffer_size` or cache protocol.** Not in the cut-down build, and it does not
  drop the repeated burst.
- **Skipping the burst by timing or Range requests.** Live streams do not honour Range.
- **A new source for the owner's own stream.** It touches the player, display, lamps, recents,
  presets, backup and the frontend for no behaviour of its own.
- **The UPnP path for the owner's stream.** Telling a controller's file from a stream there
  adds the same branches.

## Consequences

- One ffmpeg version, hash and component list for both consumers; the program links
  dynamically against the shared libav build. Using a filter or protocol the package does not
  build fails a test that reads the package. A version or configuration change rebuilds the
  package and its consumers; a warm output tree is no evidence that an old artifact has it.
- A host that does not resolve is refused up front. IPv6 follows the IPv4 rule
  ([ADR 0134](0134-ipv6-on-the-speaker.md)): loopback and link-local addresses are refused like
  127/8 and 169.254/16, also with a zone; a ULA is allowed like a private IPv4 address and a
  global address like a public one. The DNS answer can still change between
  SoundTap's check and ffmpeg's own lookup. HLS variant playlists and segments are opened by ffmpeg
  itself, within the protocol whitelist. Who may call these routes at all is
  [ADR 0055](0055-household-token-and-windows.md).
- A stall shorter than the buffered audio is inaudible, and a reconnect does not replay the
  burst in the common case. The station lies up to the buffer behind live, the connect is one
  serial round trip before ffmpeg starts (bounded at 10 s), and the stream holds at most 1 MiB
  of RAM, plus up to 1 MiB while a reconnect scan runs. The reader must never get HLS or
  playlist URLs, and must never send `Icy-MetaData`.
- Reconnecting, read-ahead, presets, the speaker's keys, recents, multiroom and now-playing work
  for the owner's stream without a new source. `TUNEIN` means internet radio, and the app's
  `tuneInId` can hold a URL: code that builds something from it goes through `logoURL` and its
  siblings, not a guide-id assumption.

## Code

- `os/package/soundtap-ffmpeg/soundtap-ffmpeg.mk`: the one ffmpeg and its component lists;
  `os/package/soundtap-plistutil`: the host `plistutil`.
- `app/internal/upnp/media.go:MediaURL`: the URL rule (DNS check in `fetchableHost`).
- `app/internal/player/args.go`: `player.FFmpegProtocols`, `player.FFmpegURL` and the ffmpeg
  input (`playbackInput`); `app/internal/player/args_internal_test.go:TestPlaybackAudioFilterOnlyUsesBuiltFilters`.
- `app/internal/readahead/readahead.go:Open`: the read-ahead reader, used from
  `app/internal/player/player.go`.
- `app/internal/speaker/speaker.go:OwnStream`, `app/internal/web/radio.go:validOwnStream` and
  `app/internal/tunein/playlist.go:PlaylistStreams`: the owner's stream as a station.
