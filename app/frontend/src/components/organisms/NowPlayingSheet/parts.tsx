// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { Button, Heading, ToggleButton } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { TransportGlyph } from '../../molecules/TransportGlyph';
import { useI18n } from '../../../lib/i18n';
import {
  setSpotifyLiked,
  setSpotifyRepeat,
  setSpotifyShuffle,
  spotifyLiked,
  spotifyModes,
  type SpotifyModes,
  type SpotifyRepeat,
} from '../../../lib/api/playback';
import { setUPnPRepeat, setUPnPShuffle, upnpModes } from '../../../lib/api/upnp';
import type { NowPlaying } from '../../../lib/types';
import { sourceName, type PlaybackView, type Transport } from '../../../lib/playback';

interface TopBarProps {
  view: PlaybackView;
  onClose: () => void;
  onPresets: () => void;
}

// Close, the source with its mark, and the way to the presets.
export function TopBar({ view, onClose, onPresets }: TopBarProps) {
  const { t } = useI18n();
  return (
    <div className="np-top">
      <Button className="top-btn" aria-label={t('close')} onPress={onClose}>
        <Icon.chevronDown />
      </Button>
      <Heading slot="title" className="np-src">
        {view.isSpotify && <Icon.spotifyMark aria-hidden="true" />}
        {view.source === 'TUNEIN' && <Icon.tuneinMark aria-hidden="true" />}
        {sourceName(view, t)}
      </Heading>
      <Button className="top-btn" aria-label={t('yourStations')} onPress={onPresets}>
        <Icon.presets />
      </Button>
    </div>
  );
}

interface TransportProps {
  action: Transport;
  label: string;
  // An input (Bluetooth, AUX) plays from outside: no play or stop.
  canControl: boolean;
  // Spotify's previous and next track.
  skips: boolean;
  onPress: () => void;
  onSkip: (dir: 'previous' | 'next') => void;
}

// The big play/stop with the skips beside it, on what can skip.
export function TransportControls({
  action,
  label,
  canControl,
  skips,
  onPress,
  onSkip,
}: TransportProps) {
  const { t } = useI18n();
  return (
    <div className="np-ctl">
      {skips && (
        <Button className="skip" aria-label={t('prevTrack')} onPress={() => onSkip('previous')}>
          <Icon.skipPrev />
        </Button>
      )}
      {canControl && (
        <Button className="big" aria-label={label} onPress={onPress}>
          <TransportGlyph action={action} />
        </Button>
      )}
      {skips && (
        <Button className="skip" aria-label={t('nextTrack')} onPress={() => onSkip('next')}>
          <Icon.skipNext />
        </Button>
      )}
    </div>
  );
}

const NEXT_REPEAT: Record<SpotifyRepeat, SpotifyRepeat> = {
  off: 'context',
  context: 'track',
  track: 'off',
};
// A phone can change these too, and the speaker sends no event for it.
const MODES_POLL_MS = 5000;

// The + of Stockholm: what plays goes in Liked Songs, a second tap takes it out again.
// Hidden for what cannot be liked. A tap that fails goes back.
function LikeButton({ speaker, track }: { speaker?: string; track?: string }) {
  const { t } = useI18n();
  const [state, setState] = useState({ available: false, liked: false });
  // Read again for every track: whether it is liked is the track's.
  useEffect(() => {
    let stale = false;
    spotifyLiked(speaker).then(
      (s) => {
        if (!stale) setState(s);
      },
      () => undefined
    );
    return () => {
      stale = true;
    };
  }, [speaker, track]);
  if (!state.available) return null;
  const toggle = (on: boolean) => {
    setState((s) => ({ ...s, liked: on }));
    setSpotifyLiked(on, speaker).catch(() => setState((s) => ({ ...s, liked: !on })));
  };
  return (
    <ToggleButton
      className="np-mode"
      aria-label={t('spotifyLiked')}
      isSelected={state.liked}
      onChange={toggle}
    >
      {state.liked ? <Icon.check /> : <Icon.plus />}
    </ToggleButton>
  );
}

interface ModesApi {
  read: (speaker?: string) => Promise<SpotifyModes>;
  shuffle: (on: boolean, speaker?: string) => Promise<unknown>;
  repeat: (mode: SpotifyRepeat, speaker?: string) => Promise<unknown>;
}

const SPOTIFY_MODES: ModesApi = {
  read: spotifyModes,
  shuffle: setSpotifyShuffle,
  repeat: setSpotifyRepeat,
};
const FOLDER_MODES: ModesApi = { read: upnpModes, shuffle: setUPnPShuffle, repeat: setUPnPRepeat };

// Spotify's shuffle and repeat, with the + before them.
export function SpotifyModeButtons({ speaker, track }: { speaker?: string; track?: string }) {
  return (
    <ModeButtons speaker={speaker} api={SPOTIFY_MODES}>
      <LikeButton speaker={speaker} track={track} />
    </ModeButtons>
  );
}

// A media-server folder's shuffle and repeat, as stock's queue had them.
export function FolderModeButtons({ speaker }: { speaker?: string }) {
  return <ModeButtons speaker={speaker} api={FOLDER_MODES} />;
}

// The shuffle and repeat of what plays: Spotify's, or a media-server folder's.
export function ModeButtonsFor({ now, speaker }: { now?: NowPlaying | null; speaker?: string }) {
  if (now?.source === 'SPOTIFY') return <SpotifyModeButtons speaker={speaker} track={now.track} />;
  if (now?.source === 'UPNP' && now.canSkip) return <FolderModeButtons speaker={speaker} />;
  return null;
}

// Shuffle and repeat. Repeat steps through off, everything and this track.
function ModeButtons({
  speaker,
  api,
  children,
}: {
  speaker?: string;
  api: ModesApi;
  children?: ReactNode;
}) {
  const { t } = useI18n();
  const [modes, setModes] = useState<SpotifyModes>({ shuffle: false, repeat: 'off' });
  // A tap moves this on: a poll that left before it must not put the old state back.
  const taps = useRef(0);
  const load = useCallback(() => {
    const tapsThen = taps.current;
    api.read(speaker).then(
      (m) => {
        if (tapsThen === taps.current) setModes(m);
      },
      () => undefined
    );
  }, [speaker, api]);
  useEffect(() => {
    taps.current++; // an answer for the speaker before does not count either
    load();
    const id = setInterval(load, MODES_POLL_MS);
    return () => clearInterval(id);
  }, [load]);
  const settled = () => {
    taps.current++;
    load();
  };
  const shuffle = (on: boolean) => {
    taps.current++;
    setModes((m) => ({ ...m, shuffle: on }));
    api.shuffle(on, speaker).then(settled, settled);
  };
  const repeat = () => {
    const mode = NEXT_REPEAT[modes.repeat];
    taps.current++;
    setModes((m) => ({ ...m, repeat: mode }));
    api.repeat(mode, speaker).then(settled, settled);
  };
  const repeatsTrack = modes.repeat === 'track';
  return (
    <div className="np-modes">
      {children}
      <ToggleButton
        className="np-mode"
        aria-label={t('shuffle')}
        isSelected={modes.shuffle}
        onChange={shuffle}
      >
        <Icon.shuffle />
      </ToggleButton>
      <ToggleButton
        className="np-mode"
        aria-label={repeatsTrack ? t('repeatTrack') : t('repeat')}
        isSelected={modes.repeat !== 'off'}
        onChange={repeat}
      >
        {repeatsTrack ? <Icon.repeatTrack /> : <Icon.repeat />}
      </ToggleButton>
    </div>
  );
}
