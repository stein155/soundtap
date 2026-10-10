// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { beforeAll, describe, expect, it } from 'vitest';
import { loadLanguage, makeT, type TranslateFn } from './i18n';
import { alarmSummary } from './summaries';
import type { Preset } from './types';

let t: TranslateFn;
beforeAll(async () => {
  await loadLanguage('nl');
  t = makeT('nl');
});
const preset: Preset = { slot: 2, name: 'NPO Radio 2', tuneInId: 's9483', location: '', logo: '' };

describe('alarmSummary', () => {
  it('names the preset it plays', () => {
    expect(alarmSummary({ enabled: true, time: '07:00', slot: 2 }, [null, preset], t)).toBe(
      '07:00 · NPO Radio 2'
    );
  });

  // An alarm that is on still rings with no preset left: it must not read "Off".
  it('says the tone wakes when no preset is left', () => {
    expect(alarmSummary({ enabled: true, time: '07:00', slot: 3 }, [null, null, null], t)).toBe(
      '07:00 · pieptoon'
    );
  });

  it('is off when disabled', () => {
    expect(alarmSummary({ enabled: false, time: '07:00', slot: 2 }, [null, preset], t)).toBe('Uit');
  });
});
