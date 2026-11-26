// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useHouseholdMqtt } from './useHouseholdMqtt';
import type { Room } from './useRooms';
import * as api from '../lib/api';
import { must } from '../test/must';
import { room as roomFixture } from './testFixtures';
import { settingsFixture } from '../test/settings';

vi.mock('../lib/api', () => ({
  getSettings: vi.fn().mockResolvedValue({ mqtt: { host: 'broker' } }),
  getMqttStatus: vi.fn().mockResolvedValue(null),
  saveSettings: vi.fn(),
}));

const room = (id: string, name: string, self: boolean): Room => roomFixture({ id, name, self });

describe('useHouseholdMqtt', () => {
  it('keeps the form, password included, when one speaker missed the save', async () => {
    const rooms = [room('a', 'Kitchen', true), room('b', 'Bedroom', false)];
    vi.mocked(api.saveSettings).mockImplementation(async (_p, speaker) => {
      if (speaker === 'b') throw new Error('offline');
      return undefined;
    });
    const { result } = renderHook(() => useHouseholdMqtt(rooms));
    await waitFor(() => expect(result.current.form).not.toBeNull());
    act(() =>
      result.current.setForm({ ...must(result.current.form, 'the form'), password: 'secret' })
    );
    await act(async () => result.current.save());
    expect(result.current.failedOn).toEqual(['Bedroom']);
    expect(result.current.form?.password).toBe('secret');
    expect(result.current.dirty).toBe(true);
  });

  // Edits typed while a save is out are not thrown away when it succeeds.
  it('keeps newer edits made while a save was pending', async () => {
    const rooms = [room('a', 'Kitchen', true)];
    let finish: () => void = vi.fn();
    vi.mocked(api.saveSettings).mockImplementation(
      async () =>
        new Promise((r) => {
          finish = () => r(undefined);
        })
    );
    const { result } = renderHook(() => useHouseholdMqtt(rooms));
    await waitFor(() => expect(result.current.form).not.toBeNull());
    act(() =>
      result.current.setForm({ ...must(result.current.form, 'the form'), host: 'saved-broker' })
    );
    let pending: Promise<void> = Promise.resolve();
    act(() => {
      pending = result.current.save();
    });
    act(() =>
      result.current.setForm({
        ...must(result.current.form, 'the form'),
        host: 'newer-broker',
        password: 'pw',
      })
    );
    await act(async () => {
      finish();
      await pending;
    });
    expect(result.current.form?.host).toBe('newer-broker');
    expect(result.current.form?.password).toBe('pw');
    expect(result.current.dirty).toBe(true);
  });

  // An unset port stays unset, so the speaker picks 1883 or, with TLS, 8883.
  it('saves an unset port as 0, also with TLS on', async () => {
    const rooms = [room('a', 'Kitchen', true)];
    vi.mocked(api.saveSettings).mockResolvedValue(undefined);
    const { result } = renderHook(() => useHouseholdMqtt(rooms));
    await waitFor(() => expect(result.current.form).not.toBeNull());
    expect(result.current.form?.port).toBe('');
    act(() => result.current.setForm({ ...must(result.current.form, 'the form'), tls: true }));
    await act(async () => result.current.save());
    expect(vi.mocked(api.saveSettings)).toHaveBeenLastCalledWith(
      { mqtt: expect.objectContaining({ port: 0, tls: true }) },
      ''
    );
  });

  it('reads the settings again after a failed first read', async () => {
    const rooms = [room('a', 'Kitchen', true)];
    vi.mocked(api.getSettings).mockResolvedValueOnce(null);
    const { result } = renderHook(() => useHouseholdMqtt(rooms));
    await waitFor(() => expect(result.current.loadFailed).toBe(true));
    await waitFor(() => expect(result.current.form?.host).toBe('broker'), { timeout: 8000 });
    expect(result.current.loadFailed).toBe(false);
  }, 10000);

  it('drops the stored password after a save to another broker without one', async () => {
    const rooms = [room('a', 'Kitchen', true)];
    vi.mocked(api.getSettings).mockResolvedValueOnce(
      settingsFixture({ mqtt: { host: 'broker', hasPassword: true } })
    );
    vi.mocked(api.saveSettings).mockResolvedValue(undefined);
    const { result } = renderHook(() => useHouseholdMqtt(rooms));
    await waitFor(() => expect(result.current.hasPassword).toBe(true));
    act(() => result.current.setForm({ ...must(result.current.form, 'the form'), username: 'me' }));
    await act(async () => result.current.save());
    expect(result.current.hasPassword).toBe(true);
    act(() => result.current.setForm({ ...must(result.current.form, 'the form'), host: 'other' }));
    await act(async () => result.current.save());
    expect(result.current.hasPassword).toBe(false);
  });

  it('stops polling the broker links while the page is hidden', async () => {
    vi.useFakeTimers();
    let state: DocumentVisibilityState = 'visible';
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state });
    try {
      vi.mocked(api.getMqttStatus).mockClear();
      renderHook(() => useHouseholdMqtt([room('a', 'Kitchen', true)]));
      expect(api.getMqttStatus).toHaveBeenCalledOnce();
      act(() => {
        state = 'hidden';
        document.dispatchEvent(new Event('visibilitychange'));
      });
      await act(async () => vi.advanceTimersByTimeAsync(20000));
      expect(api.getMqttStatus).toHaveBeenCalledOnce();
      act(() => {
        state = 'visible';
        document.dispatchEvent(new Event('visibilitychange'));
      });
      expect(api.getMqttStatus).toHaveBeenCalledTimes(2);
    } finally {
      state = 'visible';
      vi.useRealTimers();
    }
  });
});
