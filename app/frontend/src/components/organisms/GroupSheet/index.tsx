// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Button, Heading } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { ModelArt } from '../../molecules/ModelArt';
import { SetMenu } from '../../molecules/SetMenu';
import { SheetModal } from '../../molecules/SheetModal';
import { VolumeSlider } from '../../molecules/VolumeSlider';
import { useI18n } from '../../../lib/i18n';
import type { Room } from '../../../hooks/useRooms';
import type { SelectedRoom } from '../../../hooks/useSelectedRoom';
import type { useGrouping } from '../../../hooks/useGrouping';
import type { useRoomVolume } from '../../../hooks/useRoomVolume';

interface Props {
  readonly open: boolean;
  readonly onClose: () => void;
  readonly selected: SelectedRoom;
  readonly grouping: ReturnType<typeof useGrouping>;
  readonly volume: ReturnType<typeof useRoomVolume>;
}

const average = (rooms: Room[]) =>
  Math.round(rooms.reduce((sum, r) => sum + r.volume, 0) / rooms.length);

// One speaker that can play along: its drawing, its name and a switch; once it plays
// along, its own volume under it.
function Member({
  room,
  member,
  onToggle,
  onVolume,
}: {
  readonly room: Room;
  readonly member: boolean;
  readonly onToggle: () => void;
  readonly onVolume: (v: number) => void;
}) {
  const { t } = useI18n();
  return (
    <div className="grp-member">
      <button
        type="button"
        role="switch"
        aria-checked={member}
        className="grp-row"
        onClick={onToggle}
      >
        <span className="spks-disc" aria-hidden="true">
          <ModelArt model={room.model} />
        </span>
        <span className="grp-name">{room.name}</span>
        <span className="grp-switch" data-on={member || undefined} aria-hidden="true" />
      </button>
      {member && (
        <div className="grp-vol">
          <VolumeSlider
            label={`${room.name} ${t('volume')}`}
            value={room.volume}
            onChange={onVolume}
            showValue
          />
        </div>
      )}
    </div>
  );
}

// One button for all of them: join every free speaker, or end the group.
function GroupActions({
  grouping,
  joinable,
  joined,
}: {
  readonly grouping: Props['grouping'];
  readonly joinable: Room[];
  readonly joined: Room[];
}): ReactElement {
  const { t } = useI18n();
  return (
    <div className="spks-actions">
      {joinable.length > 0 ? (
        <Button
          className="spks-primary"
          onPress={() => {
            void grouping.linkMany(joinable);
          }}
        >
          {t('playOnAll')}
        </Button>
      ) : (
        joined.length > 0 && (
          <Button
            className="spks-power"
            onPress={() => {
              void grouping.unlinkMany(joined);
            }}
          >
            {t('stopPlayOnAll')}
          </Button>
        )
      )}
    </div>
  );
}

// Who plays along with the selected speaker, as the design's "Groep" (from now playing's
// Group): the volume of the whole group, each speaker that can join as a switch with its
// own volume once it plays along, and one button for all of them or to end the group.
export function GroupSheet({
  open,
  onClose,
  selected,
  grouping,
  volume,
}: Props): ReactElement | null {
  const { t } = useI18n();
  const { rooms, lead } = selected;
  if (!lead) return null;
  // Who can join: online, and not following another leader.
  const free = rooms.filter(
    (r) => r.id !== lead.id && r.online && (!r.linkedTo || r.linkedTo === lead.id)
  );
  const joined = free.filter((r) => r.linkedTo === lead.id);
  const joinable = free.filter((r) => r.linkedTo !== lead.id);
  return (
    <SheetModal open={open} onClose={onClose} label={t('group')} className="spks-modal">
      {(close) => (
        <>
          <span className="ract-grab" aria-hidden="true" />
          <div className="sheet-head">
            <Heading slot="title" className="sheet-title">
              {t('group')}
            </Heading>
            <Button className="x-btn" aria-label={t('close')} onPress={close}>
              <Icon.close />
            </Button>
          </div>
          <div className="spks-sheet">
            {joined.length > 0 && (
              <SetMenu title={t('grpVolume')}>
                <div className="grp-vol grp-all">
                  <VolumeSlider
                    label={t('grpVolume')}
                    value={average([lead, ...joined])}
                    onChange={volume.setGroupVolume}
                    showValue
                  />
                </div>
              </SetMenu>
            )}
            <SetMenu title={t('playTogetherWith', { name: lead.name })}>
              {free.map((r) => (
                <Member
                  key={r.id}
                  room={r}
                  member={r.linkedTo === lead.id}
                  onToggle={() => {
                    void (r.linkedTo === lead.id ? grouping.unlink(r) : grouping.link(r));
                  }}
                  onVolume={(v) => volume.setRoomVolume(r, v)}
                />
              ))}
            </SetMenu>
            <GroupActions grouping={grouping} joinable={joinable} joined={joined} />
          </div>
        </>
      )}
    </SheetModal>
  );
}
