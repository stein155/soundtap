// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { Settings } from '../lib/types';

type Overrides = Omit<Partial<Settings>, 'mqtt'> & { mqtt?: Partial<Settings['mqtt']> };

// A complete /api/settings answer: the API always sends these fields, so a test
// states only what it cares about.
export const settingsFixture = ({ mqtt, ...rest }: Overrides = {}): Settings => ({
  language: 'en',
  autoOff: 0,
  auxName: '',
  mqtt: {
    enabled: false,
    host: '',
    port: 1883,
    username: '',
    hasPassword: false,
    baseTopic: '',
    discoveryPrefix: '',
    tls: false,
    ...mqtt,
  },
  hasDisplay: false,
  display: {
    clockOff: false,
    hour12: false,
    city: '',
    lat: 0,
    lon: 0,
    timezone: '',
    weatherOn: false,
  },
  alarm: { enabled: false, time: '07:00', slot: 1 },
  multiroom: { keepGroups: false, syncVolume: false },
  integrations: { spotify: true, airplay: true },
  crashReports: false,
  ...rest,
});
