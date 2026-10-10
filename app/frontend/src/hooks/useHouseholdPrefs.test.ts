// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useHouseholdPrefs } from './useHouseholdPrefs';
import * as api from '../lib/api';
import type { Room } from './useRooms';

vi.mock('../lib/api', async () => ({
  getSettings: vi.fn().mockResolvedValue((await import('../test/settings')).settingsFixture()),
  saveSettings: vi.fn(),
}));

const rooms = [{ id: 'a', self: true, online: true }] as Room[];

describe('useHouseholdPrefs saveMultiroom', () => {
  // An older save that fails late must not undo a newer one.
  it('lets only the newest save roll back', async () => {
    let failOld: (e: Error) => void = () => {};
    vi.mocked(api.saveSettings)
      .mockImplementationOnce(() => new Promise((_, reject) => (failOld = reject)))
      .mockResolvedValueOnce(undefined);
    const { result } = renderHook(() => useHouseholdPrefs(rooms));
    await waitFor(() => expect(result.current.multiroom).not.toBeNull());
    let older: Promise<void> = Promise.resolve();
    let newer: Promise<void> = Promise.resolve();
    act(() => {
      older = result.current.saveMultiroom({ keepGroups: true, syncVolume: false });
    });
    await waitFor(() => expect(api.saveSettings).toHaveBeenCalledTimes(1));
    act(() => {
      newer = result.current.saveMultiroom({ keepGroups: true, syncVolume: true });
    });
    await act(async () => {
      failOld(new Error('offline'));
      await older;
      await newer;
    });
    expect(result.current.multiroom).toEqual({ keepGroups: true, syncVolume: true });
  });

  // When every overlapping save fails, the confirmed value comes back, not the
  // first save's unsaved one.
  it('rolls back to the stored value when both overlapping saves fail', async () => {
    const stored = { keepGroups: false, syncVolume: false };
    let rejectFirst: (e: Error) => void = () => {};
    let rejectSecond: (e: Error) => void = () => {};
    vi.mocked(api.saveSettings).mockReset();
    vi.mocked(api.saveSettings)
      .mockImplementationOnce(() => new Promise((_, reject) => (rejectFirst = reject)))
      .mockImplementationOnce(() => new Promise((_, reject) => (rejectSecond = reject)));
    const { result } = renderHook(() => useHouseholdPrefs(rooms));
    await waitFor(() => expect(result.current.multiroom).toEqual(stored));
    let first: Promise<void> = Promise.resolve();
    let second: Promise<void> = Promise.resolve();
    act(() => {
      first = result.current.saveMultiroom({ keepGroups: true, syncVolume: false });
    });
    await waitFor(() => expect(api.saveSettings).toHaveBeenCalledTimes(1));
    act(() => {
      second = result.current.saveMultiroom({ keepGroups: true, syncVolume: true });
    });
    await act(async () => {
      rejectFirst(new Error('offline'));
      await first;
    });
    await act(async () => {
      rejectSecond(new Error('offline'));
      await second;
    });
    expect(result.current.multiroom).toEqual(stored);
  });

  // A quick A, B, A must end as A on every speaker: saves go out in order and a
  // superseded one still waiting is not sent at all.
  it('sends quick saves in order and drops a superseded one', async () => {
    const two = [
      { id: 'a', self: true, online: true },
      { id: 'b', self: false, online: true },
    ] as Room[];
    const sent: boolean[] = [];
    vi.mocked(api.saveSettings).mockReset();
    vi.mocked(api.saveSettings).mockImplementation((body) => {
      sent.push((body.multiroom as { keepGroups: boolean }).keepGroups);
      return Promise.resolve();
    });
    const { result } = renderHook(() => useHouseholdPrefs(two));
    await waitFor(() => expect(result.current.multiroom).not.toBeNull());
    const a = { keepGroups: false, syncVolume: false };
    const b = { keepGroups: true, syncVolume: false };
    await act(async () => {
      void result.current.saveMultiroom(b);
      void result.current.saveMultiroom(a);
      await result.current.saveMultiroom(b);
    });
    // Only the last B goes out, once to each speaker.
    expect(sent).toEqual([true, true]);
  });

  // A speaker that missed a save gets it again, so it does not keep the old value.
  it('saves again to a speaker that missed it', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const two = [
      { id: 'a', self: true, online: true },
      { id: 'b', self: false, online: true },
    ] as Room[];
    vi.mocked(api.saveSettings).mockReset();
    vi.mocked(api.saveSettings)
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue(undefined);
    const { result } = renderHook(() => useHouseholdPrefs(two));
    await waitFor(() => expect(result.current.multiroom).not.toBeNull());
    await act(() => result.current.saveMultiroom({ keepGroups: true, syncVolume: false }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(api.saveSettings).toHaveBeenCalledTimes(3);
    expect(vi.mocked(api.saveSettings).mock.calls[2][1]).toBe(
      vi.mocked(api.saveSettings).mock.calls[1][1]
    );
    vi.useRealTimers();
  });
});
