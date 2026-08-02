#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

# Called by librespot via --onevent for player + sink events.
#   sink running            -> claim the SPOTIFY source (arbiter ownership)
#   sink temporarily_closed/closed -> paused or between tracks: release after a grace period
#   stopped / session_disconnected -> playback ended: release the source now
#   loading/seeked/paused   -> drop audio already queued for the DAC (a skip, a seek, a pause)
#   paused / playing        -> record the pause or resume, so /api/now follows the phone
#   track_changed           -> the same for a skip to a preloaded track, then push metadata
#   end_of_track            -> the next load is a natural advance: keep the queued tail
#   playing                 -> push now-playing metadata so /api/now shows the track
#   volume_changed          -> make Spotify's slider the SoundTap master volume
API="${API:-http://127.0.0.1}"
PAUSE_GRACE=${PAUSE_GRACE:-60}
PAUSE_STAMP=${PAUSE_STAMP:-/run/soundtap/spotify/paused} # soundtapd hands the directory to librespot's user
# Player events run in order on a separate thread. Sink events run synchronously
# on the player thread. With --disable-gapless, temporarily_closed is the boundary
# AFTER ALSA drained the old period, BEFORE any new PCM can be written. They live in the directory soundtapd hands to
# librespot's user: /run/soundtap is root's, and stamps there were never written.
EOT_STAMP=${EOT_STAMP:-/run/soundtap/spotify/eot}
FLUSHED_STAMP=${FLUSHED_STAMP:-/run/soundtap/spotify/flushed}
BOUNDARY_STAMP=${BOUNDARY_STAMP:-/run/soundtap/spotify/boundary}

# esc escapes a string for a JSON double-quoted value: drop control chars (which make
# invalid JSON), then escape backslash and quote.
esc() { printf '%s' "$1" | tr -d '\000-\037' | sed 's/\\/\\\\/g; s/"/\\"/g'; }

# post retries a few times so a source event isn't lost while soundtapd is (re)starting.
post() {
	for _ in 1 2 3; do
		wget -qO /dev/null --header='Content-Type: application/json' --post-data="$2" "$API$1" >/dev/null 2>&1 && return 0
		sleep 1
	done
	return 0
}

# post_now fires once with no retry and no wait. Used on the track-change path, which
# is latency-critical: retrying there would delay the metadata behind three sleeps.
post_now() {
	wget -qO /dev/null --timeout=1 --tries=1 \
		--header='Content-Type: application/json' --post-data="$2" "$API$1" >/dev/null 2>&1
}

