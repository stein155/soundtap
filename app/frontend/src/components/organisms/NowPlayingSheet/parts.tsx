// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useRef, useState, type ReactNode, type ReactElement } from 'react';
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
import { usePageVisible } from '../../../hooks/usePageVisible';

interface TopBarProps {
  readonly view: PlaybackView;
  // The speaker or group that plays, after the source.
  readonly name: string;
  readonly onClose: () => void;
  readonly onPresets: () => void;
}

// Close, the source and where it plays, and the way to the presets.
export function TopBar({ view, name, onClose, onPresets }: TopBarProps): ReactElement {
  const { t } = useI18n();
  return (
    <div className="np-top">
      <Button className="top-btn" aria-label={t('close')} onPress={onClose}>
        <Icon.chevronDown />
      </Button>
      <Heading slot="title" className="np-src">
        {sourceName(view, t)} · {name}
      </Heading>
      <Button className="top-btn" aria-label={t('yourStations')} onPress={onPresets}>
        <Icon.presets />
      </Button>
    </div>
  );
}

interface TransportProps {
  readonly action: Transport;
  readonly label: string;
  // An input (Bluetooth, AUX) plays from outside: no play or stop.
  readonly canControl: boolean;
  // Spotify's previous and next track.
  readonly skips: boolean;
  readonly onPress: () => void;
  readonly onSkip: (dir: 'previous' | 'next') => void;
  // Shuffle and repeat of what has them, as the outer two buttons (ModesFor).
  readonly modes?: TransportModes | null | undefined;
}

// The big play/stop with the skips beside it, on what can skip, and shuffle and repeat on
// the outside for what has them.
export function TransportControls({
  action,
  label,
  canControl,
  skips,
  onPress,
  onSkip,
  modes,
}: TransportProps): ReactElement {
  const { t } = useI18n();
  return (
    <div className={modes ? 'np-ctl np-ctl-modes' : 'np-ctl'}>
      {modes?.shuffle}
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
      {modes?.repeat}
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
// Hidden for what cannot be liked. A tap that fails goes back. Beside the title.
export function LikeButton({
  speaker,
  track,
}: {
  readonly speaker?: string | undefined;
  readonly track?: string | undefined;
}): ReactElement | null {
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
// A media-server folder's shuffle and repeat, as stock's queue had them.
const FOLDER_MODES: ModesApi = { read: upnpModes, shuffle: setUPnPShuffle, repeat: setUPnPRepeat };

// Shuffle and repeat as two buttons, for the transport row to put on its outside.
export interface TransportModes {
  readonly shuffle: ReactElement;
  readonly repeat: ReactElement;
}

// The shuffle and repeat of what plays, Spotify's or a media-server folder's, handed to
// children to place; null for what has none (radio, an input).
export function ModesFor({
  now,
  speaker,
  children,
}: {
  readonly now?: NowPlaying | null | undefined;
  readonly speaker?: string | undefined;
  readonly children: (modes: TransportModes | null) => ReactNode;
}): ReactElement {
  let api: ModesApi | null = null;
  if (now?.source === 'SPOTIFY') api = SPOTIFY_MODES;
  else if (now?.source === 'UPNP' && now.canSkip === true) api = FOLDER_MODES;
  if (!api) return <>{children(null)}</>;
  // Another source starts from its own state, not the last one's.
  return <ModeButtons key={now?.source} speaker={speaker} api={api} render={children} />;
}

// Shuffle and repeat. Repeat steps through off, everything and this track.
function ModeButtons({
  speaker,
  api,
  render,
}: {
  readonly speaker?: string | undefined;
  readonly api: ModesApi;
  readonly render: (modes: TransportModes) => ReactNode;
}) {
  const { t } = useI18n();
  const [modes, setModes] = useState<SpotifyModes>({ shuffle: false, repeat: 'off' });
  // A tap moves this on: a poll that left before it must not put the old state back.
  const taps = useRef(0);
  // Not asked while the page is hidden; read again the moment it shows.
  const visible = usePageVisible();
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
    if (!visible) return undefined;
    taps.current += 1; // an answer for the speaker before does not count either
    load();
    const id = setInterval(load, MODES_POLL_MS);
    return () => clearInterval(id);
  }, [load, visible]);
  const settled = () => {
    taps.current += 1;
    load();
  };
  const shuffle = (on: boolean) => {
    taps.current += 1;
    setModes((m) => ({ ...m, shuffle: on }));
    api.shuffle(on, speaker).then(settled, settled);
  };
  const repeat = () => {
    const mode = NEXT_REPEAT[modes.repeat];
    taps.current += 1;
    setModes((m) => ({ ...m, repeat: mode }));
    api.repeat(mode, speaker).then(settled, settled);
  };
  const repeatsTrack = modes.repeat === 'track';
  const buttons: TransportModes = {
    shuffle: (
      <ToggleButton
        className="np-mode"
        aria-label={t('shuffle')}
        isSelected={modes.shuffle}
        onChange={shuffle}
      >
        <Icon.shuffle />
      </ToggleButton>
    ),
    repeat: (
      <ToggleButton
        className="np-mode"
        aria-label={repeatsTrack ? t('repeatTrack') : t('repeat')}
        isSelected={modes.repeat !== 'off'}
        onChange={repeat}
      >
        {repeatsTrack ? <Icon.repeatTrack /> : <Icon.repeat />}
      </ToggleButton>
    ),
  };
  return <>{render(buttons)}</>;
}
