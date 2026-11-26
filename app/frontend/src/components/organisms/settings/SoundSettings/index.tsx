// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState, type ReactElement } from 'react';
import { Slider } from '../../../atoms/Slider';
import { Hint } from '../../../atoms/Hint';
import { SetSwitch } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { useI18n } from '../../../../lib/i18n';
import { formatBass } from '../../../../lib/format';
import { hasBass } from '../../../../lib/summaries';
import { failureText } from '../../../../lib/failure';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import { getTone, setTone, startAdaptiq } from '../../../../lib/api/settings';
import { getChannel, setChannel, type Channel } from '../../../../lib/api/multiroom';
import type { BassCaps, Soundbar, Tone } from '../../../../lib/types';
import { Card, Head, Inset, Level, Name, Scale } from './styled';
import { AudioModeChoice, TvChoices } from './SoundbarChoices';
import { SoundbarLipSync } from './SoundbarLipSync';
import { useTrailingSend } from './useTrailingSend';

// Whether a speaker has anything for the Sound page: a soundbar, a bass range or the
// stereo output switch. Stock's Wireless Link has none of these.
export const hasSound = (
  caps: BassCaps,
  soundbar: Soundbar | null | undefined,
  mono: boolean
): boolean => soundbar !== undefined || hasBass(caps) || mono;

// The Sound page, opened from the Speaker group of every speaker that has one: on a
// soundbar its tone and audio mode, the TV, its extra speakers and room calibration;
// elsewhere bass within the range the speaker reports, filling from its neutral value;
// and, as on stock, stereo or mono on a speaker that has both (mono). Nothing at all when
// the speaker has none of these.
export function SoundSettings({
  bass,
  caps,
  soundbar,
  speaker,
  name = '',
  mono = false,
  onBass,
}: {
  readonly bass: number;
  readonly caps: BassCaps;
  readonly soundbar?: Soundbar | null | undefined;
  readonly speaker?: string | undefined;
  // The speaker's name, for what a failure says.
  readonly name?: string;
  readonly mono?: boolean;
  readonly onBass: (bass: number) => void;
}): ReactElement | null {
  const { t } = useI18n();
  const bar = soundbar !== undefined;
  if (!hasSound(caps, soundbar, mono)) return null;
  // Keyed by speaker: a card must not show or write one bar's value on the next one's
  // screen while its own read is on the way.
  const key = String(speaker);
  if (bar)
    return (
      <>
        <SetMenu title={t('soundTone')}>
          <SoundbarTone key={`tone-${key}`} speaker={speaker} />
          <AudioModeChoice key={`mode-${key}`} speaker={speaker} />
        </SetMenu>
        <SetMenu title={t('soundTv')}>
          <TvChoices key={`tv-${key}`} speaker={speaker} />
          <SoundbarLipSync key={`lipsync-${key}`} speaker={speaker} />
        </SetMenu>
        {soundbar && (
          <SetMenu title={t('soundExtras')}>
            <SoundbarSpeakers bar={soundbar} />
          </SetMenu>
        )}
        <SetMenu title="ADAPTiQ">
          <SoundbarAdaptiq speaker={speaker} name={name} />
        </SetMenu>
      </>
    );
  return (
    <>
      {hasBass(caps) && (
        <SetMenu title={t('soundTone')}>
          <Card>
            <Head>
              <Name>{t('bass')}</Name>
              <Level>{formatBass(bass)}</Level>
            </Head>
            <Inset>
              <Slider
                tall
                value={bass}
                onChange={onBass}
                label={t('bass')}
                min={caps.min}
                max={caps.max}
                valueText={formatBass(bass)}
              />
            </Inset>
            <Scale>
              <span>{formatBass(caps.min)}</span>
              <span>{formatBass(caps.max)}</span>
            </Scale>
          </Card>
        </SetMenu>
      )}
      {mono && (
        <SetMenu title={t('soundOutput')}>
          <MonoSwitch key={`mono-${key}`} speaker={speaker} />
        </SetMenu>
      )}
    </>
  );
}

// Starts the soundbar's room calibration: the bar runs it itself and returns to its last
// source, so the app only has to ask. A start that did not happen says why: the bar said no,
// or it did not answer.
function SoundbarAdaptiq({
  speaker,
  name,
}: {
  readonly speaker?: string | undefined;
  readonly name: string;
}) {
  const { t } = useI18n();
  const [error, setError] = useState('');
  const start = async () => {
    setError('');
    try {
      await startAdaptiq(speaker);
    } catch (e) {
      const status =
        typeof e === 'object' && e !== null && 'status' in e && typeof e.status === 'number'
          ? e.status
          : undefined;
      const refused = status !== undefined && status >= 400 && status < 500;
      setError(failureText(e, t(refused ? 'actionRefused' : 'actionFailed', { name }), t));
    }
  };
  return (
    <Card>
      <ConfirmAction
        label={t('adaptiqRun')}
        warning={t('adaptiqWarning')}
        confirmLabel={t('adaptiqStart')}
        onConfirm={start}
      />
      {error && (
        <Hint error role="alert">
          {error}
        </Hint>
      )}
    </Card>
  );
}

