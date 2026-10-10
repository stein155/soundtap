// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef } from 'react';
import { getSettings, saveSettings } from '../lib/api';
import { useI18n } from '../lib/i18n';
import { PREF, readPref } from '../lib/storage';
import { speakerArg, type Room } from './useRooms';

// A speaker's panel speaks the speaker's language, as stock's does. Picking a language here
// sets it on every speaker, as each comes online. A browser that picked none only moves a speaker still on
// the default English, so a second browser does not undo the household's choice.
export function useSpeakerLanguage(rooms: Room[]) {
  const { lang } = useI18n();
  const roomsRef = useRef(rooms);
  useEffect(() => {
    roomsRef.current = rooms;
  }, [rooms]);
  const online = () => roomsRef.current.filter((r) => r.online);

  // The speakers that have this browser's pick; null until it picks. A speaker offline at
  // that moment, or one that refused the write, gets it when it next shows up online.
  const said = useRef(lang);
  const told = useRef<Set<string> | null>(null);
  useEffect(() => {
    if (said.current !== lang) {
      said.current = lang;
      told.current = new Set();
    }
    const sent = told.current;
    if (!sent) return;
    for (const r of rooms) {
      if (!r.online || sent.has(r.id)) continue;
      sent.add(r.id);
      void saveSettings({ language: lang }, speakerArg(r)).catch(() => sent.delete(r.id));
    }
  }, [lang, rooms]);

  const anyOnline = rooms.some((r) => r.online);
  const offered = useRef(false);
  useEffect(() => {
    if (offered.current || !anyOnline || lang === 'en' || readPref(PREF.lang)) return;
    offered.current = true;
    for (const r of online()) {
      const speaker = speakerArg(r);
      void getSettings(speaker)
        .then((s) => (s?.language === 'en' ? saveSettings({ language: lang }, speaker) : null))
        .catch(() => null);
    }
  }, [anyOnline, lang]);
}
