// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Button, ToggleButton } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { Spinning } from '../../atoms/Spinning';
import { ModelArt } from '../../molecules/ModelArt';
import { useI18n, type TranslateFn } from '../../../lib/i18n';
import { describePlayback } from '../../../lib/playback';
import type { Room } from '../../../hooks/useRooms';
import type { useGrouping } from '../../../hooks/useGrouping';

// What another speaker is doing, so you know what you would take it away from.
function statusOf(room: Room, rooms: Room[], t: TranslateFn): string {
  const leader = rooms.find((r) => r.id === room.linkedTo);
  if (leader) return `${t('withSp')} ${leader.name}`;
  return describePlayback(room.now, t).status;
}

interface Props {
  // The speaker whose card this is: the others join or leave it.
  room: Room;
  rooms: Room[];
  grouping: ReturnType<typeof useGrouping>;
  onReconnect: (id: string) => void;
}

// Who plays along with this speaker: all at once, or one by one, and the speakers that
// are offline with a way to reach them again.
export function GroupPicker({ room, rooms, grouping, onReconnect }: Props) {
  const { t } = useI18n();
  if (rooms.length === 1) {
    return (
      <div className="grp">
        <p className="members">{t('noOtherSpeakers')}</p>
      </div>
    );
  }
  const others = rooms.filter((r) => r.online && r.id !== room.id);
  const offline = rooms.filter((r) => !r.online);
  const joined = rooms.filter((r) => r.linkedTo === room.id);
  const joinable = others.filter((r) => r.linkedTo !== room.id);
  if (others.length === 0 && offline.length === 0) return null;
  return (
    <div className="grp">
      {joinable.length > 0 && (
        <Button className="pill" onPress={() => void grouping.linkMany(joinable)}>
          {t('playOnAll')}
        </Button>
      )}
      {joined.length > 0 && (
        <Button className="pill" onPress={() => void grouping.unlinkMany(joined)}>
          {t('stopPlayOnAll')}
        </Button>
      )}
      {others.length > 0 && <p className="or">{t('orPerSpeaker')}</p>}
      <div className="gwrap">
        {others.map((other) => {
          const member = other.linkedTo === room.id;
          return (
            <ToggleButton
              key={other.id}
              className="gpill"
              isSelected={member}
              onChange={() => void (member ? grouping.unlink(other) : grouping.link(other))}
            >
              {member ? <Icon.check /> : <Icon.plus />}
              <span className="gicon">
                <ModelArt model={other.model} />
              </span>
              <span className="gtext">
                <b>{other.name}</b>
                <small>{member ? `${t('withSp')} ${room.name}` : statusOf(other, rooms, t)}</small>
              </span>
            </ToggleButton>
          );
        })}
        {offline.map((gone) => (
          <div key={gone.id} className="gpill offline">
            <span className="gicon">
              <ModelArt model={gone.model} />
            </span>
            <span className="gtext">
              <b>{gone.name}</b>
              <small>{gone.connecting ? t('connecting') : t('offline')}</small>
            </span>
            <Button
              className="retry"
              isDisabled={gone.connecting}
              onPress={() => onReconnect(gone.id)}
            >
              <Spinning active={!!gone.connecting}>
                <Icon.refresh />
              </Spinning>
              {t('reconnect')}
            </Button>
          </div>
        ))}
      </div>
    </div>
  );
}
