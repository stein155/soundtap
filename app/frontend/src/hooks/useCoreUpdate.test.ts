// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { afterEach, describe, it, expect, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { UPDATE_POLL_MS } from './useSelfUpdate';
import { useCoreUpdate } from './useCoreUpdate';
import * as api from '../lib/api';
import { version } from './testFixtures';
import type { VersionInfo } from '../lib/types';

vi.mock('../lib/api', () => ({
  getVersion: vi.fn(),
  startCoreUpdate: vi.fn(),
}));
vi.mock('../lib/i18n', () => ({ useI18n: () => ({ t: (k: string) => k }) }));

const core = (os: string): VersionInfo => version({ version: '0.1.0', os });
const report = { size: 1, modules: [], kernel: 'mainline', models: ['st10'], installable: true };

afterEach(() => {
  vi.useRealTimers();
  vi.resetAllMocks();
});

// Once started (the card asks first): the speaker installs the release it fetched, restarts into it on its own, and the
// page follows it away and back (ADR 0048).
describe('useCoreUpdate', () => {
  it('follows the install, the restart and the new Core', async () => {
    vi.useFakeTimers();
    vi.mocked(api.startCoreUpdate).mockResolvedValueOnce({
      ...report,
      installed: true,
      restarting: true,
    });
    vi.mocked(api.getVersion)
      .mockResolvedValueOnce(core('0.1.0'))
      .mockResolvedValueOnce(null)
      .mockResolvedValueOnce(core('0.2.0'));
    const { result } = renderHook(() => useCoreUpdate('Kitchen', '0.2.0'));
    expect(result.current.stage).toBeNull();
    await act(async () => result.current.install());
    expect(api.startCoreUpdate).toHaveBeenCalledWith('Kitchen');
    expect(result.current.phase).toBe('busy');
    expect(result.current.stage).toBe('restart');
    await act(async () => vi.advanceTimersByTimeAsync(6000));
    expect(result.current.phase).toBe('busy');
    await act(async () => vi.advanceTimersByTimeAsync(UPDATE_POLL_MS));
    expect(result.current.stage).toBe('restart');
    await act(async () => vi.advanceTimersByTimeAsync(UPDATE_POLL_MS));
    expect(result.current.stage).toBe('check');
    expect(result.current.phase).toBe('updated');
  });

  it('shows how far the speaker says it is while it downloads and writes the release', async () => {
    vi.useFakeTimers();
    let answer: (r: Awaited<ReturnType<typeof api.startCoreUpdate>>) => void = vi.fn();
    vi.mocked(api.startCoreUpdate).mockReturnValueOnce(new Promise((r) => (answer = r)));
    vi.mocked(api.getVersion)
      .mockResolvedValueOnce(version({ version: '0.1.0', os: '0.1.0', updateProgress: 62 }))
      .mockResolvedValueOnce(core('0.1.0'));
    const { result } = renderHook(() => useCoreUpdate('Kitchen', '0.2.0'));
    await act(async () => {
      void result.current.install();
    });
    expect(result.current.phase).toBe('busy');
    expect(result.current.progress).toBeNull();
    await act(async () => vi.advanceTimersByTimeAsync(UPDATE_POLL_MS / 2));
    expect(result.current.progress).toBe(62);
    // Without a number from the speaker none is shown.
    await act(async () => vi.advanceTimersByTimeAsync(UPDATE_POLL_MS));
    expect(result.current.progress).toBeNull();
    await act(async () => answer({ ...report, installed: true, reason: 'Reboot to run it.' }));
    expect(result.current.phase).toBe('done');
    expect(result.current.progress).toBeNull();
  });

  it('says so when the speaker comes back on the Core it had', async () => {
    vi.useFakeTimers();
    vi.mocked(api.startCoreUpdate).mockResolvedValueOnce({
      ...report,
      installed: true,
      restarting: true,
    });
    vi.mocked(api.getVersion).mockResolvedValueOnce(null).mockResolvedValueOnce(core('0.1.0'));
    const { result } = renderHook(() => useCoreUpdate('Kitchen', '0.2.0'));
    await act(async () => result.current.install());
    await act(async () => vi.advanceTimersByTimeAsync(6000 + UPDATE_POLL_MS));
    expect(result.current.phase).toBe('error');
    expect(result.current.text).toBe('coreInstallFailed');
  });

  it('follows nothing when the speaker did not restart', async () => {
    vi.mocked(api.startCoreUpdate).mockResolvedValueOnce({
      ...report,
      installed: true,
      reason: 'Reboot to run it.',
    });
    const { result } = renderHook(() => useCoreUpdate('Kitchen', '0.2.0'));
    await act(async () => result.current.install());
    expect(result.current.phase).toBe('done');
    expect(result.current.stage).toBeNull();
    expect(result.current.text).toBe('Reboot to run it.');
    expect(api.getVersion).not.toHaveBeenCalled();
  });

  it('says so when the install fails, and when nothing is newer', async () => {
    vi.mocked(api.startCoreUpdate)
      .mockRejectedValueOnce(new Error('the Core release could not be downloaded'))
      .mockResolvedValueOnce({ status: 'current' });
    const { result } = renderHook(() => useCoreUpdate('Kitchen', '0.2.0'));
    await act(async () => result.current.install());
    expect(result.current.phase).toBe('error');
    expect(result.current.stage).toBeNull();
    await act(async () => result.current.install());
    expect(result.current.phase).toBe('done');
    expect(result.current.text).toBe('upLatest');
  });
});
