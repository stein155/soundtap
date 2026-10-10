// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Blur } from '../../molecules/Art';
import { SheetModal } from '../../molecules/SheetModal';
import { useI18n } from '../../../lib/i18n';
import { artSrc, describePlayback } from '../../../lib/playback';
import { groupName, roomOn } from '../../../lib/rooms';
import { speakerArg, type Room } from '../../../hooks/useRooms';
import type { useGrouping } from '../../../hooks/useGrouping';
import type { usePlayback } from '../../../hooks/usePlayback';
import type { useRoomVolume } from '../../../hooks/useRoomVolume';
import type { useSleepTimer } from '../../../hooks/useSleepTimer';
import type { SelectedRoom } from '../../../hooks/useSelectedRoom';
import { Carousel } from './Carousel';
import { NowPlayingStrip } from './NowPlayingStrip';
import { SpeakerPage } from './SpeakerPage';

export interface SpeakerCardProps {
  open: boolean;
  onClose: () => void;
  selected: SelectedRoom;
  playback: ReturnType<typeof usePlayback>;
  grouping: ReturnType<typeof useGrouping>;
  volume: ReturnType<typeof useRoomVolume>;
  sleep: ReturnType<typeof useSleepTimer>;
  // The speaker icon of a volume slider: mutes the room, or puts its level back.
  onMute: (room: Room) => void;
  // The gear on the speaker's picture: that speaker's settings.
  onSettings: (room: Room) => void;
}

// One speaker, in full: what it plays, power, source, volume, who plays along with it
// and the sleep timer. It is a carousel of the speakers: swipe (or use the dots or the
// arrows) to go to another one, whose card then shows in full.
export function SpeakerCard({ open, onClose, ...pageProps }: SpeakerCardProps) {
  const { t } = useI18n();
  const { selected, playback } = pageProps;
  const { rooms, lead, members, live } = selected;
  if (!lead) return null;

  const view = describePlayback(lead.now, t, speakerArg(lead));
  const starting = live.player.status === 'starting' || live.player.status === 'buffering';
  // The pages to swipe through: every group once, led by its leader.
  const pages = rooms.filter((r) => r.online && !r.linkedTo);

  return (
    <SheetModal
      open={open}
      onClose={onClose}
      label={`${t('speaker')} ${lead.name}`}
      variant="full"
      className="spk-modal"
    >
      {(close) => (
        <>
          <Blur src={artSrc(view)} />
          {roomOn(lead) && (
            <NowPlayingStrip
              view={view}
              starting={starting}
              groupLabel={groupName(lead, members)}
              onTransport={() => void playback.transport()}
            />
          )}
          <Carousel
            ids={pages.map((r) => r.id)}
            active={lead.id}
            onSelect={selected.select}
            names={pages.map((r) => r.name)}
            dotsLabel={t('rooms')}
            prevLabel={t('prevSpeaker')}
            nextLabel={t('nextSpeaker')}
            tip={t('swipeTip')}
            page={(id, on) => {
              const room = pages.find((r) => r.id === id);
              return room ? (
                <SpeakerPage room={room} live={on} close={close} {...pageProps} />
              ) : null;
            }}
          />
        </>
      )}
    </SheetModal>
  );
}
