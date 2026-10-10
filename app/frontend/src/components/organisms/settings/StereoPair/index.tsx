// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState } from 'react';
import { Button } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { ChoiceList } from '../../../molecules/ChoiceList';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import { SegmentedControl } from '../../../molecules/SegmentedControl';
import { useI18n } from '../../../../lib/i18n';
import { failureText } from '../../../../lib/failure';
import {
  followSpeaker,
  getChannel,
  inviteSpeaker,
  setChannel,
  type Channel,
} from '../../../../lib/api';
import { speakerArg, type Room } from '../../../../hooks/useRooms';

type Side = Extract<Channel, 'left' | 'right'>;
const other = (side: Side): Side => (side === 'left' ? 'right' : 'left');

// The speaker this one is grouped with, either way round: a pair is two speakers in one
// group, each playing one channel.
export function pairPartner(room: Room, rooms: Room[]): Room | null {
  return (
    rooms.find((r) => r.id !== room.id && (r.linkedTo === room.id || room.linkedTo === r.id)) ??
    null
  );
}

// The speakers this one can pair with: online and not in another group, as leader or as
// follower. One already following this speaker is fine, pairing just takes it over.
export function pairCandidates(room: Room, rooms: Room[]): Room[] {
  const inOtherGroup = (r: Room) =>
    (r.linkedTo !== null && r.linkedTo !== room.id) ||
    rooms.some((f) => f.linkedTo === r.id && f.id !== room.id);
  return rooms.filter((r) => r.online && r.id !== room.id && !inOtherGroup(r));
}

interface Props {
  room: Room;
  rooms: Room[];
  onChanged: () => void;
}

// Two speakers as one stereo speaker, of any model: this one leads, the partner follows
// and plays the other channel. Volume, presets and power then act on the group.
// Pair and unpair: the partner follows this speaker and plays the other channel.
function useStereoPair(room: Room, rooms: Room[], side: Side, onChanged: () => void) {
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const [channel, setOwn] = useState<Channel | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    void getChannel(speaker)
      .then(setOwn)
      .catch(() => null); // no answer: the section stays hidden, as for a speaker without channels
  }, [speaker]);

  const partner = pairPartner(room, rooms);
  const run = async (steps: () => Promise<unknown>) => {
    setBusy(true);
    setError('');
    try {
      await steps();
    } catch (e) {
      setError(failureText(e, t('stereoPairFailed'), t));
    } finally {
      // Re-read the group either way: a step that failed may still have half applied.
      onChanged();
      // No answer leaves what was known. The button must come back regardless.
      await getChannel(speaker).then(setOwn, () => null);
      setBusy(false);
    }
  };
  const pair = (partnerRoom: Room) =>
    run(async () => {
      const them = speakerArg(partnerRoom);
      try {
        await inviteSpeaker(partnerRoom.ip, speaker);
        await setChannel(other(side), them);
        await setChannel(side, speaker);
      } catch (e) {
        // Leave no half pair behind: undo every step, each on its own.
        await Promise.allSettled([
          followSpeaker('', them),
          setChannel('stereo', them),
          setChannel('stereo', speaker),
        ]);
        throw e;
      }
    });
  const unpair = () =>
    run(async () => {
      if (partner) {
        const follower = partner.linkedTo === room.id ? partner : room;
        await followSpeaker('', speakerArg(follower));
        await setChannel('stereo', speakerArg(partner));
      }
      await setChannel('stereo', speaker);
    });
  return { channel, partner, busy, error, pair, unpair };
}

export function StereoPair({ room, rooms, onChanged }: Props) {
  const { t } = useI18n();
  const [side, setSide] = useState<Side>('left');
  const candidates = pairCandidates(room, rooms);
  const [pick, setPick] = useState(candidates[0]?.id ?? '');
  const { channel, partner, busy, error, pair, unpair } = useStereoPair(
    room,
    rooms,
    side,
    onChanged
  );
  const sideLabel: Record<Side, string> = { left: t('stereo_left'), right: t('stereo_right') };
  const paired = channel === 'left' || channel === 'right';

  if (channel === null) return null;
  if (paired)
    return (
      <>
        <Note>
          {partner
            ? t('stereoPairWith', { name: partner.name, side: sideLabel[channel] })
            : t('stereoPairAlone', { side: sideLabel[channel] })}
        </Note>
        <ConfirmAction
          label={t('stereoPairEnd')}
          warning={t('stereoPairEndWarn')}
          confirmLabel={t('stereoPairEnd')}
          disabled={busy}
          onConfirm={unpair}
        />
        {error && <Hint error>{error}</Hint>}
      </>
    );
  const chosen = candidates.find((r) => r.id === pick) ?? candidates.at(0);
  if (!chosen) return <Note>{t('noOtherSpeakers')}</Note>;
  return (
    <>
      <Note>{t('stereoPairHint')}</Note>
      <ChoiceList
        label={t('stereoPairPartner')}
        value={chosen.id}
        options={candidates.map((r) => ({ value: r.id, label: r.name }))}
        onChange={setPick}
      />
      <SegmentedControl
        label={t('stereoPairThis')}
        value={side}
        options={[
          { value: 'left', label: sideLabel.left },
          { value: 'right', label: sideLabel.right },
        ]}
        onChange={setSide}
      />
      <PairButton
        label={t('stereoPairStart')}
        busy={busy}
        error={error}
        onClick={() => pair(chosen)}
      />
    </>
  );
}

function PairButton(props: {
  label: string;
  busy: boolean;
  error: string;
  onClick: () => Promise<void>;
}) {
  return (
    <>
      <Button wide disabled={props.busy} onClick={() => void props.onClick()}>
        {props.label}
      </Button>
      {props.error && <Hint error>{props.error}</Hint>}
    </>
  );
}