// Whether the soundbar's bass module and rear speakers are paired and connected.
function SoundbarSpeakers({ bar }: { readonly bar: Soundbar }) {
  const { t } = useI18n();
  const state = (paired: boolean, connected: boolean) => {
    if (!paired) return t('soundbarUnpaired');
    return connected ? t('soundbarConnected') : t('soundbarOff');
  };
  return (
    <Card>
      <Head>
        <Name>{t('soundbarBass')}</Name>
        <Level $changed={bar.bassConnected}>{state(bar.bassPaired, bar.bassConnected)}</Level>
      </Head>
      <Head>
        <Name>{t('soundbarRears')}</Name>
        <Level $changed={bar.rearsConnected}>{state(bar.rearsPaired, bar.rearsConnected)}</Level>
      </Head>
    </Card>
  );
}

const TONE_CONTROLS = [
  ['bass', 'bass'],
  ['treble', 'treble'],
  ['centre', 'soundbarCentre'],
  ['rear', 'soundbarRears'],
] as const;

// The soundbar's bass, treble, centre and rear levels, within the ranges it reports. Sent
// to the soundbar shortly after the last move, not saved with the form. A control whose
// range is empty is not shown.
function SoundbarTone({ speaker }: { readonly speaker?: string | undefined }) {
  const { t } = useI18n();
  const [tone, setOwn] = useState<Tone | null>(null);
  const [error, setError] = useState('');
  const later = useTrailingSend(300);
  useEffect(() => {
    // The first read only wakes the soundbar's answer, so ask again once if it had none.
    let alive = true;
    let again: number | undefined;
    const load = (retry: boolean) => {
      void getTone(speaker).then((got) => {
        if (!alive) return;
        if (got || !retry) setOwn(got);
        else again = window.setTimeout(() => load(false), 1500);
      });
    };
    load(true);
    return () => {
      alive = false;
      window.clearTimeout(again);
    };
  }, [speaker]);
  if (!tone) return null;
  const change = (key: keyof Tone, value: number) => {
    const c = tone[key];
    const snapped = c.step > 1 ? c.min + Math.round((value - c.min) / c.step) * c.step : value;
    const next = { ...tone, [key]: { ...c, value: Math.min(c.max, snapped) } };
    setOwn(next);
    later(() => {
      setError('');
      setTone(
        {
          bass: next.bass.value,
          treble: next.treble.value,
          centre: next.centre.value,
          rear: next.rear.value,
        },
        speaker
      ).catch((e: unknown) => setError(failureText(e, t('saveFailed'), t)));
    });
  };
  return (
    <>
      {TONE_CONTROLS.filter(([key]) => tone[key].max > tone[key].min).map(([key, label]) => {
        const c = tone[key];
        return (
          <Card key={key}>
            <Head>
              <Name>{t(label)}</Name>
              <Level $changed={c.value !== 0}>{formatBass(c.value)}</Level>
            </Head>
            <Slider
              tall
              value={c.value}
              onChange={(v) => change(key, v)}
              label={t(label)}
              min={c.min}
              max={c.max}
              step={c.step === 0 ? 1 : c.step}
              origin={0}
              valueText={formatBass(c.value)}
            />
            <Scale>
              <span>{formatBass(c.min)}</span>
              <span>{formatBass(c.max)}</span>
            </Scale>
          </Card>
        );
      })}
      {error && <Hint error>{error}</Hint>}
    </>
  );
}

// Stereo or mono, saved on the speaker at once like the stereo pair: a row of the Sound
// group. Half of a pair plays one channel already, so the switch is not shown there.
export function MonoSwitch({
  speaker,
}: {
  readonly speaker?: string | undefined;
}): ReactElement | null {
  const { t } = useI18n();
  const [channel, setOwn] = useState<Channel | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    // An answer for the speaker before, or after leaving, does not count.
    let alive = true;
    void getChannel(speaker).then((got) => {
      if (alive) setOwn(got);
    });
    return () => {
      alive = false;
    };
  }, [speaker]);
  if (channel !== 'stereo' && channel !== 'mono') return null;
  const toggle = async () => {
    const next = channel === 'mono' ? 'stereo' : 'mono';
    setError('');
    try {
      await setChannel(next, speaker);
      setOwn(next);
    } catch (e) {
      setError(failureText(e, t('saveFailed'), t));
    }
  };
  return (
    <div>
      <SetSwitch
        label={t('mono')}
        desc={t('monoHint')}
        on={channel === 'stereo'}
        onToggle={() => {
          void toggle();
        }}
      />
      {error && (
        <Card>
          <Hint error>{error}</Hint>
        </Card>
      )}
    </div>
  );
}
