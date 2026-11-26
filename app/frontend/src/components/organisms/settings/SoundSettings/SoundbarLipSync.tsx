// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState, type ReactElement } from 'react';
import { Slider } from '../../../atoms/Slider';
import { Hint } from '../../../atoms/Hint';
import { useI18n } from '../../../../lib/i18n';
import { failureText } from '../../../../lib/failure';
import { getLipSync, setLipSync } from '../../../../lib/api/settings';
import { Card, Head, Level, Name, Scale } from './styled';
import { useTrailingSend } from './useTrailingSend';

// How long the soundbar's sound waits for the TV picture. Sent shortly after the
// last move, like the tone levels; 0 until the bar has said.
export function SoundbarLipSync({
  speaker,
}: {
  readonly speaker?: string | undefined;
}): ReactElement | null {
  const { t } = useI18n();
  const [delay, setOwn] = useState<{ ms: number | null; max: number } | null>(null);
  const [error, setError] = useState('');
  const later = useTrailingSend(300);
  useEffect(() => {
    let alive = true;
    void getLipSync(speaker).then((got) => alive && setOwn(got));
    return () => {
      alive = false;
    };
  }, [speaker]);
  if (!delay) return null;
  const ms = delay.ms ?? 0;
  const change = (next: number) => {
    setOwn({ ...delay, ms: next });
    later(() => {
      setError('');
      setLipSync(next, speaker).catch((e: unknown) => setError(failureText(e, t('saveFailed'), t)));
    });
  };
  return (
    <Card>
      <Head>
        <Name>{t('lipSync')}</Name>
        <Level $changed={ms !== 0}>{ms} ms</Level>
      </Head>
      <Slider
        tall
        value={ms}
        onChange={change}
        label={t('lipSync')}
        min={0}
        max={delay.max}
        step={10}
        origin={0}
        valueText={`${ms} ms`}
      />
      <Scale>
        <span>0 ms</span>
        <span>{delay.max} ms</span>
      </Scale>
      {error && <Hint error>{error}</Hint>}
    </Card>
  );
}
