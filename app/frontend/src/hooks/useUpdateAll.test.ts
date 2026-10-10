// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useUpdateAll } from './useUpdateAll';
import * as api from '../lib/api';
import type { Room } from './useRooms';
import type { Releases, VersionInfo } from '../lib/types';

vi.mock('../lib/api', () => ({ getReleases: vi.fn(), getVersion: vi.fn(), startUpdate: vi.fn() }));

const room = (id: string, self: boolean) => ({ id, name: id, online: true, self }) as Room;
const version = (v: string) =>
  ({ version: v, updateMode: 'firmware', updatable: true }) as VersionInfo;

describe('useUpdateAll', () => {
  it('updates the other speakers first and the page’s own last', async () => {
    vi.useFakeTimers();
    const calls: string[] = [];
    let kitchen = 'app-v0.1.0';
    vi.mocked(api.getVersion).mockImplementation(async (s) =>
      version(s === 'kitchen' ? kitchen : 'app-v0.2.0')
    );
    vi.mocked(api.getReleases).mockImplementation(
      async (s) =>
        ({
          current: s === 'kitchen' ? 'app-v0.1.0' : 'app-v0.2.0',
          stable: { tag: 'app-v0.2.0' },
        }) as Releases
    );
    vi.mocked(api.startUpdate).mockImplementation(async (_tag, s) => {
      calls.push(s ?? '');
      kitchen = 'app-v0.2.0';
      return { ok: true, status: 202, body: { status: 'updating' } };
    });
    const { result } = renderHook(() => useUpdateAll([room('self', true), room('kitchen', false)]));
    expect(result.current.order.map((r) => r.id)).toEqual(['kitchen', 'self']);
    await act(async () => {
      const run = result.current.run();
      await vi.runAllTimersAsync();
      await run;
    });
    expect(calls).toEqual(['kitchen']);
    expect(result.current.states).toEqual({ kitchen: 'done', self: 'current' });
    vi.useRealTimers();
  });

  it('does not count a speaker back on another version as updated', async () => {
    vi.useFakeTimers();
    let now = 'app-v1.0.0';
    vi.mocked(api.getVersion).mockImplementation(async () => version(now));
    vi.mocked(api.getReleases).mockResolvedValue({
      current: 'app-v1.0.0',
      stable: { tag: 'app-v2.0.0' },
    } as Releases);
    vi.mocked(api.startUpdate).mockImplementation(async () => {
      now = 'app-v1.1.0';
      return { ok: true, status: 202, body: { status: 'updating' } };
    });
    const { result } = renderHook(() => useUpdateAll([room('kitchen', false)]));
    await act(async () => {
      const run = result.current.run();
      await vi.runAllTimersAsync();
      await run;
    });
    expect(result.current.states).toEqual({ kitchen: 'failed' });
    vi.useRealTimers();
  });

  it('reports a failed lookup as failed, not as up to date', async () => {
    const releases = { current: 'app-v1.0.0', stable: { tag: 'app-v1.0.0' } } as Releases;
    const cases: [VersionInfo | null, Releases | null, string][] = [
      [null, releases, 'failed'],
      [version('app-v1.0.0'), null, 'failed'],
      [version('app-v1.0.0'), { ...releases, unavailable: 'offline' }, 'failed'],
      [version('app-v1.0.0'), releases, 'current'],
    ];
    for (const [v, r, want] of cases) {
      vi.mocked(api.getVersion).mockResolvedValue(v);
      vi.mocked(api.getReleases).mockResolvedValue(r);
      const { result } = renderHook(() => useUpdateAll([room('kitchen', false)]));
      await act(async () => {
        await result.current.run();
      });
      expect(result.current.states).toEqual({ kitchen: want });
    }
  });
});
