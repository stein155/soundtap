// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { initials, logoURL, stationColor } from './artwork';
import { SOURCE_DESCRIPTION, SOURCE_LABEL, sourceOf, stationOf, type Source } from './rooms';
import type { TranslateFn } from './i18n';
import type { NowPlaying } from './types';

// What the artwork square shows: a station's logo on its colour, an input's glyph, or
// the speaker itself.
export type Artwork =
  | { kind: 'station'; src: string | null; initials: string; color: string }
  | { kind: 'bluetooth' | 'aux' | 'tv' | 'speaker' };

// The one transport control a room offers: Spotify can pause and resume, everything
// else that plays can only stop, and a silent room starts.
export type Transport = 'play' | 'pause' | 'resume' | 'stop';

// What plays offers: Spotify pauses and resumes, the rest stops.
function transportOf(station: { spotify: boolean; paused: boolean }): Transport {
  if (!station.spotify) return 'stop';
  return station.paused ? 'resume' : 'pause';
}

// The line under radio that names no song: live, or paused; nothing for the rest.
function quietTail(radio: boolean, paused: boolean, t: TranslateFn): string {
  if (radio) return t('liveShort');
  return paused ? t('paused') : '';
}

export interface PlaybackView {
  artwork: Artwork;
  // The name of what plays (or of the input); "Silent"/"Standby" when nothing does.
  title: string;
  // The second line: artist and track, or what the input is.
  detail: string;
  // The short status of a room card: "Paused · NPO 3FM", "AUX", "Silent".
  status: string;
  // Something is audibly playing right now (equalizer bars).
  live: boolean;
  isSpotify: boolean;
  skips: boolean; // next and previous do something
  // The raw source of a station (TUNEIN, SPOTIFY, AIRPLAY, UPNP, ...); empty for inputs.
  source: string;
  transport: Transport;
}

// The speaker reports the name given to its AUX input in place of "AUX".
// A soundbar input reports which one (HDMI 1, Optical 1, ...) the same way.
function inputName(now: NowPlaying | null, source: Exclude<Source, 'wifi'>) {
  if (source === 'aux') return now?.stationName === 'AUX' ? '' : now?.stationName;
  return source === 'tv' || source === 'hdmi' ? now?.stationName : '';
}

// An input: its name and what it is, or the track where a phone on Bluetooth names one.
function describeInput(
  now: NowPlaying | null,
  source: Exclude<Source, 'wifi'>,
  t: TranslateFn
): PlaybackView {
  const named = inputName(now, source);
  const phone = source === 'bluetooth' ? { name: now?.stationName, track: now?.track } : {};
  const title = phone.track || named || t(SOURCE_LABEL[source]);
  return {
    artwork: { kind: source === 'hdmi' ? 'tv' : source },
    title,
    detail: phone.track
      ? [now?.artist, now?.album].filter(Boolean).join(' · ')
      : phone.name || t(SOURCE_DESCRIPTION[source]),
    status: title,
    live: false,
    isSpotify: false,
    skips: false,
    source: '',
    transport: 'stop',
  };
}

// A room's now-playing, turned into what every view of it shows, so the cards, the
// mini player and the now-playing screen can never disagree. `speaker` is the one whose
// proxy loads the artwork.
export function describePlayback(
  now: NowPlaying | null,
  t: TranslateFn,
  speaker?: string
): PlaybackView {
  const station = stationOf(now);
  const source = sourceOf(now);
  if (station) {
    return {
      artwork: {
        kind: 'station',
        src: logoURL({ logo: station.art, tuneInId: station.tuneInId }, speaker),
        initials: initials(station.title),
        color: stationColor(station.key),
      },
      title: station.title,
      detail: station.sub,
      status: station.paused ? `${t('paused')} · ${station.title}` : station.title,
      live: !station.paused,
      isSpotify: station.spotify,
      skips: station.skips,
      source: station.source,
      transport: transportOf(station),
    };
  }
  if (source !== 'wifi') return describeInput(now, source, t);
  const standby = !now || now.standby;
  return {
    artwork: { kind: 'speaker' },
    title: standby ? t('standby') : t('silent'),
    detail: '',
    status: t('silent'),
    live: false,
    isSpotify: false,
    skips: false,
    source: '',
    transport: 'play',
  };
}

// Brand names, not translated. Any other station source is internet radio.
const SERVICE_NAME: Record<string, string> = {
  SPOTIFY: 'Spotify',
  AIRPLAY: 'AirPlay',
  UPNP: 'UPnP',
};

// Which source a view is from, for the heading of the now-playing screen: the service
// of a station, the input, or "Standby".
export function sourceName(view: PlaybackView, t: TranslateFn): string {
  if (view.artwork.kind === 'station') return SERVICE_NAME[view.source] ?? 'TuneIn';
  if (view.artwork.kind === 'bluetooth') return t(SOURCE_LABEL.bluetooth);
  return view.artwork.kind === 'speaker' ? t('standby') : view.title;
}

// The small line under a title: "Artist · Album · Spotify", "Live · TuneIn",
// "Play from your device · Bluetooth". Nothing when nothing plays. Radio whose stream names no
// song says live, and paused radio says paused: its song is no longer current.
export function sourceLine(view: PlaybackView, t: TranslateFn): string {
  if (view.artwork.kind === 'speaker') return '';
  const radio = view.artwork.kind === 'station' && !SERVICE_NAME[view.source];
  const paused = view.artwork.kind === 'station' && !view.live;
  const tail = radio && paused ? t('paused') : view.detail || quietTail(radio, paused, t);
  return [tail, sourceName(view, t)].filter(Boolean).join(' · ');
}

// The same line on the now playing page, whose top bar already names the source.
export function detailLine(view: PlaybackView, t: TranslateFn): string {
  const pausedRadio = view.artwork.kind === 'station' && !SERVICE_NAME[view.source] && !view.live;
  return view.detail && !pausedRadio ? view.detail : sourceLine(view, t);
}

// The picture of what plays, for blurring behind a bar or a sheet; none for an input.
export const artSrc = (view: PlaybackView): string | null =>
  view.artwork.kind === 'station' ? view.artwork.src : null;
