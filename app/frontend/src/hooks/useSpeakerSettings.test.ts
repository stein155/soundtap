// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useSpeakerSettings } from './useSpeakerSettings';
import * as api from '../lib/api';
import { settingsFixture } from '../test/settings';
import { must } from '../test/must';

const BASS = { actual: 0, target: 0, min: -9, max: 0, default: 0 };

vi.mock('../lib/api', async () => ({
  getSettings: vi.fn().mockResolvedValue(
    (await import('../test/settings')).settingsFixture({
      name: 'Kitchen',
      bass: { actual: 0, target: 0, min: -9, max: 0, default: 0 },
    })
  ),
  getPresets: vi.fn().mockResolvedValue([]),
  getBluetooth: vi.fn().mockResolvedValue(null),
  getVersion: vi.fn().mockResolvedValue(null),
  saveSettings: vi.fn().mockResolvedValue(undefined),
}));

describe('useSpeakerSettings save', () => {
  // A blank name is not sent, so it must not become the saved baseline.
  it('keeps the old name when the name was cleared', async () => {
    const { result } = renderHook(() => useSpeakerSettings('Kitchen', 'Kitchen'));
    await waitFor(() => expect(result.current.form?.name).toBe('Kitchen'));
    act(() => result.current.set('name', '  '));
    act(() => result.current.set('bass', 3));
    await act(async () => result.current.save());
    expect(api.saveSettings).toHaveBeenCalledWith({ bass: 3 }, 'Kitchen');
    expect(result.current.form?.name).toBe('Kitchen');
    expect(result.current.dirty).toBe(false);
  });

  // A speaker without a display has no town; the alarm carries the browser's zone.
  it('saves the alarm with the browser zone', async () => {
    const { result } = renderHook(() => useSpeakerSettings('Kitchen', 'Kitchen'));
    await waitFor(() => expect(result.current.form?.alarm.time).toBe('07:00'));
    act(() => result.current.set('alarm', { enabled: true, time: '06:45', slot: 1 }));
    await act(async () => result.current.save());
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    expect(api.saveSettings).toHaveBeenLastCalledWith(
      { alarm: { enabled: true, time: '06:45', slot: 1, timezone: zone } },
      'Kitchen'
    );
  });

  // Like stock, the ST20 clock shows without a town: a panel with no zone gets the browser's.
  it('gives a standby clock without a town the browser zone', async () => {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    const panel = settingsFixture({ name: 'Hall', bass: BASS, hasDisplay: true });
    vi.mocked(api.getSettings).mockResolvedValueOnce(panel);
    vi.mocked(api.saveSettings).mockClear();
    const { result } = renderHook(() => useSpeakerSettings('Hall', 'Hall'));
    await waitFor(() => expect(result.current.form?.display).toBeTruthy());
    expect(api.saveSettings).toHaveBeenCalledWith(
      { display: { ...panel.display, timezone: zone } },
      'Hall'
    );
    act(() => result.current.set('display', { ...panel.display, hour12: true }));
    await act(async () => result.current.save());
    expect(api.saveSettings).toHaveBeenLastCalledWith(
      { display: { ...panel.display, hour12: true, timezone: zone } },
      'Hall'
    );
  });

  it('rereads what the speaker reports and leaves the form alone', async () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useSpeakerSettings('Kitchen', 'Kitchen'));
    await act(async () => vi.advanceTimersByTimeAsync(0));
    act(() => result.current.set('bass', 3));
    vi.mocked(api.getSettings).mockResolvedValueOnce(
      settingsFixture({ name: 'Kitchen', bass: BASS, network: { type: 'wifi', ssid: 'Roamed' } })
    );
    await act(async () => vi.advanceTimersByTimeAsync(15000));
    vi.useRealTimers();
    expect(result.current.settings?.network?.ssid).toBe('Roamed');
    expect(result.current.form?.bass).toBe(3);
  });

  it('recovers when only the first read failed', async () => {
    vi.useFakeTimers();
    vi.mocked(api.getSettings).mockResolvedValueOnce(null);
    const { result } = renderHook(() => useSpeakerSettings('Hall', 'Hall'));
    await act(async () => vi.advanceTimersByTimeAsync(0));
    expect(result.current.form).toBeNull();
    await act(async () => vi.advanceTimersByTimeAsync(15000));
    vi.useRealTimers();
    expect(result.current.form?.name).toBe('Kitchen');
  });

  // Discovery renaming the room must not reload the page over unsaved edits.
  it('keeps unsaved edits when the room name changes', async () => {
    const { result, rerender } = renderHook(({ name }) => useSpeakerSettings('Kitchen', name), {
      initialProps: { name: 'Kitchen' },
    });
    await waitFor(() => expect(result.current.form?.name).toBe('Kitchen'));
    act(() => result.current.set('bass', -3));
    rerender({ name: 'Renamed Kitchen' });
    await act(async () => Promise.resolve());
    expect(result.current.form?.bass).toBe(-3);
    expect(result.current.dirty).toBe(true);
  });

  // An older save finishing last must not rewind the accepted baseline.
  it('sends overlapping saves one at a time, the later against the first', async () => {
    const { result } = renderHook(() => useSpeakerSettings('Kitchen', 'Kitchen'));
    await waitFor(() => expect(result.current.form?.name).toBe('Kitchen'));
    vi.mocked(api.saveSettings).mockClear();
    const done: (() => void)[] = [];
    vi.mocked(api.saveSettings).mockImplementation(
      async () => new Promise<undefined>((r) => done.push(() => r(undefined)))
    );
    act(() => result.current.set('bass', 2));
    let first: Promise<boolean> = Promise.resolve(false);
    await act(async () => void (first = result.current.save()));
    act(() => result.current.set('bass', 0));
    act(() => result.current.set('name', 'Dining'));
    let second: Promise<boolean> = Promise.resolve(false);
    await act(async () => void (second = result.current.save()));
    expect(api.saveSettings).toHaveBeenCalledTimes(1); // the second waits for the speaker
    await act(async () => {
      must(done[0], 'the first save')();
      await first;
    });
    expect(api.saveSettings).toHaveBeenLastCalledWith({ bass: 0, name: 'Dining' }, 'Kitchen');
    await act(async () => {
      must(done[1], 'the second save')();
      await second;
    });
    expect(result.current.dirty).toBe(false);
    vi.mocked(api.saveSettings).mockResolvedValue(undefined);
  });

  it('keeps the older accepted save as baseline when a newer one is refused', async () => {
    const { result } = renderHook(() => useSpeakerSettings('Kitchen', 'Kitchen'));
    await waitFor(() => expect(result.current.form?.name).toBe('Kitchen'));
    vi.mocked(api.saveSettings)
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('refused'));
    act(() => result.current.set('bass', 2));
    let first: Promise<boolean> = Promise.resolve(false);
    act(() => void (first = result.current.save()));
    act(() => result.current.set('bass', 4));
    let second: Promise<boolean> = Promise.resolve(false);
    act(() => void (second = result.current.save()));
    await act(async () => {
      await first;
      await second.catch(() => false);
    });
    act(() => result.current.set('bass', 2));
    expect(result.current.dirty).toBe(false);
    act(() => result.current.set('bass', 0));
    expect(result.current.dirty).toBe(true);
    vi.mocked(api.saveSettings).mockResolvedValue(undefined);
  });
});
