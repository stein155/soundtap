// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it } from 'vitest';
import { updateAvailable, versionLabel } from './version';
import type { VersionInfo } from './types';

const v = (fields: Partial<VersionInfo>): VersionInfo => ({
  version: '',
  os: '',
  app: '',
  commit: '',
  updatable: false,
  updateMode: 'unmanaged',
  api: 1,
  capabilities: [],
  ...fields,
});

describe('versionLabel', () => {
  it('names both lanes', () => {
    expect(versionLabel(v({ version: 'app-v0.1.0', os: '0.1.0', app: '0.1.0' }))).toBe(
      'SoundTap Core 0.1.0 · app 0.1.0'
    );
  });
  it('shows only the app off a speaker', () => {
    expect(versionLabel(v({ version: 'dev', app: 'dev' }))).toBe('app dev');
  });
  it('falls back to the raw version', () => {
    expect(versionLabel(v({ version: 'firmware' }))).toBe('firmware');
    expect(versionLabel(null)).toBe('');
  });
});

describe('updateAvailable', () => {
  it('is true only for a newer stable release', () => {
    expect(updateAvailable('app-v0.1.0', 'app-v0.2.0')).toBe(true);
    expect(updateAvailable('app-v0.9.0', 'app-v0.10.0')).toBe(true);
    expect(updateAvailable('app-v0.2.0', 'app-v0.2.0')).toBe(false);
    expect(updateAvailable('app-v0.3.0', 'app-v0.2.0')).toBe(false);
  });
  it('orders an alpha before its release', () => {
    expect(updateAvailable('app-v0.1.0-alpha.1', 'app-v0.1.0-alpha.2')).toBe(true);
    expect(updateAvailable('app-v0.1.0-alpha.9', 'app-v0.1.0-alpha.10')).toBe(true);
    expect(updateAvailable('app-v0.1.0-alpha.2', 'app-v0.1.0')).toBe(true);
    expect(updateAvailable('app-v0.1.0-alpha.2', 'app-v0.1.0-alpha.2')).toBe(false);
    expect(updateAvailable('app-v0.1.0', 'app-v0.1.0-alpha.2')).toBe(false);
    expect(updateAvailable('app-v0.1.0-alpha.2', 'app-v0.1.0-alpha.1')).toBe(false);
  });
  it('never offers a stable over a dev or beta build', () => {
    expect(updateAvailable('dev', 'app-v0.2.0')).toBe(false);
    expect(updateAvailable('beta-pr-12', 'app-v0.2.0')).toBe(false);
    expect(updateAvailable('app-v0.1.0', undefined)).toBe(false);
  });
});
