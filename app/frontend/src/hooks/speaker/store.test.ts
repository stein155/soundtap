// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import * as api from '../../lib/api';
import type { Settings } from '../../lib/types';
import {
  discardResource,
  ensureResource,
  refreshHeld,
  refreshResource,
  snapshot,
  subscribeResource,
} from './store';
import { must } from '../../test/must';
import { settingsFixture } from '../../test/settings';

vi.mock('../../lib/api', () => ({ getSettings: vi.fn() }));

const named = (name: string): Settings => settingsFixture({ name });

// A read that answers when the test says so.
function later() {
  const answers: ((s: Settings | null) => void)[] = [];
  vi.mocked(api.getSettings).mockImplementation(
    async () =>
      new Promise<Settings | null>((r) => {
        answers.push(r);
      })
  );
  return answers;
}

describe('speaker store', () => {
  beforeEach(() => {
    vi.mocked(api.getSettings).mockReset();
  });

  it('lets only the newest read land', async () => {
    const answers = later();
    const off = subscribeResource('settings', 'a', vi.fn());
    const first = refreshResource('settings', 'a');
    const second = refreshResource('settings', 'a');
    must(answers[1], 'the second read')(named('new'));
    await second;
    must(answers[0], 'the first read')(named('old'));
    expect(await first).toBe(true);
    expect(snapshot('settings', 'a')).toEqual(named('new'));
    off();
  });

  it('keeps the last answer when a read fails, and says the speaker is away until one', async () => {
    const off = subscribeResource('settings', 'a', vi.fn());
    vi.mocked(api.getSettings).mockResolvedValueOnce(null);
    expect(await refreshResource('settings', 'a')).toBe(false);
    expect(snapshot('settings', 'a')).toBeNull();
    vi.mocked(api.getSettings).mockResolvedValueOnce(named('a'));
    await refreshResource('settings', 'a');
    vi.mocked(api.getSettings).mockResolvedValueOnce(null);
    await refreshResource('settings', 'a');
    expect(snapshot('settings', 'a')).toEqual(named('a'));
    off();
  });

  it('keeps speakers apart', async () => {
    vi.mocked(api.getSettings).mockImplementation(async (s) => Promise.resolve(named(s ?? '')));
    const offA = subscribeResource('settings', 'a', vi.fn());
    const offB = subscribeResource('settings', 'b', vi.fn());
    await Promise.all([refreshResource('settings', 'a'), refreshResource('settings', 'b')]);
    expect(snapshot('settings', 'a')?.name).toBe('a');
    expect(snapshot('settings', 'b')?.name).toBe('b');
    offA();
    offB();
  });

  it('reads once for every page that asks at the same time', async () => {
    vi.mocked(api.getSettings).mockResolvedValue(named('a'));
    const off = subscribeResource('settings', 'a', vi.fn());
    await Promise.all([ensureResource('settings', 'a'), ensureResource('settings', 'a')]);
    await ensureResource('settings', 'a');
    expect(api.getSettings).toHaveBeenCalledTimes(1);
    off();
  });

  it('forgets a speaker no page reads any more', async () => {
    vi.mocked(api.getSettings).mockResolvedValue(named('a'));
    const off = subscribeResource('settings', 'a', vi.fn());
    await ensureResource('settings', 'a');
    off();
    expect(snapshot('settings', 'a')).toBeUndefined();
    const again = subscribeResource('settings', 'a', vi.fn());
    await ensureResource('settings', 'a');
    expect(api.getSettings).toHaveBeenCalledTimes(2);
    again();
  });

  it('tells every reader of a speaker when an answer lands', async () => {
    vi.mocked(api.getSettings).mockResolvedValue(named('a'));
    const home = vi.fn();
    const page = vi.fn();
    const offHome = subscribeResource('settings', 'a', home);
    const offPage = subscribeResource('settings', 'a', page);
    await refreshResource('settings', 'a');
    expect(home).toHaveBeenCalledTimes(1);
    expect(page).toHaveBeenCalledTimes(1);
    offHome();
    offPage();
  });

  it('reads again after a change only what a page holds', async () => {
    vi.mocked(api.getSettings).mockResolvedValue(named('a'));
    refreshHeld('settings', 'nobody');
    expect(api.getSettings).not.toHaveBeenCalled();
    const off = subscribeResource('settings', 'a', vi.fn());
    refreshHeld('settings', 'a');
    expect(api.getSettings).toHaveBeenCalledWith('a');
    off();
  });

  it('drops a read an action made stale', async () => {
    const answers = later();
    const off = subscribeResource('settings', 'a', vi.fn());
    const stale = refreshResource('settings', 'a');
    discardResource('settings', 'a');
    must(answers[0], 'the read')(named('before the action'));
    await stale;
    expect(snapshot('settings', 'a')).toBeUndefined();
    off();
  });
});
