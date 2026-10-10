// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { usePlayback } from './usePlayback';
import type { SelectedRoom } from './useSelectedRoom';
import * as api from '../lib/api';

vi.mock('../lib/api', () => ({ selectUserSource: vi.fn(), stopPlayback: vi.fn() }));
vi.mock('../lib/i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }));

// Wi-Fi picked while an AUX pick is still on its way: the Stop goes out after it, so the
// last pick wins.
describe('usePlayback source picks', () => {
  it('queues the Wi-Fi stop behind a pending input pick', async () => {
    const order: string[] = [];
    let finishAux = () => {};
    vi.mocked(api.selectUserSource).mockImplementation(
      () =>
        new Promise((resolve) => {
          finishAux = () => {
            order.push('aux');
            resolve(undefined);
          };
        })
    );
    vi.mocked(api.stopPlayback).mockImplementation(async () => {
      order.push('stop');
    });
    const room = {
      speaker: 'kitchen',
      lead: { id: 'kitchen', name: 'Kitchen', now: { source: 'TUNEIN' } },
      live: {
        presets: [],
        player: { status: 'idle' },
        nudge: vi.fn(),
        stopOptimistic: vi.fn(),
        refreshNowPlaying: vi.fn(),
        releaseStopHold: vi.fn(),
      },
    } as unknown as SelectedRoom;
    const { result } = renderHook(() => usePlayback(room, () => vi.fn()));
    let aux!: Promise<void>, wifi!: Promise<void>;
    act(() => {
      aux = result.current.setSource('aux');
      wifi = result.current.setSource('wifi');
    });
    await act(async () => {
      await Promise.resolve();
      finishAux();
      await aux;
      await wifi;
    });
    expect(order).toEqual(['aux', 'stop']);
  });
});
