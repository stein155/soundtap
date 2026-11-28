// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement, ReactNode } from 'react';
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
import { shownProgress, type UpdateRun } from '../../../hooks/updates';

interface Props {
  readonly lead: Room;
  readonly members: Room[];
  readonly starting: boolean;
  // The speaker stopped answering: what it last said it played is no longer known.
  readonly unreachable: boolean;
  // The speaker's update while the page follows it: its controls wait meanwhile.
  readonly update?: UpdateRun | null | undefined;
  // Floats over the dock: the way back to an update screen that was closed.
  readonly notice?: ReactNode;
  readonly onTransport: () => void;
  // The player, full screen.
  readonly onPlayer: () => void;
  // The presets, to pick something to play.
  readonly onChoose: () => void;
  // The speaker's card.
  readonly onSpeaker: () => void;
  readonly onVolume: () => void;
}

// "Updating… 42% done": a speaker's line while it updates.
export function updatingLine(update: UpdateRun, t: ReturnType<typeof useI18n>['t']): string {
  const progress = shownProgress(update);
  return progress === null
    ? t('updatingShort')
    : `${t('updatingShort')} ${t('updatingProgress', { percent: progress })}`;
}

// What the selected room plays, as a bar (tap for the full player) with its transport
// button, or an invitation to pick something; and under it the speaker bar.
export function Dock({
  lead,
  members,
  starting,
  unreachable,
  update = null,
  notice,
  onTransport,
  onPlayer,
  onChoose,
  onSpeaker,
  onVolume,
}: Props): ReactElement {
  const { t } = useI18n();
  const view = describePlayback(lead.now, t, speakerArg(lead));
  const name = groupName(lead, members);
  let miniLine = sourceLine(view, t);
  if (starting) miniLine = t('connecting');
  if (unreachable) miniLine = t('offline');
  if (update) miniLine = updatingLine(update, t);
  return (
    <footer className="dock">
      {notice}
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
              isDisabled={unreachable || !!update}
              onPress={onTransport}
              aria-label={`${t(view.transport)} · ${name}`}
            >
              <TransportGlyph action={view.transport} />
            </Button>
          )}
        </div>
      ) : (
        <Button className="idlebar" onPress={onChoose} isDisabled={!!update}>
          <span className="idle-badge">
            <Icon.play />
          </span>
          {t('chooseSomething')}
        </Button>
      )}
      <SpeakerBar
        lead={lead}
        members={members}
        update={update}
        onPress={onSpeaker}
        onVolume={onVolume}
      />
    </footer>
  );
}

// The speaker you are controlling: its drawing, its name (a group's names), what it is
// doing, and whether it is muted. Opens the speaker's card.
export function SpeakerBar({
  lead,
  members,
  update = null,
  onPress,
  onVolume,
}: {
  readonly lead: Room;
  readonly members: Room[];
  // The speaker's update while the page follows it: the line says so, the volume waits.
  readonly update?: UpdateRun | null | undefined;
  readonly onPress: () => void;
  // Opens the volume panel from the icon; without it the icon only shows the state.
  readonly onVolume?: () => void;
}): ReactElement {
  const { t } = useI18n();
  const view = describePlayback(lead.now, t, speakerArg(lead));
  const name = groupName(lead, members);
  const on = lead.now ? !lead.now.standby : false;
  // On the page: what plays and how loud. Under the player, which already shows the
  // volume: that it plays, and a sign that the bar opens the speakers.
  let status = t('standby');
  if (on) {
    status = onVolume
      ? `${view.status} · ${t('volumeLevel', { n: lead.volume })}`
      : t('playingWhat', { name: view.status });
  }
  if (update) status = updatingLine(update, t);
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
        {!onVolume && (
          <span className="spk-vol spk-up">
            <Icon.chevronUp />
          </span>
        )}
      </Button>
      {onVolume && (
        <Button
          className="spk-vol tap"
          aria-label={`${t('volume')} ${name}`}
          onPress={onVolume}
          isDisabled={!!update}
        >
          {icon}
        </Button>
      )}
    </div>
  );
}
