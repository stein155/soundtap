// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import { Button, Heading } from 'react-aria-components';
import { EqualizerBars } from '../../atoms/EqualizerBars';
import { Icon } from '../../atoms/Icon';
import { ChoiceList } from '../../molecules/ChoiceList';
import { ModelArt } from '../../molecules/ModelArt';
import { PairAlone } from '../SpeakerCard/PairAlone';
import { SetMenu } from '../../molecules/SetMenu';
import { SetItem } from '../../molecules/SetItem';
import { SheetModal } from '../../molecules/SheetModal';
import { useI18n } from '../../../lib/i18n';
import { describePlayback } from '../../../lib/playback';
import { sourceOf, sourcesOf, type Source } from '../../../lib/rooms';
import { speakerArg, type Room } from '../../../hooks/useRooms';
import type { SelectedRoom } from '../../../hooks/useSelectedRoom';
import type { usePlayback } from '../../../hooks/usePlayback';

interface Props {
  open: boolean;
  onClose: () => void;
  selected: SelectedRoom;
  playback: ReturnType<typeof usePlayback>;
  onSettings: (room: Room) => void;
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

// The selected speaker itself: its source (when it has more than one), its settings, and
// its power.
function Lead({
  lead,
  playback,
  onSettings,
}: {
  lead: Room;
  playback: Props['playback'];
  onSettings: () => void;
}) {
  const { t } = useI18n();
  const [choosing, setChoosing] = useState(false);
  const sources = sourcesOf(lead.sources);
  const current = sourceOf(lead.now);
  const on = !!lead.now && !lead.now.standby;
  return (
    <>
      <SetMenu title={lead.name}>
        {sources.length > 1 && (
          <SetItem
            icon="wifi"
            label={t('source')}
            meta={t(`src_${current}`)}
            onClick={() => setChoosing(!choosing)}
          />
        )}
        {choosing && (
          <div className="spks-choice">
            <ChoiceList<Source>
              label={t('source')}
              value={current}
              options={sources.map((s) => ({ value: s, label: t(`src_${s}`) }))}
              onChange={(s) => {
                setChoosing(false);
                void playback.setSource(s);
              }}
            />
          </div>
        )}
        <SetItem icon="settings" label={t('settings')} onClick={onSettings} />
      </SetMenu>
      <PairAlone room={lead} grouped={false} />
      <div className="spks-actions">
        <Button className="spks-power" onPress={() => void playback.power(!on)}>
          <Icon.power aria-hidden="true" />
          {t(on ? 'powerOffName' : 'powerOnName', { name: lead.name })}
        </Button>
      </div>
    </>
  );
}

// Which speaker this page controls, as the design's "Speaker wisselen": every speaker as a
// row (its drawing, what it plays, a tick on the one selected), then the selected one's
// source, settings and power. Grouping has its own sheet, from now playing's Group.
export function SpeakersSheet({ open, onClose, selected, playback, onSettings }: Props) {
  const { t } = useI18n();
  const { rooms, lead } = selected;
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
              <Lead
                lead={lead}
                playback={playback}
                onSettings={() => {
                  close();
                  onSettings(lead);
                }}
              />
            )}
          </div>
        </>
      )}
    </SheetModal>
  );
}
