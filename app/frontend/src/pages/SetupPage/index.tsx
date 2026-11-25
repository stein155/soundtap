// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState, type ReactElement } from 'react';
import { Button } from '../../components/atoms/Button';
import { Icon } from '../../components/atoms/Icon';
import { Note } from '../../components/atoms/Note';
import { TextField } from '../../components/atoms/TextField';
import { LabeledField } from '../../components/molecules/LabeledField';
import { ModelArt } from '../../components/molecules/ModelArt';
import { WifiPicker } from '../../components/organisms/settings/NetworkSettings';
import { AppShell } from '../../components/templates/AppShell';
import { getSettings, getWifiJoinFailed, saveSettings } from '../../lib/api';
import { LANGS, useI18n, type I18nKey } from '../../lib/i18n';
import { NAME_MAX_LENGTH } from '../../lib/limits';
import { clean } from '../../lib/text';
import './setup.css';

// The speaker that served the page (an empty target addresses it, see lib/base.ts).
const LOCAL = '';

// The Wi-Fi manager's reasons for a failed join, and what to tell the person about each.
const JOIN_FAILED: Record<string, I18nKey> = {
  not_found: 'wifiFailNotFound',
  wrong_password: 'wifiFailPassword',
  refused: 'wifiFailRefused',
  no_address: 'wifiFailAddress',
  timeout: 'wifiFailTimeout',
};

const STEPS = 2;

// The bar over both steps: which step this is, the language, and a line per step.
function StepBar({ step }: { step: number }) {
  const { t, lang, setLang } = useI18n();
  return (
    <>
      <div className="setup-bar">
        <span />
        <p>{t('setupStep', { n: step, total: STEPS })}</p>
        <label className="setup-lang">
          <span>{LANGS.find((l) => l.code === lang)?.label}</span>
          <select aria-label={t('language')} value={lang} onChange={(e) => setLang(e.target.value)}>
            {LANGS.map((l) => (
              <option key={l.code} value={l.code}>
                {l.label}
              </option>
            ))}
          </select>
        </label>
      </div>
      <div className="setup-steps" aria-hidden="true">
        {Array.from({ length: STEPS }, (_, i) => (
          <span key={i} data-done={i < step || undefined} />
        ))}
      </div>
    </>
  );
}

// What to know before Connect: the phone loses the speaker, and where to find it after.
function PhoneNotice({ host }: { host: string }) {
  const { t } = useI18n();
  const [before, after] = t('setupPhoneLoses').split('{host}');
  return (
    <div className="setup-notice">
      <Icon.info width="18" height="18" aria-hidden="true" />
      <p>
        {before}
        <b>{host}</b>
        {after}
      </p>
    </div>
  );
}

// The speaker's name, read once from the speaker; the stored name fills the field unless
// the person already started typing. Only a name the person changed is saved, and a
// refusal does not stop the Wi-Fi hand-over: a speaker that is already paired accepts only
// the Wi-Fi from a phone on its hotspot, and an unchanged pre-filled name used to block it.
// Setup always talks to the speaker serving this page, never a room picked earlier in the
// normal UI, hence the explicit '' target here and below.
function useSetupSpeaker() {
  const [name, setName] = useState('');
  const [edited, setEdited] = useState(false);
  const [stored, setStored] = useState('');
  const [model, setModel] = useState<string>();
  const [host, setHost] = useState<string>();
  const [nameFailed, setNameFailed] = useState(false);
  const [joinFailed, setJoinFailed] = useState<I18nKey>();
  const typing = useRef(false);
  useEffect(() => {
    void getWifiJoinFailed(LOCAL).then((reason) => setJoinFailed(JOIN_FAILED[reason]));
    void getSettings(LOCAL).then((settings) => {
      setModel(settings?.model);
      setHost(settings?.host);
      if (!settings?.name) return;
      setStored(settings.name);
      if (!typing.current) setName(settings.name);
    });
  }, []);
  const type = (value: string) => {
    typing.current = true;
    setEdited(true);
    setName(value);
  };
  const saveName = async () => {
    const next = clean(name);
    setNameFailed(false);
    if (!edited || !next || next === stored) return;
    await saveSettings({ name: next }, LOCAL).catch(() => setNameFailed(true));
  };
  return { name, type, model, host, nameFailed, joinFailed, saveName };
}

type Speaker = ReturnType<typeof useSetupSpeaker>;

// Step 1: the speaker, and its name.
function NameStep({ speaker, onNext }: { speaker: Speaker; onNext: () => void }) {
  const { t } = useI18n();
  return (
    <>
      <header className="setup-head">
        <StepBar step={1} />
        <div className="setup-intro setup-welcome">
          <div className="setup-disc">
            <ModelArt model={speaker.model} />
          </div>
          <h1>{t('setupWelcome')}</h1>
          <p>{t('setupTagline')}</p>
        </div>
      </header>
      <div className="setup-body">
        <LabeledField id="setup-name" label={t('name')}>
          <TextField
            id="setup-name"
            value={speaker.name}
            onChange={(e) => speaker.type(e.target.value)}
            placeholder={t('namePlaceholder')}
            maxLength={NAME_MAX_LENGTH}
            autoComplete="off"
          />
        </LabeledField>
        <div className="setup-foot">
          <Button variant="stockholm" wide onClick={onNext}>
            {t('setupNext')}
          </Button>
        </div>
      </div>
    </>
  );
}

// Step 2: the home network. Nothing here may need the internet.
function WifiStep({ speaker }: { speaker: Speaker }) {
  const { t } = useI18n();
  const host = speaker.host || 'soundtap.local';
  return (
    <>
      <header className="setup-head">
        <StepBar step={2} />
        <div className="setup-intro">
          {speaker.name && <p className="setup-name">{speaker.name}</p>}
          <h1>{t('setupWifiTitle')}</h1>
          <p>{t('setupWifiBody', { host })}</p>
        </div>
      </header>
      <div className="setup-body">
        {speaker.joinFailed && <Note>{t(speaker.joinFailed)}</Note>}
        <WifiPicker
          network={null}
          speaker={LOCAL}
          beforeConnect={speaker.saveName}
          fixedSurvey
          docked
          host={speaker.host}
          notice={
            <>
              {speaker.nameFailed && <Note>{t('setupNameNotSaved')}</Note>}
              <PhoneNotice host={host} />
            </>
          }
        />
      </div>
    </>
  );
}

// First run, on the speaker's own SoundTap-Setup access point (?setup=wifi), in two steps:
// name the speaker, then put it on the home network. Each step is a column down to its
// button at the bottom of the screen.
export function SetupPage(): ReactElement {
  const speaker = useSetupSpeaker();
  const [step, setStep] = useState(1);
  return (
    <AppShell
      page={
        <div className="setup-page">
          {step === 1 ? (
            <NameStep speaker={speaker} onNext={() => setStep(2)} />
          ) : (
            <WifiStep speaker={speaker} />
          )}
        </div>
      }
    />
  );
}
