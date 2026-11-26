// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState, type ReactElement } from 'react';
import { Hint } from '../../../atoms/Hint';
import { SegmentedControl } from '../../../molecules/SegmentedControl';
import { useI18n, type I18nKey } from '../../../../lib/i18n';
import { failureText } from '../../../../lib/failure';
import {
  getSoundbarChoice,
  setSoundbarChoice,
  type SoundbarChoice,
} from '../../../../lib/api/settings';
import { Card, Head, Name } from './styled';

const AUDIO_MODES = [
  ['normal', 'audioModeNormal'],
  ['dialog', 'audioModeDialog'],
  ['night', 'audioModeNight'],
  ['direct', 'audioModeDirect'],
] as const;

// Stock also has "fine"; the app offers what the Bose app did.
const CEC_MODES = [
  ['on', 'cecOn'],
  ['alternate', 'cecAlternate'],
  ['off', 'off'],
] as const;

// The remote's source button that picks the bar's HDMI input; the numbers need no words.
const HDMI_INPUTS = [
  ['none', 'hdmiInputNone'],
  ['1', null],
  ['2', null],
  ['3', null],
] as const;

// One of the soundbar's one-of-a-few settings (audio mode, HDMI-CEC), as its own app offered:
// sent at once, shown back as the bar reports it. Hidden on a speaker without it; with
// nothing selected while the bar has not said which one it uses.
function SoundbarChoiceCard({
  speaker,
  choice,
  title,
  options,
}: {
  readonly speaker?: string | undefined;
  readonly choice: SoundbarChoice;
  readonly title: I18nKey;
  readonly options: readonly (readonly [string, I18nKey | null])[];
}) {
  const { t } = useI18n();
  const [mode, setOwn] = useState<string | null>(null);
  const [error, setError] = useState('');
  const latest = useRef(0); // only the newest choice may undo itself on a failure
  useEffect(() => {
    let alive = true;
    void getSoundbarChoice(choice, speaker).then((got) => alive && setOwn(got));
    return () => {
      alive = false;
    };
  }, [choice, speaker]);
  if (mode === null) return null;
  const choose = async (next: string) => {
    const was = mode;
    latest.current += 1;
    const mine = latest.current;
    setOwn(next);
    setError('');
    try {
      await setSoundbarChoice(choice, next, speaker);
    } catch (e) {
      if (mine !== latest.current) return;
      setOwn(was);
      setError(failureText(e, t('saveFailed'), t));
    }
  };
  return (
    <Card>
      <Head>
        <Name>{t(title)}</Name>
      </Head>
      <SegmentedControl
        label={t(title)}
        value={mode}
        options={options.map(([value, label]) => ({ value, label: label ? t(label) : value }))}
        onChange={(m) => {
          void choose(m);
        }}
      />
      {error && <Hint error>{error}</Hint>}
    </Card>
  );
}

// The soundbar's audio mode, hidden until the bar has reported it.
export function AudioModeChoice({
  speaker,
}: {
  readonly speaker?: string | undefined;
}): ReactElement {
  return (
    <SoundbarChoiceCard
      speaker={speaker}
      choice="audio-mode"
      title="audioMode"
      options={AUDIO_MODES}
    />
  );
}

// The soundbar's HDMI-CEC mode and HDMI input button, each hidden until the bar has reported it.
export function TvChoices({ speaker }: { readonly speaker?: string | undefined }): ReactElement {
  return (
    <>
      <SoundbarChoiceCard speaker={speaker} choice="cec-mode" title="cecMode" options={CEC_MODES} />
      <SoundbarChoiceCard
        speaker={speaker}
        choice="hdmi-input"
        title="hdmiInput"
        options={HDMI_INPUTS}
      />
    </>
  );
}
