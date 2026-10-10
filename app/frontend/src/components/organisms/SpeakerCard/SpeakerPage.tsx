// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Button } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { ModelArt } from '../../molecules/ModelArt';
import { SleepMenu } from '../../molecules/SleepMenu';
import { useI18n } from '../../../lib/i18n';
import { sourceOf, sourcesOf } from '../../../lib/rooms';
import type { Room } from '../../../hooks/useRooms';
import { CardTop } from './CardTop';
import { CardVolumes } from './CardVolumes';
import { GroupPicker } from './GroupPicker';
import { PairAlone } from './PairAlone';
import { SourcePicker } from './SourcePicker';
import type { SpeakerCardProps } from './index';

interface Props extends Omit<SpeakerCardProps, 'open' | 'onClose'> {
  room: Room;
  // Only the selected page is live; its neighbours render the same card, inert.
  live: boolean;
  close: () => void;
}

// One page of the carousel: the full card of a speaker (or group). A neighbour renders
// the same card, inert, so a swipe shows it complete and nothing shifts when it becomes
// the selected page. A tap on a peeking card selects it: inert content is not
// hit-tested, so the tap lands on the card itself.
export function SpeakerPage({ room, live, close, ...p }: Props) {
  const { t } = useI18n();
  const { rooms, roomsState } = p.selected;
  const joined = rooms.filter((r) => r.linkedTo === room.id);
  const group = [room, ...joined];
  const grouped = joined.length > 0;
  const title = grouped ? t('groupOf', { name: room.name }) : room.name;
  return (
    // The tap is the touch route only: the arrows and dots are the keyboard way to another
    // speaker. The card is no tab stop (its content is inert), so a key handler here could
    // never fire; the rule asks for one only because a tap handler exists.
    // eslint-disable-next-line jsx-a11y/click-events-have-key-events -- keyboard has the arrows
    <div className="spk-card" onClick={live ? undefined : () => p.selected.select(room.id)}>
      <div className="spk-body" inert={!live} aria-hidden={!live || undefined}>
        <CardTop
          title={title}
          live={live}
          poweredOn={!!room.now && !room.now.standby}
          onClose={close}
          onPower={(on) => {
            void p.playback.power(on);
            if (!on) p.sleep.clear();
          }}
        />
        <div className="spk-hero">
          <div className={`disc${grouped ? ' grouped' : ''}`}>
            {grouped && <span className="plus">+{joined.length}</span>}
            <div className="glyph">
              <ModelArt model={room.model} />
            </div>
            <Button
              className="gear"
              aria-label={`${t('settings')} ${room.name}`}
              onPress={() => p.onSettings(room)}
            >
              <Icon.cog />
            </Button>
          </div>
          {grouped && <p className="members">{group.map((r) => r.name).join(' & ')}</p>}
        </div>
        <SourcePicker
          sources={sourcesOf(room.sources)}
          current={sourceOf(room.now)}
          onChange={(source) => void p.playback.setSource(source)}
        />
        <CardVolumes group={group} volume={p.volume} onMute={p.onMute} />
        <PairAlone room={room} grouped={grouped} />
        <GroupPicker
          room={room}
          rooms={rooms}
          grouping={p.grouping}
          onReconnect={(id) => void roomsState.reconnect(id)}
        />
        <div className="line slrow">
          <SleepMenu
            timer={live ? p.sleep.timer : null}
            onChange={(minutes) => void p.sleep.start(minutes)}
          />
        </div>
      </div>
    </div>
  );
}
