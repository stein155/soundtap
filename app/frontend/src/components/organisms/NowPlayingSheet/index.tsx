// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

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
  onMute?: () => void;
  onPresets: () => void;
  onSpeaker: () => void;
}

// Now playing, full screen: what plays over its own blurred picture, the transport, the
// volume (a group's moves every speaker by the same step), the sleep timer and the
// speaker bar.
export function NowPlayingSheet({
  open,
  onClose,
  lead,
  members,
  starting,
  sleepTimer,
  onTransport,
  onSkip,
  onStart,
  onSleep,
  onVolume,
  onMute,
  onPresets,
  onSpeaker,
}: Props) {
  const { t } = useI18n();
  const view = describePlayback(lead.now, t, speakerArg(lead));
  const name = groupName(lead, members);
  const group = [lead, ...members];
  const on = roomOn(lead);
  // An input (Bluetooth, AUX) plays from outside; this page can only start and stop radio.
  const canControl = view.artwork.kind === 'station' || !on;
  const action = on ? view.transport : 'play';
  const skips = on && view.skips;
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
          <TopBar view={view} onClose={close} onPresets={onPresets} />
          <div className="np-body">
            <h2 className="np-title">{view.title}</h2>
            <p className="np-line">{starting ? t('connecting') : detailLine(view, t)}</p>
            <TransportControls
              action={action}
              label={`${t(action)} · ${name}`}
              canControl={canControl}
              skips={skips}
              onPress={on ? onTransport : onStart}
              onSkip={onSkip}
            />
            {on && view.artwork.kind === 'station' && (
              <TrackTime
                speaker={speakerArg(lead)}
                playing={lead.now?.playStatus === 'PLAY_STATE'}
                nowKey={`${lead.now?.track}|${lead.now?.playStatus}`}
              />
            )}
            <VolumeSlider
              light
              label={group.length > 1 ? t('grpVolume') : t('volume')}
              value={average(group)}
              onChange={onVolume}
              onMute={onMute}
            />
            <div className="np-row">
              <div className="np-left">
                {on && <ModeButtonsFor now={lead.now} speaker={speakerArg(lead)} />}
                <SleepMenu light timer={sleepTimer} onChange={onSleep} />
              </div>
              {on && <PlayArt artwork={view.artwork} name={view.title} className="np-art" />}
            </div>
          </div>
          <SpeakerBar lead={lead} members={members} onPress={onSpeaker} />
        </>
      )}
    </SheetModal>
  );
}
