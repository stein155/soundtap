// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useEffectEvent } from 'react';
import { storePreset } from '../lib/api';
import { isStreamUrl } from '../lib/station';
import type { Station } from '../lib/types';
import type { RecentItem } from '../lib/recent';
import type { Source } from '../lib/rooms';
import type { Room } from './useRooms';
import { useI18n } from '../lib/i18n';
import type { Block, Notify, ReportFailure } from './useErrorReporter';
import type { SelectedRoom } from './useSelectedRoom';
import { useSettling, type PlaybackContext } from './playback/context';
import { presetStores } from './playback/presets';
import { roomTransporter, useSourcePicks } from './playback/rooms';
import { starters } from './playback/starters';
import { transports } from './playback/transport';

// The HTTP status a failed request carries, if any.
const statusOf = (err: unknown) =>
  typeof err === 'object' && err !== null && 'status' in err ? err.status : undefined;

export interface Playback {
  playSlot: (slot: number) => Promise<void>;
  pick: (station: Station, slot: number | null) => Promise<void>;
  playRecent: (item: RecentItem) => Promise<void>;
  transport: () => Promise<void>;
  skip: (dir: 'previous' | 'next') => Promise<void>;
  canStore: boolean;
  storeCurrent: (slot: number) => Promise<void>;
  storeSpotify: (
    slot: number,
    item: { uri: string; name: string; artwork?: string | undefined }
  ) => Promise<void>;
  setSource: (source: Source) => Promise<void>;
  power: (on: boolean) => Promise<void>;
  roomTransport: (room: Room) => Promise<'empty' | undefined>;
}

// Everything that starts, stops or changes what a room plays. The selected room goes
// through useSpeaker's optimistic state, so a tap shows at once; any other room is
// asked directly and re-read a moment later. A request that fails says so (`failed`)
// and the speaker's real state is read back.
export function usePlayback(
  { live, speaker, lead, roomsState }: SelectedRoom,
  failed: ReportFailure,
  notify?: Notify,
  block?: Block
): Playback {
  const { t } = useI18n();
  const leadFailed = failed(lead?.name || '');

  // The request went through but the speaker could not play what it was given.
  const { gaveUp } = live;
  const reportGaveUp = useEffectEvent(() => block?.(t('playGaveUp')));
  useEffect(() => {
    if (gaveUp) reportGaveUp(); // once per failure: a new language must not replay it
  }, [gaveUp]);

  const { begin, settle } = useSettling(live);
  const c: PlaybackContext = {
    live,
    speaker,
    lead,
    roomsState,
    failed,
    leadFailed,
    notify,
    t,
    begin,
    settle,
  };
  const { playSlot, playStation, playRecent } = starters(c);
  const { stop, transport, skip, power } = transports(c);
  const { storeSpotify, canStore, storeCurrent } = presetStores(c);
  const setSource = useSourcePicks(c, stop);
  const roomTransport = roomTransporter(c, { transport, playSlot });

  // A station played, or stored when held onto a slot. Storing does not also play: that cut
  // off whatever the room was playing.
  const pick = async (station: Station, slot: number | null) => {
    if (!station.tuneInId) return;
    if (slot === null) {
      await playStation(station);
      return;
    }
    await storePreset(slot, station, speaker).catch((err: unknown) =>
      // The speaker refused the address itself: say so, not "refused that".
      statusOf(err) === 400 && isStreamUrl(station.tuneInId)
        ? notify?.(t('customHelpInvalid'))
        : leadFailed(err)
    );
    await live.refreshPresets();
  };

  return {
    playSlot,
    pick,
    playRecent,
    transport,
    skip,
    canStore,
    storeCurrent,
    storeSpotify,
    setSource,
    power,
    roomTransport,
  };
}
