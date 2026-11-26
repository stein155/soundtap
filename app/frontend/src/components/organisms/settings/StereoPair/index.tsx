// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState, type ReactElement } from 'react';
import { Button } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { Note } from '../../../atoms/Note';
import { ChoiceList } from '../../../molecules/ChoiceList';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import { ModelArt } from '../../../molecules/ModelArt';
import { SetMenu } from '../../../molecules/SetMenu';
import { useRadioKeys } from '../../../../hooks/useRadioKeys';
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
import { Footer, Letter, SideBox, SideButton, Sides, Then } from './styled';

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
  readonly room: Room;
  readonly rooms: Room[];
  readonly onChanged: () => void;
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
    // An answer for the speaker before, or after leaving, does not count.
    let alive = true;
    void getChannel(speaker)
      .then((got) => {
        if (alive) setOwn(got);
      })
      .catch(() => null); // no answer: the section stays hidden, as for a speaker without channels
    return () => {
      alive = false;
    };
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
  const pair = async (partnerRoom: Room) =>
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
  const unpair = async () =>
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

// Which side this speaker plays, as two halves with the side's letter, and what that leaves
// the partner.
function SidePicker({
  name,
  partner,
  side,
  onChange,
}: {
  readonly name: string;
  readonly partner: string;
  readonly side: Side;
  readonly onChange: (side: Side) => void;
}) {
  const { t } = useI18n();
  const sides: Side[] = ['left', 'right'];
  const label: Record<Side, string> = { left: t('stereoSideLeft'), right: t('stereoSideRight') };
  const keys = useRadioKeys(2, sides.indexOf(side), (i) => {
    const picked = sides[i];
    if (picked) onChange(picked);
  });
  const title = t('stereoPairThis', { name });
  return (
    <SetMenu title={title}>
      <SideBox>
        <Sides role="radiogroup" aria-label={title}>
          {sides.map((s, i) => (
            <SideButton
              key={s}
              type="button"
              role="radio"
              aria-checked={s === side}
              tabIndex={keys.tabIndex(i)}
              $on={s === side}
              onClick={() => onChange(s)}
              onKeyDown={keys.onKeyDown}
            >
              <Letter $on={s === side} aria-hidden="true">
                {label[s].charAt(0).toUpperCase()}
              </Letter>
              {label[s]}
            </SideButton>
          ))}
        </Sides>
        <Then>
          {t('stereoPairOther', {
            name: partner,
            side: side === 'left' ? t('stereo_right') : t('stereo_left'),
          })}
        </Then>
      </SideBox>
    </SetMenu>
  );
}

export function StereoPair({ room, rooms, onChanged }: Props): ReactElement | null {
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
        title={t('stereoPairPartner')}
        marked
        value={chosen.id}
        options={candidates.map((r) => ({
          value: r.id,
          label: r.name,
          art: <ModelArt model={r.model} />,
        }))}
        onChange={setPick}
      />
      <SidePicker name={room.name} partner={chosen.name} side={side} onChange={setSide} />
      <PairButton
        label={t('stereoPairStart')}
        busy={busy}
        error={error}
        onClick={async () => pair(chosen)}
      />
    </>
  );
}

function PairButton({
  label,
  busy,
  error,
  onClick,
}: {
  readonly label: string;
  readonly busy: boolean;
  readonly error: string;
  readonly onClick: () => Promise<void>;
}) {
  return (
    <Footer>
      {error && <Hint error>{error}</Hint>}
      <Button
        wide
        variant="stockholm"
        disabled={busy}
        onClick={() => {
          void onClick();
        }}
      >
        {label}
      </Button>
    </Footer>
  );
}
