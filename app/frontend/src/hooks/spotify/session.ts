// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useState, type Dispatch, type SetStateAction } from 'react';
import { useI18n } from '../../lib/i18n';
import {
  SpotifyError,
  spotifyDisconnect,
  spotifyStatus,
  statusView,
  type SpotifyStatus,
  type SpotifyView,
} from '../../lib/spotify';

const STATUS_POLL_MS = 3000;

export interface SpotifySession {
  view: SpotifyView;
  statusLoaded: boolean;
  renew: () => void;
  disconnect: () => Promise<void>;
}

// The speaker's librespot session, polled until it is usable, so the tab turns ready by
// itself once the user picks the speaker in the Spotify app.
export function useSpotifySession(active: boolean): SpotifySession {
  const [status, setStatus] = useState<SpotifyStatus | null>(null);
  const [statusLoaded, setStatusLoaded] = useState(false);
  // Bumped by disconnect and by a lost session to start the status poll over: it stops
  // once the session is ready, so without this an open sheet would keep loading.
  const [session, setSession] = useState(0);

  useEffect(() => {
    if (!active) return undefined;
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      const s = await spotifyStatus().catch(() => null);
      if (!alive) return;
      setStatus(s);
      setStatusLoaded(true);
      if (statusView(s) !== 'ready') timer = setTimeout(poll, STATUS_POLL_MS);
    };
    void poll();
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [active, session]);

  const renew = useCallback(() => setSession((n) => n + 1), []);

  const disconnect = async () => {
    try {
      await spotifyDisconnect();
    } finally {
      setStatus(null);
      setStatusLoaded(false);
      renew();
    }
  };

  return { view: statusView(status), statusLoaded, renew, disconnect };
}

export type ErrorFallback = 'spotifyFailed' | 'spotifyLoadFailed';

export interface SpotifyErrorLine {
  error: string;
  setError: Dispatch<SetStateAction<string>>;
  showError: (e: unknown, fallback?: ErrorFallback) => void;
}

// The library's one error line. A lost session is no error to show: `renew` polls its
// status again until it is usable, so the view shows what to do and turns ready by itself
// once it recovers.
export function useSpotifyError(renew: () => void): SpotifyErrorLine {
  const { t } = useI18n();
  const [error, setError] = useState('');
  const showError = useCallback(
    (e: unknown, fallback: ErrorFallback = 'spotifyFailed') => {
      const code = e instanceof SpotifyError ? e.code : '';
      if (code === 'spotify_not_authenticated' || code === 'spotify_not_connected') {
        renew();
        return;
      }
      setError(code === 'spotify_content_unavailable' ? t('spotifyNoResults') : t(fallback));
    },
    [t, renew]
  );
  return { error, setError, showError };
}
