// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Button, Heading } from 'react-aria-components';
import { EqualizerBars } from '../../atoms/EqualizerBars';
import { Icon } from '../../atoms/Icon';
import { ModelArt } from '../../molecules/ModelArt';
import { SetMenu } from '../../molecules/SetMenu';
import { SetSwitch } from '../../molecules/SetItem';
import { SheetModal } from '../../molecules/SheetModal';
import { useI18n } from '../../../lib/i18n';
import { describePlayback } from '../../../lib/playback';
import { speakerArg, type Room } from '../../../hooks/useRooms';
import type { SelectedRoom } from '../../../hooks/useSelectedRoom';
import type { useGrouping } from '../../../hooks/useGrouping';

type Grouping = ReturnType<typeof useGrouping>;

interface Props {
  open: boolean;
  onClose: () => void;
  selected: SelectedRoom;
  grouping: Grouping;
  // The speaker card of the selected room: its power, its own volume, its settings.
  onMore: () => void;
}

// One speaker: its drawing, its name, what it plays (or that it is offline), and a tick on
// the one this page controls. A tap makes it the one.
function SpeakerRow({ room, on, onSelect }: { room: Room; on: boolean; onSelect: () => void }) {
  const { t } = useI18n();
  const view = describePlayback(room.now, t, speakerArg(room));
  return (
    <button
      type="button"
      role="radio"
      aria-checked={on}
      className="spks-row"
      disabled={!room.online}
      onClick={onSelect}
    >
      <span className="spks-disc" aria-hidden="true">
        <ModelArt model={room.model} />
      </span>
      <span className="spks-text">
        <span className="spks-name">{room.name}</span>
        <span className="spks-now">
          {room.online && view.live && <EqualizerBars size={9} />}
          {room.online ? view.status : t('offline')}
        </span>
      </span>
      {on && (
        <span className="spks-tick" aria-hidden="true">
          <Icon.check />
        </span>
      )}
    </button>
  );
}

// The speakers that can play along with the selected one, each a switch, then one button
// for all of them (or to stop the group) and the way to the selected speaker's card.
function Together({
  lead,
  free,
  grouping,
  onMore,
}: {
  lead: Room;
  free: Room[];
  grouping: Grouping;
  onMore: () => void;
}) {
  const { t } = useI18n();
  const joined = free.filter((r) => r.linkedTo === lead.id);
  const joinable = free.filter((r) => r.linkedTo !== lead.id);
  const toggle = (r: Room) => void (r.linkedTo === lead.id ? grouping.unlink(r) : grouping.link(r));
  return (
    <>
      {free.length > 0 && (
        <SetMenu title={t('playTogetherWith', { name: lead.name })}>
          {free.map((r) => (
            <SetSwitch
              key={r.id}
              icon="stereo"
              label={r.name}
              on={r.linkedTo === lead.id}
              onToggle={() => toggle(r)}
            />
          ))}
        </SetMenu>
      )}
      <div className="spks-actions">
        {joinable.length > 0 && (
          <Button className="spks-primary" onPress={() => void grouping.linkMany(joinable)}>
            {t('playOnAll')}
          </Button>
        )}
        {joined.length > 0 && joinable.length === 0 && (
          <Button className="spks-primary" onPress={() => void grouping.unlinkMany(joined)}>
            {t('stopPlayOnAll')}
          </Button>
        )}
        <Button className="spks-link" onPress={onMore}>
          {t('speakerMore', { name: lead.name })}
        </Button>
      </div>
    </>
  );
}

// Which speaker this page controls, and which play along with it: every speaker as a row
// (its drawing, what it plays, a tick on the one selected), the others as switches that
// join it, and one button to play everywhere.
export function SpeakersSheet({ open, onClose, selected, grouping, onMore }: Props) {
  const { t } = useI18n();
  const { rooms, lead } = selected;
  // Who can join the selected room: online, and not following another leader.
  const free = lead
    ? rooms.filter((r) => r.id !== lead.id && r.online && (!r.linkedTo || r.linkedTo === lead.id))
    : [];
  return (
    <SheetModal open={open} onClose={onClose} label={t('speakers')} variant="page">
      {(close) => (
        <>
          <div className="sheet-head">
            <Heading slot="title" className="sheet-title">
              {t('speakers')}
            </Heading>
            <Button className="x-btn" aria-label={t('close')} onPress={close}>
              <Icon.close />
            </Button>
          </div>
          <div className="spks-sheet">
            <SetMenu title={t('speakersControl')}>
              <div role="radiogroup" aria-label={t('speakersControl')} className="spks-rows">
                {rooms.map((room) => (
                  <SpeakerRow
                    key={room.id}
                    room={room}
                    on={room.id === lead?.id}
                    onSelect={() => {
                      selected.select(room.id);
                      close();
                    }}
                  />
                ))}
              </div>
            </SetMenu>
            {lead && (
              <Together
                lead={lead}
                free={free}
                grouping={grouping}
                onMore={() => {
                  close();
                  onMore();
                }}
              />
            )}
          </div>
        </>
      )}
    </SheetModal>
  );
}
