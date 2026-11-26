// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState, type ReactElement } from 'react';
import { Button } from 'react-aria-components';
import { useI18n } from '../../../lib/i18n';
import { getChannel, setChannel, type Channel } from '../../../lib/api/multiroom';
import { speakerArg, type Room } from '../../../hooks/useRooms';

// Half of a stereo pair whose partner is gone plays one channel on its own. The card says
// so and offers both channels back, without a trip to settings.
export function PairAlone({
  room,
  grouped,
}: {
  readonly room: Room;
  readonly grouped: boolean;
}): ReactElement | null {
  const { t } = useI18n();
  const speaker = speakerArg(room);
  const [channel, setOwn] = useState<Channel | null>(null);
  // While the speaker is being told, the button waits; a refusal says so on the card.
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (grouped || !room.online) return undefined;
    let current = true;
    void getChannel(speaker).then((c) => current && setOwn(c));
    return () => {
      current = false;
    };
  }, [speaker, grouped, room.online]);
  if (grouped || (channel !== 'left' && channel !== 'right')) return null;
  const side = channel === 'left' ? t('stereo_left') : t('stereo_right');
  const unpair = async () => {
    setBusy(true);
    setFailed(false);
    try {
      await setChannel('stereo', speaker);
      setOwn('stereo');
    } catch {
      setFailed(true);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="grp">
      <p className="members">{t('stereoPairAlone', { side })}</p>
      {failed && (
        <p className="members" role="alert">
          {t('actionFailed', { name: room.name })}
        </p>
      )}
      <Button
        className="pill"
        isDisabled={busy}
        onPress={() => {
          void unpair();
        }}
      >
        {t('stereoPairEnd')}
      </Button>
    </div>
  );
}
