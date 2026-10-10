// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { Spinner } from '../../atoms/Spinner';
import { Button } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { Blur, PlayArt } from '../../molecules/Art';
import { TransportGlyph } from '../../molecules/TransportGlyph';
import { ModelArt } from '../../molecules/ModelArt';
import { useI18n } from '../../../lib/i18n';
import { artSrc, describePlayback, sourceLine } from '../../../lib/playback';
import { groupName, roomOn } from '../../../lib/rooms';
import { speakerArg, type Room } from '../../../hooks/useRooms';

interface Props {
  lead: Room;
  members: Room[];
  starting: boolean;
  // The speaker stopped answering: what it last said it played is no longer known.
  unreachable: boolean;
  onTransport: () => void;
  // The player, full screen.
  onPlayer: () => void;
  // The presets, to pick something to play.
  onChoose: () => void;
  // The speaker's card.
  onSpeaker: () => void;
  onVolume: () => void;
}

// What the selected room plays, as a bar (tap for the full player) with its transport
// button, or an invitation to pick something; and under it the speaker bar.
export function Dock({
  lead,
  members,
  starting,
  unreachable,
  onTransport,
  onPlayer,
  onChoose,
  onSpeaker,
  onVolume,
}: Props) {
  const { t } = useI18n();
  const view = describePlayback(lead.now, t, speakerArg(lead));
  const name = groupName(lead, members);
  const miniLine = unreachable ? t('offline') : starting ? t('connecting') : sourceLine(view, t);
  return (
    <footer className="dock">
      {roomOn(lead) ? (
        <div className="minibar">
          <Blur src={artSrc(view)} />
          <Button
            className="mini-open"
            onPress={onPlayer}
            aria-label={t('openPlayer', { name: view.title })}
          >
            {starting ? (
              <span className="art mini-art">
                <Spinner />
              </span>
            ) : (
              <PlayArt artwork={view.artwork} name={view.title} className="mini-art" />
            )}
            <span className="mini-text">
              <span className="mini-title">{view.title}</span>
              <span className="mini-line">{miniLine}</span>
            </span>
          </Button>
          {view.artwork.kind === 'station' && (
            <Button
              className="mini-ctl"
              isDisabled={unreachable}
              onPress={onTransport}
              aria-label={`${t(view.transport)} · ${name}`}
            >
              <TransportGlyph action={view.transport} />
            </Button>
          )}
        </div>
      ) : (
        <Button className="idlebar" onPress={onChoose}>
          <span className="idle-badge">
            <Icon.play />
          </span>
          {t('chooseSomething')}
        </Button>
      )}
      <SpeakerBar lead={lead} members={members} onPress={onSpeaker} onVolume={onVolume} />
    </footer>
  );
}

// The speaker you are controlling: its drawing, its name (a group's names), what it is
// doing, and whether it is muted. Opens the speaker's card.
export function SpeakerBar({
  lead,
  members,
  onPress,
  onVolume,
}: {
  lead: Room;
  members: Room[];
  onPress: () => void;
  // Opens the volume panel from the icon; without it the icon only shows the state.
  onVolume?: () => void;
}) {
  const { t } = useI18n();
  const view = describePlayback(lead.now, t, speakerArg(lead));
  const name = groupName(lead, members);
  const status = lead.now?.standby || !lead.now ? t('standby') : view.status;
  const icon = lead.volume === 0 ? <Icon.mute /> : <Icon.volume />;
  return (
    <div className="spkbar-wrap">
      <Button className="spkbar" onPress={onPress} aria-label={`${t('speaker')}: ${name}`}>
        <span className="avatar">
          <span className="av-glyph">
            <ModelArt model={lead.model} />
          </span>
          {members.length > 0 && <b>+{members.length}</b>}
        </span>
        <span className="spk-text">
          <span className="spk-name">{name}</span>
          <span className="spk-sub">{status}</span>
        </span>
        {!onVolume && <span className="spk-vol">{icon}</span>}
      </Button>
      {onVolume && (
        <Button className="spk-vol tap" aria-label={`${t('volume')} ${name}`} onPress={onVolume}>
          {icon}
        </Button>
      )}
    </div>
  );
}
