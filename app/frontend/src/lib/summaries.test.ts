// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { beforeAll, describe, expect, it } from 'vitest';
import { loadLanguage, makeT, type TranslateFn } from './i18n';
import { alarmSummary, bluetoothSummary } from './summaries';
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

describe('bluetoothSummary', () => {
  const status = { adapter: true, alias: 'Keuken', powered: true, discoverable: false };

  it('names the paired devices', () => {
    const devices = [{ address: '02:00:00:00:00:01', name: 'Telefoon', connected: false }];
    expect(bluetoothSummary({ ...status, devices }, t)).toBe('Telefoon');
  });

  // A speaker with nothing paired sent "devices": null, and the settings page failed on it.
  it('says nothing is paired when the list is null or empty', () => {
    expect(bluetoothSummary({ ...status, devices: null }, t)).toBe(t('btNone'));
    expect(bluetoothSummary({ ...status, devices: [] }, t)).toBe(t('btNone'));
  });
});
