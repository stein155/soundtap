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
import { artSrc, describePlayback, detailLine, type PlaybackView } from '../../../lib/playback';
import { groupName, roomOn } from '../../../lib/rooms';
import { speakerArg, type Room } from '../../../hooks/useRooms';
import type { SleepTimer } from '../../../hooks/useSleepTimer';
import { LikeButton, ModesFor, TopBar, TransportControls } from './parts';
import { ArtFrame, TrackTime } from './TrackTime';

const average = (rooms: Room[]) =>
  Math.round(rooms.reduce((sum, r) => sum + r.volume, 0) / rooms.length);

interface Props {
  readonly open: boolean;
  readonly onClose: () => void;
  readonly lead: Room;
  readonly members: Room[];
  readonly starting: boolean;
  readonly sleepTimer: SleepTimer;
  readonly onTransport: () => void;
  // Spotify's previous and next track.
  readonly onSkip: (dir: 'previous' | 'next') => void;
  // Nothing plays: start the room's first preset (or ask for a station).
  readonly onStart: () => void;
  readonly onSleep: (minutes: number) => void;
  readonly onVolume: (volume: number) => void;
  // Without it the speaker icon is only a picture (a group has no single level to mute).
  readonly onMute?: (() => void) | undefined;
  readonly onPresets: () => void;
  readonly onSpeaker: () => void;
  // Who plays along: the group sheet.
  readonly onGroup: () => void;
}

// Now playing, full screen and without scrolling: what plays over its own blurred picture,
// the artwork with how far the track is, the transport with shuffle and repeat outside it,
// the volume (a group's moves every speaker by the same step), the sleep timer and the
// speaker bar.
// What plays, in words: a station that names its song shows the station small above, the
// song as the title and the artist under it, as the design has it; anything else keeps its
// title and second line.
function Titles({
  title,
  track,
  artist,
  line,
}: {
  readonly title: string;
  readonly track?: string | undefined;
  readonly artist?: string | undefined;
  readonly line: string;
}) {
  if (!track) {
    return (
      <>
        <h2 className="np-title">{title}</h2>
        <p className="np-line">{line}</p>
      </>
    );
  }
  return (
    <>
      <p className="np-eyebrow">{title}</p>
      <h2 className="np-title">{track}</h2>
      {artist && <p className="np-line">{artist}</p>}
    </>
  );
}

// The artwork, with how far the track is for what plays from a service or station (an
// input has no track).
function Cover({ lead, view }: { readonly lead: Room; readonly view: PlaybackView }) {
  const art = <PlayArt artwork={view.artwork} name={view.title} className="np-art" />;
  if (view.artwork.kind !== 'station') return <ArtFrame>{art}</ArtFrame>;
  return (
    <TrackTime
      speaker={speakerArg(lead)}
      playing={lead.now?.playStatus === 'PLAY_STATE'}
      nowKey={`${String(lead.now?.track)}|${String(lead.now?.playStatus)}`}
    >
      {art}
    </TrackTime>
  );
}

// Under the volume: the sleep timer and the group as pills.
function Extras({ sleep, onGroup }: { readonly sleep: ReactNode; readonly onGroup: () => void }) {
  const { t } = useI18n();
  return (
    <div className="np-pills">
      {sleep}
      <Button className="np-pill" onPress={onGroup}>
        <Icon.group aria-hidden="true" />
        {t('group')}
      </Button>
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
            {on && <Cover lead={lead} view={view} />}
            <div className="np-head">
              <div className="np-titles">
                <Titles
                  title={view.title}
                  track={starting ? '' : track}
                  artist={lead.now?.artist}
                  line={starting ? t('connecting') : detailLine(view, t)}
                />
              </div>
              {on && lead.now?.source === 'SPOTIFY' && (
                <LikeButton speaker={speakerArg(lead)} track={lead.now.track} />
              )}
            </div>
            <ModesFor now={on ? lead.now : null} speaker={speakerArg(lead)}>
              {(modes) => (
                <TransportControls
                  action={action}
                  label={`${t(action)} · ${name}`}
                  canControl={canControl}
                  skips={skips}
                  onPress={on ? cb.onTransport : cb.onStart}
                  onSkip={cb.onSkip}
                  modes={modes}
                />
              )}
            </ModesFor>
            <VolumeSlider
              light
              showValue
              label={group.length > 1 ? t('grpVolume') : t('volume')}
              value={average(group)}
              onChange={cb.onVolume}
              onMute={cb.onMute}
            />
            <Extras
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
