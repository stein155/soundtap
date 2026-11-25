// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRef } from 'react';
import { getPresets, playPreset, selectUserSource, setPaused, stopPlayback } from '../../lib/api';
import { roomOn, sourceOf, stationOf, type Source } from '../../lib/rooms';
import { speakerArg, type Room } from '../useRooms';
import type { ReportFailure } from '../useErrorReporter';
import type { PlaybackContext } from './context';

// After an action, how long until the speaker's state is read back.
const REREAD_MS = 800;

const SOURCE_NAME: Record<Exclude<Source, 'wifi'>, string> = {
  bluetooth: 'BLUETOOTH',
  aux: 'AUX',
  tv: 'TV',
  hdmi: 'HDMI',
};

// Source picks go out one after another in tap order, so an earlier pick that is slow
// cannot land after a later one and leave the speaker on the older source.
// Wi-Fi joins the same line: its Stop must not overtake an input pick still
// on its way, or that pick lands last and wins.
export function useSourcePicks(
  { live, speaker, lead, leadFailed }: PlaybackContext,
  stop: () => Promise<void>
): (source: Source) => Promise<void> {
  const sourcePicks = useRef<Promise<unknown>>(Promise.resolve());
  const picksWaiting = useRef(0);
  return async (source: Source) => {
    if (!lead) return;
    // Leaving an input for the network sources means stopping the input.
    if (source === 'wifi' && sourceOf(lead.now) === 'wifi' && picksWaiting.current === 0) return;
    picksWaiting.current++;
    const pick = sourcePicks.current.then(async () =>
      source === 'wifi' ? stop() : selectUserSource(SOURCE_NAME[source], speaker).catch(leadFailed)
    );
    sourcePicks.current = pick;
    await pick;
    picksWaiting.current--;
    if (source !== 'wifi') live.nudge();
  };
}

// The transport button on another room's card: asked directly and re-read a moment later.
async function otherRoomTransport(
  room: Room,
  roomsState: PlaybackContext['roomsState'],
  failed: ReportFailure
): Promise<'empty' | undefined> {
  const target = speakerArg(room);
  const roomFailed = failed(room.name);
  const station = stationOf(room.now);
  if (station?.spotify === true) {
    await setPaused(!station.paused, target).catch(roomFailed);
  } else if (roomOn(room)) {
    roomsState.patch(room.id, { now: { standby: true } });
    // The household stream keeps the poll quiet, so nothing else would undo this.
    await stopPlayback(target).catch((err: unknown) => {
      roomsState.patch(room.id, { now: room.now });
      roomFailed(err);
    });
  } else {
    const presets = await getPresets(target);
    const first = presets?.findIndex(Boolean) ?? -1;
    if (!presets || first < 0) return 'empty';
    await playPreset(first + 1, true, presets[first], target).catch(roomFailed);
  }
  setTimeout(roomsState.loadState, REREAD_MS);
  return undefined;
}

// The transport button on any room card: pause or resume Spotify, stop what plays, or
// start the first preset. 'empty' when the room has no preset to start, so the page can
// offer a station instead of the tap doing nothing.
export function roomTransporter(
  { live, lead, roomsState, failed, begin, settle }: PlaybackContext,
  own: { transport: () => Promise<void>; playSlot: (slot: number) => Promise<void> }
): (room: Room) => Promise<'empty' | undefined> {
  return async (room: Room): Promise<'empty' | undefined> => {
    if (room.id === lead?.id) {
      if (roomOn(room)) {
        await own.transport();
        return undefined;
      }
      const first = live.presets.findIndex(Boolean);
      if (first < 0) return 'empty';
      await own.playSlot(first + 1);
      return undefined;
    }
    // A repeat tap on a room card is ignored until this one has settled.
    const key = `transport:${room.id}`;
    if (!begin(key)) return undefined;
    try {
      return await otherRoomTransport(room, roomsState, failed);
    } finally {
      settle(key);
    }
  };
}
