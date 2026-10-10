// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState } from 'react';
import { Slider } from '../../../atoms/Slider';
import { NameRow } from '../SpeakerSettings/NameRow';
import { Hint } from '../../../atoms/Hint';
import { SetSwitch } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { cardShadow, useSurface } from '../../../../hooks/useSurface';
import { useI18n } from '../../../../lib/i18n';
import { formatBass } from '../../../../lib/format';
import { hasBass } from '../../../../lib/summaries';
import { failureText } from '../../../../lib/failure';
import { ConfirmAction } from '../../../molecules/ConfirmAction';
import {
  getLipSync,
  getTone,
  setLipSync,
  setTone,
  startAdaptiq,
} from '../../../../lib/api/settings';
import { getChannel, setChannel, type Channel } from '../../../../lib/api/multiroom';
import type { BassCaps, Soundbar, Tone } from '../../../../lib/types';
import { Card, Head, Level, Name, Scale } from './styled';
import { SoundbarChoices } from './SoundbarChoices';

// Bass, within the range the speaker reports, filling from its neutral value; then whether
// the speaker goes to standby when idle (autoOff: 0 is the default 20 minutes, below 0 never),
// and on a speaker with an AUX input what that input is called.
export function SoundSettings({
  bass,
  caps,
  autoOff,
  auxName,
  soundbar,
  speaker,
  onBass,
  onAutoOff,
  onAuxName,
}: {
  bass: number;
  caps: BassCaps;
  autoOff: number;
  auxName: string | null;
  soundbar?: Soundbar | null;
  speaker?: string;
  onBass: (bass: number) => void;
  onAutoOff: (minutes: number) => void;
  onAuxName: (name: string) => void;
}) {
  const { t } = useI18n();
  const surface = useSurface();
  return (
    <>
      {soundbar && <SoundbarSpeakers bar={soundbar} />}
      {/* Keyed by speaker: a card must not show or write one bar's value on the next
          one's screen while its own read is on the way. */}
      {soundbar !== undefined && <SoundbarChoices key={`choices-${speaker}`} speaker={speaker} />}
      {soundbar !== undefined && <SoundbarTone speaker={speaker} />}
      {soundbar !== undefined && <SoundbarLipSync key={`lipsync-${speaker}`} speaker={speaker} />}
      {soundbar !== undefined && <SoundbarAdaptiq speaker={speaker} />}
      {hasBass(caps) && (
        <Card $shadow={cardShadow(surface)}>
          <Head>
            <Name>{t('bass')}</Name>
            <Level $changed={bass !== caps.default}>{formatBass(bass)}</Level>
          </Head>
          <Slider
            tall
            value={bass}
            onChange={onBass}
            label={t('bass')}
            min={caps.min}
            max={caps.max}
            origin={caps.default}
            valueText={formatBass(bass)}
          />
          <Scale>
            <span>{formatBass(caps.min)}</span>
            <span>{formatBass(caps.max)}</span>
          </Scale>
        </Card>
      )}
      <SetMenu>
        <SetSwitch
          label={t('autoOff')}
          on={autoOff >= 0}
          onToggle={() => onAutoOff(autoOff >= 0 ? -1 : 0)}
        />
      </SetMenu>
      <Hint>{t('autoOffHint')}</Hint>
      {auxName !== null && (
        <NameRow
          label={t('auxName')}
          value={auxName}
          placeholder={t('src_aux')}
          onChange={onAuxName}
        />
      )}
    </>
  );
}

// Starts the soundbar's room calibration: the bar runs it itself and returns to its last
// source, so the app only has to ask.
function SoundbarAdaptiq({ speaker }: { speaker?: string }) {
  const { t } = useI18n();
  const surface = useSurface();
  return (
    <Card $shadow={cardShadow(surface)}>
      <ConfirmAction
        label={t('adaptiqRun')}
        warning={t('adaptiqWarning')}
        confirmLabel={t('adaptiqStart')}
        onConfirm={async () => {
          await startAdaptiq(speaker);
        }}
      />
    </Card>
  );
}

// Whether the soundbar's bass module and rear speakers are paired and connected.
function SoundbarSpeakers({ bar }: { bar: Soundbar }) {
  const { t } = useI18n();
  const surface = useSurface();
  const state = (paired: boolean, connected: boolean) =>
    !paired ? t('soundbarUnpaired') : connected ? t('soundbarConnected') : t('soundbarOff');
  return (
    <Card $shadow={cardShadow(surface)}>
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
function SoundbarTone({ speaker }: { speaker?: string }) {
  const { t } = useI18n();
  const surface = useSurface();
  const [tone, setOwn] = useState<Tone | null>(null);
  const [error, setError] = useState('');
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => {
    // The first read only wakes the soundbar's answer, so ask again once if it had none.
    let alive = true;
    const load = (retry: boolean) =>
      void getTone(speaker).then((got) => {
        if (!alive) return;
        if (got || !retry) setOwn(got);
        else window.setTimeout(() => load(false), 1500);
      });
    load(true);
    return () => {
      alive = false;
      window.clearTimeout(timer.current);
    };
  }, [speaker]);
  if (!tone) return null;
  const change = (key: keyof Tone, value: number) => {
    const c = tone[key];
    const snapped = c.step > 1 ? c.min + Math.round((value - c.min) / c.step) * c.step : value;
    const next = { ...tone, [key]: { ...c, value: Math.min(c.max, snapped) } };
    setOwn(next);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
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
    }, 300);
  };
  return (
    <>
      {TONE_CONTROLS.filter(([key]) => tone[key].max > tone[key].min).map(([key, label]) => {
        const c = tone[key];
        return (
          <Card key={key} $shadow={cardShadow(surface)}>
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
              step={c.step || 1}
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

// How long the soundbar's sound waits for the TV picture. Sent shortly after the
// last move, like the tone levels; 0 until the bar has said.
function SoundbarLipSync({ speaker }: { speaker?: string }) {
  const { t } = useI18n();
  const surface = useSurface();
  const [delay, setOwn] = useState<{ ms: number | null; max: number } | null>(null);
  const [error, setError] = useState('');
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => {
    let alive = true;
    void getLipSync(speaker).then((got) => alive && setOwn(got));
    return () => {
      alive = false;
      window.clearTimeout(timer.current);
    };
  }, [speaker]);
  if (!delay) return null;
  const ms = delay.ms ?? 0;
  const change = (next: number) => {
    setOwn({ ...delay, ms: next });
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      setError('');
      setLipSync(next, speaker).catch((e: unknown) => setError(failureText(e, t('saveFailed'), t)));
    }, 300);
  };
  return (
    <Card $shadow={cardShadow(surface)}>
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

// Stereo or mono, saved on the speaker at once like the stereo pair. Half of a pair plays
// one channel already, so the switch is not shown there.
export function MonoSwitch({ speaker }: { speaker?: string }) {
  const { t } = useI18n();
  const [channel, setOwn] = useState<Channel | null>(null);
  const [error, setError] = useState('');
  useEffect(() => {
    void getChannel(speaker).then(setOwn);
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
    <>
      <SetMenu>
        <SetSwitch label={t('mono')} on={channel === 'stereo'} onToggle={() => void toggle()} />
      </SetMenu>
      <Hint error={!!error}>{error || t('monoHint')}</Hint>
    </>
  );
}
