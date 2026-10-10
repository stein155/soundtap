// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { act, renderHook, waitFor } from '@testing-library/react';
import { useSpeakerInfo } from './useSpeakerInfo';
import { useSpeakerSettings } from './useSpeakerSettings';
import * as api from '../lib/api';
import type { Settings } from '../lib/types';
import { settingsFixture } from '../test/settings';

vi.mock('../lib/api', () => ({
  getSettings: vi.fn(),
  getVersion: vi.fn(),
  getPresets: vi.fn().mockResolvedValue(null),
  getBluetooth: vi.fn().mockResolvedValue(null),
  saveSettings: vi.fn().mockResolvedValue(undefined),
}));

describe('useSpeakerInfo', () => {
  it('does not lend one room’s settings to the next while it loads', async () => {
    const a = { multiroom: { syncVolume: true } } as Settings;
    vi.mocked(api.getVersion).mockResolvedValue(null);
    vi.mocked(api.getSettings).mockImplementation((speaker) =>
      speaker === 'a' ? Promise.resolve(a) : new Promise(() => {})
    );
    const { result, rerender } = renderHook(({ speaker }) => useSpeakerInfo(speaker), {
      initialProps: { speaker: 'a' },
    });
    await waitFor(() => expect(result.current.settings).toBe(a));
    rerender({ speaker: 'b' });
    expect(result.current.settings).toBeNull();
    expect(result.current.version).toBe('');
  });

  // The home page and the settings page hold one copy of a speaker's settings: a save on
  // the one shows on the other without a reload.
  it('shows what the settings page saved', async () => {
    const before = settingsFixture({ name: 'Kitchen' });
    const after = settingsFixture({ name: 'Kitchen', bass: { actual: 3 } as Settings['bass'] });
    vi.mocked(api.getVersion).mockResolvedValue(null);
    vi.mocked(api.getSettings).mockResolvedValue(before);
    const { result } = renderHook(() => ({
      home: useSpeakerInfo('k'),
      page: useSpeakerSettings('k', 'Kitchen'),
    }));
    await waitFor(() => expect(result.current.page.form?.name).toBe('Kitchen'));
    expect(result.current.home.settings).toBe(before);
    vi.mocked(api.getSettings).mockResolvedValue(after);
    act(() => result.current.page.set('bass', 3));
    await act(() => result.current.page.save());
    await waitFor(() => expect(result.current.home.settings).toBe(after));
  });
});
