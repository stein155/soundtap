// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Dock } from '../../components/organisms/Dock';
import { VolumePanel } from '../../components/organisms/VolumePanel';
import { UpdateNotice } from '../../components/organisms/settings/UpdateSettings/UpdateScreen';
import { useRoomUpdate } from '../../hooks/updates';
import { groupName } from '../../lib/rooms';
import type { Room } from '../../hooks/useRooms';
import type { useRoomVolume } from '../../hooks/useRoomVolume';
import type { usePlayback } from '../../hooks/usePlayback';
import type { SelectedRoom } from '../../hooks/useSelectedRoom';
import type { Layers } from './useLayers';

interface PlayerDockProps {
  readonly layers: Layers;
  readonly selected: SelectedRoom;
  readonly playback: ReturnType<typeof usePlayback>;
  readonly starting: boolean;
  readonly volume: ReturnType<typeof useRoomVolume>;
  readonly onMute: (room: Room) => void;
}

// The selected room's player and speaker, docked at the bottom.
export function PlayerDock({
  layers,
  selected,
  playback,
  starting,
  volume,
  onMute,
}: PlayerDockProps): ReactElement | null {
  const { lead, members, live, rooms } = selected;
  const update = useRoomUpdate(lead);
  if (!lead) return null;
  return (
    <>
      <Dock
        lead={lead}
        members={members}
        starting={starting}
        unreachable={live.unreachable}
        update={update}
        notice={<UpdateNotice rooms={rooms} />}
        onTransport={() => {
          void playback.transport();
        }}
        onPlayer={() => layers.show('nowPlaying')}
        onChoose={() => layers.show('presets')}
        onSpeaker={() => layers.show('speakers')}
        onVolume={() => layers.show('volume')}
      />
      <VolumePanel
        open={layers.open.volume}
        onClose={() => layers.hide('volume')}
        name={groupName(lead, members)}
        group={[lead, ...members]}
        volume={volume}
        onMute={onMute}
      />
    </>
  );
}
