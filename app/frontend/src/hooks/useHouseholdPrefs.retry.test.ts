// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { useHouseholdPrefs } from './useHouseholdPrefs';
import * as api from '../lib/api';
import { settingsFixture } from '../test/settings';
import type { Room } from './useRooms';

vi.mock('../lib/api', () => ({ getSettings: vi.fn(), saveSettings: vi.fn() }));

const room = { id: 'a', name: 'Kitchen', self: true, online: true } as Room;

// One failed read must not hide the household controls for good.
describe('useHouseholdPrefs', () => {
  it('reads again after a failed read', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.mocked(api.getSettings)
      .mockResolvedValueOnce(null)
      .mockResolvedValue(settingsFixture({ multiroom: { keepGroups: false, syncVolume: true } }));
    const { result } = renderHook(() => useHouseholdPrefs([room]));
    await waitFor(() => expect(api.getSettings).toHaveBeenCalledTimes(1));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    await waitFor(() => expect(result.current.multiroom).not.toBeNull());
    vi.useRealTimers();
  });

  // A retry left by a closed view must not overwrite what a reopened one saved.
  it('drops the retry of an unmounted view', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const b = { id: 'b', name: 'Bedroom', online: true } as Room;
    vi.mocked(api.getSettings).mockResolvedValue(settingsFixture());
    vi.mocked(api.saveSettings)
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('busy'))
      .mockResolvedValue(undefined);
    const first = renderHook(() => useHouseholdPrefs([room, b]));
    await waitFor(() => expect(first.result.current.integrations).not.toBeNull());
    await act(() => first.result.current.saveIntegrations({ spotify: false, airplay: true }));
    first.unmount();
    const second = renderHook(() => useHouseholdPrefs([room, b]));
    await waitFor(() => expect(second.result.current.integrations).not.toBeNull());
    await act(() => second.result.current.saveIntegrations({ spotify: true, airplay: true }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(api.saveSettings).toHaveBeenCalledTimes(4);
    vi.useRealTimers();
  });
});
