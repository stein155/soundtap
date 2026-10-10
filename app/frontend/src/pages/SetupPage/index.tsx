// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState } from 'react';
import { FormSection } from '../../components/atoms/FormSection';
import { Note } from '../../components/atoms/Note';
import { TextField } from '../../components/atoms/TextField';
import { ModelArt } from '../../components/molecules/ModelArt';
import { Section } from '../../components/molecules/Section';
import { WifiPicker } from '../../components/organisms/settings/NetworkSettings';
import { AppShell } from '../../components/templates/AppShell';
import { getSettings, getWifiJoinFailed, saveSettings } from '../../lib/api';
import { useI18n, type I18nKey } from '../../lib/i18n';
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

// First run, on the speaker's own SoundTap-Setup access point (?setup=wifi): name the
// speaker and put it on the home network. Nothing here may need the internet.
export function SetupPage() {
  const { t } = useI18n();
  const [name, setName] = useState('');
  const [edited, setEdited] = useState(false);
  const [stored, setStored] = useState('');
  const [model, setModel] = useState<string>();
  const [host, setHost] = useState<string>();
  const [nameFailed, setNameFailed] = useState(false);
  const [joinFailed, setJoinFailed] = useState<I18nKey>();
  // The stored name fills the field, unless the person already started typing: read once,
  // and checked against what is typed when the answer lands, not when it was asked.
  // Setup always talks to the speaker serving this page, never a room picked earlier in
  // the normal UI, hence the explicit '' target here and below.
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
  // The name is saved first, but only a name the person changed, and a refusal does not
  // stop the Wi-Fi hand-over: a speaker that is already paired accepts only the Wi-Fi
  // from a phone on its hotspot, and an unchanged pre-filled name used to block it.
  const saveName = async () => {
    const next = clean(name);
    setNameFailed(false);
    if (!edited || !next || next === stored) return;
    await saveSettings({ name: next }, LOCAL).catch(() => setNameFailed(true));
  };
  return (
    <AppShell
      header={
        <header className="setup-head">
          <div className="setup-disc">
            <ModelArt model={model} />
          </div>
          {name && <p className="setup-name">{name}</p>}
          <h1>{t('setupWelcome')}</h1>
          <p>{t('setupTagline')}</p>
        </header>
      }
    >
      <div className="setup">
        <Section title={t('thisRadio')}>
          <TextField
            value={name}
            onChange={(e) => {
              typing.current = true;
              setEdited(true);
              setName(e.target.value);
            }}
            placeholder={t('namePlaceholder')}
            aria-label={t('name')}
            maxLength={NAME_MAX_LENGTH}
            autoComplete="off"
          />
          <FormSection spaced>{t('setupWifiTitle')}</FormSection>
          <Note>{t('setupWifiBody')}</Note>
          {joinFailed && <Note>{t(joinFailed)}</Note>}
          <WifiPicker
            network={null}
            speaker={LOCAL}
            beforeConnect={saveName}
            fixedSurvey
            host={host}
          />
          {nameFailed && <Note>{t('setupNameNotSaved')}</Note>}
        </Section>
        <footer className="setup-foot">{t('setupFooter')}</footer>
      </div>
    </AppShell>
  );
}
