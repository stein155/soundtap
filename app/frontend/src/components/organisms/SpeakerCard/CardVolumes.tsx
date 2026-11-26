// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { VolumeSlider } from '../../molecules/VolumeSlider';
import { useI18n } from '../../../lib/i18n';
import type { Room } from '../../../hooks/useRooms';
import type { useRoomVolume } from '../../../hooks/useRoomVolume';

const average = (rooms: Room[]) =>
  Math.round(rooms.reduce((sum, r) => sum + r.volume, 0) / rooms.length);

interface Props {
  // The speaker first, then the ones playing along with it.
  readonly group: Room[];
  readonly volume: ReturnType<typeof useRoomVolume>;
  readonly onMute: (room: Room) => void;
}

// One slider for a lone speaker; for a group, one for all of them and one per speaker.
export function CardVolumes({ group, volume, onMute }: Props): ReactElement {
  const { t } = useI18n();
  const [room] = group;
  if (room && group.length === 1) {
    return (
      <div className="line vols">
        <VolumeSlider
          label={t('volume')}
          value={room.volume}
          onChange={(v) => volume.setRoomVolume(room, v)}
          onMute={() => onMute(room)}
        />
      </div>
    );
  }
  return (
    <div className="line vols">
      <p className="vtitle">{t('grpVolume')}</p>
      <VolumeSlider
        label={t('grpVolume')}
        value={average(group)}
        onChange={volume.setGroupVolume}
      />
      {group.map((member) => (
        <div key={member.id}>
          <p className="vname">{member.name}</p>
          <VolumeSlider
            label={`${t('volume')} ${member.name}`}
            value={member.volume}
            onChange={(v) => volume.setRoomVolume(member, v)}
            onMute={() => onMute(member)}
          />
        </div>
      ))}
    </div>
  );
}
