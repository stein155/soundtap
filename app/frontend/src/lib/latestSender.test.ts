// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { createLatestSender } from './latestSender';

describe('createLatestSender', () => {
  it('sends the first value, skips the ones in between and always sends the last', async () => {
    let release: () => void = vi.fn();
    const send = vi.fn(
      async () =>
        new Promise<void>((r) => {
          release = r;
        })
    );
    const push = createLatestSender(send);
    push(10);
    push(20);
    push(30);
    expect(send).toHaveBeenCalledTimes(1);
    release();
    await vi.waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    expect(send).toHaveBeenLastCalledWith(30);
  });

  // A failed send is reported, not swallowed.
  it('reports a failed send', async () => {
    const onError = vi.fn();
    const push = createLatestSender(async () => {
      throw new Error('down');
    }, onError);
    push(70);
    await vi.waitFor(() => expect(onError).toHaveBeenCalledWith(70));
  });
});