case "${PLAYER_EVENT:-}" in
  sink)
    case "${SINK_STATUS:-}" in
      running)
        rm -f "$PAUSE_STAMP"
        post /api/source '{"source":"SPOTIFY"}'
        ;;
      temporarily_closed|closed)
        if [ "${SOUNDTAP_SPOTIFY_BOUNDARY:-0}" = 1 ] && [ "$SINK_STATUS" = temporarily_closed ]; then
          # play_request_id_changed is queued BEFORE this callback. Its hook sees
          # end_of_track first, even when the player thread reaches us sooner.
          # track_changed cannot arrive until this callback has returned.
          tries=0
          while [ ! -f "$BOUNDARY_STAMP" ] && [ "$tries" -lt 100 ]; do
            sleep 0.01
            tries=$((tries + 1))
          done
          # A missing marker after the bounded wait means the event hook failed; flush conservatively.
          boundary=$(cat "$BOUNDARY_STAMP" 2>/dev/null)
          rm -f "$BOUNDARY_STAMP"
          if [ "$boundary" != natural ]; then
            wget -qO /dev/null --timeout=5 --tries=1 \
              --header='Content-Type: application/json' \
              --post-data='{"source":"SPOTIFY","event":"flush_wait"}' "$API/api/source" \
              || echo 'SoundTap: Spotify audio boundary was not acknowledged' >&2
          fi
        fi
        # librespot 0.8 closes the sink (not temporarily) on every pause, so "closed"
        # is no session end: releasing on it put the speaker in standby and the next
        # play went nowhere. Releasing the amp immediately would cut a track
        # off mid-pause, but never releasing leaves SPOTIFY owning the amp forever:
        # the speaker never idles and the GPIO stays unmuted. Release only if
        # playback has not resumed within the grace period.
        mkdir -p "$(dirname "$PAUSE_STAMP")"
        pause_token=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n') || exit 0
        pause_tmp="${PAUSE_STAMP}.${pause_token}"
        printf '%s\n' "$pause_token" >"$pause_tmp" && mv "$pause_tmp" "$PAUSE_STAMP" \
          || { rm -f "$pause_tmp"; exit 0; }
        (
          sleep "$PAUSE_GRACE"
          [ "$(cat "$PAUSE_STAMP" 2>/dev/null)" = "$pause_token" ] || exit 0 # removed = playback resumed
          post /api/source '{"source":"SPOTIFY","event":"stop"}'
          rm -f "$PAUSE_STAMP"
        ) &
        ;;
    esac
    ;;
  play_request_id_changed)
    if [ "${SOUNDTAP_SPOTIFY_BOUNDARY:-0}" = 1 ]; then
      boundary=skip
      [ -e "$EOT_STAMP" ] && boundary=natural
      rm -f "$EOT_STAMP"
      mkdir -p "$(dirname "$BOUNDARY_STAMP")"
      printf '%s\n' "$boundary" >"$BOUNDARY_STAMP.tmp" && mv "$BOUNDARY_STAMP.tmp" "$BOUNDARY_STAMP"
    fi
    ;;
  stopped|session_disconnected)
    rm -f "$PAUSE_STAMP" "$BOUNDARY_STAMP" "$EOT_STAMP" "$FLUSHED_STAMP"
    post /api/source '{"source":"SPOTIFY","event":"stop"}'
    ;;
  end_of_track)
    # librespot finished decoding the track, but seconds of it are still queued for
    # the DAC. The load that follows is the natural advance, not a skip, and flushing
    # there cut the end off every song.
    mkdir -p "$(dirname "$EOT_STAMP")"
    : >"$EOT_STAMP"
    ;;
  loading)
    [ "${SOUNDTAP_SPOTIFY_BOUNDARY:-0}" = 1 ] && exit 0
    # A skip to a track that is not preloaded. librespot stops writing now and only
    # reports track_changed once the new track has downloaded. Flushing only then let
    # the previous track play on from the queue for seconds, then cut to the new one.
    # Either way this boundary is handled; track_changed must not flush it again.
    [ -e "$EOT_STAMP" ] || post_now /api/source '{"source":"SPOTIFY","event":"flush"}'
    rm -f "$EOT_STAMP"
    mkdir -p "$(dirname "$FLUSHED_STAMP")"
    : >"$FLUSHED_STAMP"
    ;;
  track_changed)
    if [ "${SOUNDTAP_SPOTIFY_BOUNDARY:-0}" = 1 ]; then
      # Initial loads (no running sink) also leave a request marker. Retire it;
      # never flush here: the new track may already be filling the source bus.
      rm -f "$BOUNDARY_STAMP" "$EOT_STAMP" "$FLUSHED_STAMP"
    else
      # A skip to a preloaded track has no loading event: flush here. After a loading
      # flush the new track is already streaming, so flushing again would cut its start.
      if [ -e "$FLUSHED_STAMP" ] || [ -e "$EOT_STAMP" ]; then
        rm -f "$FLUSHED_STAMP" "$EOT_STAMP"
      else
        post_now /api/source '{"source":"SPOTIFY","event":"flush"}'
      fi
    fi
    ;;
  seeked|paused)
    # Both mean "stop what you hear now"; the queue would otherwise play on for seconds.
    rm -f "$EOT_STAMP"
    post_now /api/source '{"source":"SPOTIFY","event":"flush"}'
    [ "$PLAYER_EVENT" = paused ] && post /api/source '{"source":"SPOTIFY","event":"pause"}'
    ;;
  playing)
    # Metadata alone does not lift a pause; the resume says so itself.
    post /api/source '{"source":"SPOTIFY","event":"play"}'
    ;;
  volume_changed)
    # Spotify uses an unsigned 16-bit scale. Validate before shell arithmetic; the
    # API clamps again and updates the one master gain used by every source.
    case "${VOLUME:-}" in ''|*[!0-9]*) exit 0;; esac
    [ "$VOLUME" -le 65535 ] || exit 0
    volume=$(( (VOLUME * 100 + 32767) / 65535 ))
    post /api/volume "{\"volume\":$volume,\"source\":\"SPOTIFY\"}"
    ;;
esac

# Where the track is, for the time bar on the now-playing page. Only a track change says
# how long it is, and starts it from the top.
case "${PLAYER_EVENT:-}" in
  track_changed)
    case "${DURATION_MS:-}" in ''|*[!0-9]*) ;;
      *) post_now /api/position "{\"source\":\"SPOTIFY\",\"positionMs\":0,\"durationMs\":$DURATION_MS}" ;;
    esac
    ;;
  playing|paused|seeked|position_correction)
    case "${POSITION_MS:-}" in ''|*[!0-9]*) ;;
      *) post_now /api/position "{\"source\":\"SPOTIFY\",\"positionMs\":$POSITION_MS}" ;;
    esac
    ;;
esac

case "${PLAYER_EVENT:-}" in
  track_changed|playing)
    # librespot exports NAME, ALBUM, ARTISTS (newline-separated), COVERS (space/newline URLs).
    track=$(printf '%s' "${NAME:-}" | head -n1)
    artist=$(printf '%s' "${ARTISTS:-}" | head -n1)
    art=$(printf '%s' "${COVERS:-}" | head -n1)
    [ -z "$track" ] && exit 0
    post /api/nowplaying "{\"source\":\"SPOTIFY\",\"track\":\"$(esc "$track")\",\"artist\":\"$(esc "$artist")\",\"album\":\"$(esc "${ALBUM:-}")\",\"art\":\"$(esc "$art")\",\"uri\":\"$(esc "${URI:-}")\"}"
    ;;
esac
