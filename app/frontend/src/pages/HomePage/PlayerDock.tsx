// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Dock } from '../../components/organisms/Dock';
import { VolumePanel } from '../../components/organisms/VolumePanel';
import { groupName } from '../../lib/rooms';
import type { Room } from '../../hooks/useRooms';
import type { useRoomVolume } from '../../hooks/useRoomVolume';
import type { usePlayback } from '../../hooks/usePlayback';
import type { SelectedRoom } from '../../hooks/useSelectedRoom';
import type { Layers } from './useLayers';

interface PlayerDockProps {
  layers: Layers;
  selected: SelectedRoom;
  playback: ReturnType<typeof usePlayback>;
  starting: boolean;
  volume: ReturnType<typeof useRoomVolume>;
  onMute: (room: Room) => void;
}

// The selected room's player and speaker, docked at the bottom.
export function PlayerDock({
  layers,
  selected,
  playback,
  starting,
  volume,
  onMute,
}: PlayerDockProps) {
  const { lead, members, live } = selected;
  if (!lead) return null;
  return (
    <>
      <Dock
        lead={lead}
        members={members}
        starting={starting}
        unreachable={live.unreachable}
        onTransport={() => void playback.transport()}
        onPlayer={() => layers.show('nowPlaying')}
        onChoose={() => layers.show('presets')}
        onSpeaker={() => layers.show('speaker')}
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
