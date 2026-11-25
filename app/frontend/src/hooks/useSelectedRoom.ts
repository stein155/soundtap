// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useMemo, useRef, useState } from 'react';
import { keepRooms, useRooms, speakerArg, type Room, type RoomsState } from './useRooms';
import { useSpeaker, type SpeakerLive } from './useSpeaker';
import { followDevice, setTargetDevice, targetDevice } from '../lib/base';
import { sourceOf } from '../lib/rooms';
import type { NowPlaying, Player } from '../lib/types';

// A paused room shows paused; otherwise playing once the speaker plays, else buffering.
function shownStatus(reported: NowPlaying | null, player: Player): string {
  if (reported?.playStatus === 'PAUSE_STATE') return 'PAUSE_STATE';
  return player.status === 'playing' ? 'PLAY_STATE' : 'BUFFERING_STATE';
}

// The selected room's now-playing as the room cards read it, built from the live
// speaker state: the optimistic station while one is starting, then the speaker's own.
function liveNowPlaying(raw: NowPlaying | null, player: Player): NowPlaying {
  const station = player.station;
  const reported = raw && !raw.standby ? raw : null;
  if (station) {
    return {
      ...reported,
      standby: false,
      source: reported?.source || station.source || 'TUNEIN',
      stationName: station.name,
      art: station.art,
      tuneInId: station.tuneInId,
      track: station.track,
      artist: station.artist,
      playStatus: shownStatus(reported, player),
    };
  }
  if (reported && sourceOf(reported) !== 'wifi') return reported;
  return reported ? { ...reported, playStatus: 'STOP_STATE' } : { standby: true };
}

// A grouped room plays on its leader.
function leaderOf(rooms: Room[], room: Room): Room {
  return (room.linkedTo && rooms.find((r) => r.id === room.linkedTo)) || room;
}

// The room this browser drives: the remembered one, else the speaker serving the page,
// and in a group always its leader.
function leadFor(rooms: Room[], key: string): Room | undefined {
  const chosen = rooms.find((r) => speakerArg(r) === key) ?? rooms.find((r) => r.self);
  return chosen && leaderOf(rooms, chosen);
}

export interface SelectedRoom {
  rooms: Room[];
  lead: Room | null;
  members: Room[];
  speaker: string;
  live: SpeakerLive;
  roomsState: RoomsState;
  select: (id: string) => void;
}

// Every room, and the one this browser looks at. Playback lives on a group's leader, so
// the selected room is always a leader; it is followed live (useSpeaker, over SSE) while
// the others are polled (useRooms). The choice is kept per browser.
export function useSelectedRoom(): SelectedRoom {
  const [selectedKey, setSelectedKey] = useState(targetDevice);
  const [leadId, setLeadId] = useState<string | null>(null);
  const roomsState = useRooms(leadId);
  const polled = roomsState.rooms;
  // A remembered room that is gone would leave the page driving nothing; fall back to
  // the speaker serving the page.
  const gone =
    roomsState.loaded &&
    !!selectedKey &&
    !polled.some((r) => speakerArg(r) === selectedKey && r.online);
  const key = gone ? '' : selectedKey;
  useEffect(() => {
    if (gone) setTargetDevice('');
  }, [gone]);

  const lead = leadFor(polled, key);
  const leadKey = lead?.id ?? null;
  const speaker = lead ? speakerArg(lead) : key;
  // useRooms needs the lead before it can resolve it; keep it one render behind.
  if (leadKey !== leadId) setLeadId(leadKey);
  useEffect(() => followDevice(speaker), [speaker]);

  const live = useSpeaker(speaker);

  const now = useMemo(
    () => liveNowPlaying(live.nowPlaying, live.player),
    [live.nowPlaying, live.player]
  );
  // The room that stops being followed live goes back to the polls, whose last reading of
  // it predates the selection (often idle). Hand it what it showed live until a poll lands.
  const { patch } = roomsState;
  const shown = useRef<{ id: string | null; now: NowPlaying; volume: number } | null>(null);
  useEffect(() => {
    const prev = shown.current;
    if (prev?.id && prev.id !== leadKey) patch(prev.id, { now: prev.now, volume: prev.volume });
    shown.current = { id: leadKey, now, volume: live.volume };
  });

  // Until the live speaker first answers, the lead shows what this browser last saw of
  // it (a cold start, a room switch) rather than standby at a made-up volume.
  const heard = live.nowPlaying !== null || live.player.station !== null;
  const rooms: Room[] = polled.map((r) =>
    r.id === lead?.id && heard ? { ...r, now, volume: live.volume } : r
  );
  const snapshot = roomsState.loaded && heard ? rooms : null;
  useEffect(() => {
    if (snapshot) keepRooms(snapshot);
  });
  const leadRoom = rooms.find((r) => r.id === lead?.id) ?? null;

  const select = (id: string) => {
    const room = rooms.find((r) => r.id === id);
    if (!room) return;
    const target = leaderOf(rooms, room);
    setTargetDevice(speakerArg(target));
    setSelectedKey(speakerArg(target));
  };

  return {
    rooms,
    lead: leadRoom,
    members: leadRoom ? rooms.filter((r) => r.linkedTo === leadRoom.id) : [],
    speaker,
    live,
    roomsState,
    select,
  };
}
