// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { UPDATE_MAX_POLLS, UPDATE_POLL_MS, useSelfUpdate } from './useSelfUpdate';
import * as api from '../lib/api';

vi.mock('../lib/api', () => ({
  getReleases: vi.fn().mockResolvedValue({
    stable: { tag: 'v1' },
  }),
  getVersion: vi.fn(),
  startUpdate: vi.fn().mockResolvedValue({ status: 200, body: { status: 'current' } }),
}));
vi.mock('../lib/i18n', () => ({ useI18n: () => ({ t: (k: string) => k }) }));

describe('useSelfUpdate', () => {
  it('installs the latest stable release', async () => {
    const { result } = renderHook(() => useSelfUpdate('Kitchen', null));
    await waitFor(() => expect(result.current.target?.tag).toBe('v1'));
    await act(() => result.current.install());
    expect(api.startUpdate).toHaveBeenCalledWith(undefined, 'Kitchen');
  });

  it('says so when the speaker cannot reach the release list', async () => {
    vi.mocked(api.getReleases).mockResolvedValueOnce({
      current: '1',
      updatable: true,
      stable: null,
      unavailable: 'release list unreachable',
    });
    const { result } = renderHook(() => useSelfUpdate('Kitchen', null));
    await waitFor(() => expect(result.current.text).toBe('updateUnavailable'));
  });

  it('says the clock is not set yet instead of unreachable', async () => {
    vi.mocked(api.getReleases).mockResolvedValueOnce({
      current: '1',
      updatable: true,
      stable: null,
      unavailable: 'the speaker’s clock is not set yet',
      unavailableCode: 'clock_not_set',
    });
    const { result } = renderHook(() => useSelfUpdate('Kitchen', null));
    await waitFor(() => expect(result.current.text).toBe('updateUnavailable (errClockNotSet)'));
  });

  it('names the unset clock when the update is refused for it', async () => {
    vi.mocked(api.startUpdate).mockResolvedValueOnce({
      ok: false,
      status: 503,
      body: { error: 'the speaker’s clock is not set yet', code: 'clock_not_set' },
    });
    const { result } = renderHook(() => useSelfUpdate('Kitchen', null));
    await waitFor(() => expect(result.current.target?.tag).toBe('v1'));
    await act(() => result.current.install());
    expect(result.current.phase).toBe('error');
    expect(result.current.text).toBe('updateError (errClockNotSet)');
  });

  it('waits longer than the speaker may take to download', () => {
    // update.go: downloadTimeout of five minutes, once for the app and once for its signature.
    const speakerBudgetMs = 2 * 5 * 60_000;
    expect(UPDATE_MAX_POLLS * UPDATE_POLL_MS).toBeGreaterThan(speakerBudgetMs);
    expect(Number.isInteger(UPDATE_MAX_POLLS)).toBe(true);
  });
});
