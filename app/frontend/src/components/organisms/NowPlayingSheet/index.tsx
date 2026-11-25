// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, ReactElement } from 'react';
import { Button } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { WashedBlur, PlayArt } from '../../molecules/Art';
import { SheetModal } from '../../molecules/SheetModal';
import { SleepMenu } from '../../molecules/SleepMenu';
import { VolumeSlider } from '../../molecules/VolumeSlider';
import { SpeakerBar } from '../Dock';
import { useI18n } from '../../../lib/i18n';
import { artSrc, describePlayback, detailLine } from '../../../lib/playback';
import { groupName, roomOn } from '../../../lib/rooms';
import { speakerArg, type Room } from '../../../hooks/useRooms';
import type { SleepTimer } from '../../../hooks/useSleepTimer';
import { ModeButtonsFor, TopBar, TransportControls } from './parts';
import { TrackTime } from './TrackTime';

const average = (rooms: Room[]) =>
  Math.round(rooms.reduce((sum, r) => sum + r.volume, 0) / rooms.length);

interface Props {
  open: boolean;
  onClose: () => void;
  lead: Room;
  members: Room[];
  starting: boolean;
  sleepTimer: SleepTimer;
  onTransport: () => void;
  // Spotify's previous and next track.
  onSkip: (dir: 'previous' | 'next') => void;
  // Nothing plays: start the room's first preset (or ask for a station).
  onStart: () => void;
  onSleep: (minutes: number) => void;
  onVolume: (volume: number) => void;
  // Without it the speaker icon is only a picture (a group has no single level to mute).
  onMute?: (() => void) | undefined;
  onPresets: () => void;
  onSpeaker: () => void;
  // Who plays along: the group sheet.
  onGroup: () => void;
}

// Now playing, full screen: what plays over its own blurred picture, the transport, the
// volume (a group's moves every speaker by the same step), the sleep timer and the
// speaker bar.
// What plays, in words: a station that names its song shows the station small above, the
// song as the title and the artist under it, as the design has it; anything else keeps its
// title and second line.
function Titles(p: {
  title: string;
  track?: string | undefined;
  artist?: string | undefined;
  line: string;
}) {
  if (!p.track) {
    return (
      <>
        <h2 className="np-title">{p.title}</h2>
        <p className="np-line">{p.line}</p>
      </>
    );
  }
  return (
    <>
      <p className="np-eyebrow">{p.title}</p>
      <h2 className="np-title">{p.track}</h2>
      {p.artist && <p className="np-line">{p.artist}</p>}
    </>
  );
}

// Under the volume: Spotify's modes, then the sleep timer and the group as pills.
function Extras(p: { modes: ReactNode; sleep: ReactNode; onGroup: () => void }) {
  const { t } = useI18n();
  return (
    <div className="np-row">
      <div className="np-left">
        {p.modes}
        <div className="np-pills">
          {p.sleep}
          <Button className="np-pill" onPress={p.onGroup}>
            <Icon.group aria-hidden="true" />
            {t('group')}
          </Button>
        </div>
      </div>
    </div>
  );
}

export function NowPlayingSheet({
  open,
  onClose,
  lead,
  members,
  starting,
  sleepTimer,
  ...cb
}: Props): ReactElement {
  const { t } = useI18n();
  const view = describePlayback(lead.now, t, speakerArg(lead));
  const name = groupName(lead, members);
  const group = [lead, ...members];
  const on = roomOn(lead);
  // An input (Bluetooth, AUX) plays from outside; this page can only start and stop radio.
  const canControl = view.artwork.kind === 'station' || !on;
  const action = on ? view.transport : 'play';
  const skips = on && view.skips;
  const track = on && view.artwork.kind === 'station' ? lead.now?.track?.trim() : '';
  return (
    <SheetModal
      open={open}
      onClose={onClose}
      label={`${t('playingIn')} ${name}`}
      variant="full"
      className="np"
    >
      {(close) => (
        <>
          <WashedBlur src={artSrc(view)} />
          <TopBar view={view} name={name} onClose={close} onPresets={cb.onPresets} />
          <div className="np-body">
            {on && <PlayArt artwork={view.artwork} name={view.title} className="np-art" />}
            <Titles
              title={view.title}
              track={starting ? '' : track}
              artist={lead.now?.artist}
              line={starting ? t('connecting') : detailLine(view, t)}
            />
            <TransportControls
              action={action}
              label={`${t(action)} · ${name}`}
              canControl={canControl}
              skips={skips}
              onPress={on ? cb.onTransport : cb.onStart}
              onSkip={cb.onSkip}
            />
            {on && view.artwork.kind === 'station' && (
              <TrackTime
                speaker={speakerArg(lead)}
                playing={lead.now?.playStatus === 'PLAY_STATE'}
                nowKey={`${String(lead.now?.track)}|${String(lead.now?.playStatus)}`}
              />
            )}
            <VolumeSlider
              light
              showValue
              label={group.length > 1 ? t('grpVolume') : t('volume')}
              value={average(group)}
              onChange={cb.onVolume}
              onMute={cb.onMute}
            />
            <Extras
              modes={on && <ModeButtonsFor now={lead.now} speaker={speakerArg(lead)} />}
              sleep={<SleepMenu light timer={sleepTimer} onChange={cb.onSleep} />}
              onGroup={cb.onGroup}
            />
          </div>
          <SpeakerBar lead={lead} members={members} onPress={cb.onSpeaker} />
        </>
      )}
    </SheetModal>
  );
}
