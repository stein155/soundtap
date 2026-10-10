// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import type { ChangeEvent } from 'react';
import { useSignedPick } from './useSignedPick';

const change = (files: File[]) =>
  ({ target: { files, value: 'x' } }) as unknown as ChangeEvent<HTMLInputElement>;

describe('useSignedPick', () => {
  it('hands over the file with its signature', async () => {
    const onPick = vi.fn();
    const { result } = renderHook(() => useSignedPick(onPick, vi.fn()));
    const sig = new File([' abc \n'], 'a.bin.sig');
    sig.text = async () => ' abc \n';
    await act(() => result.current.onChange(change([new File(['a'], 'a.bin'), sig])));
    expect(onPick).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'a.bin' }),
      'abc',
      expect.any(Function)
    );
  });

  it('ignores an older pick that finishes late, and marks it stale', async () => {
    const onPick = vi.fn();
    const onError = vi.fn();
    const { result } = renderHook(() => useSignedPick(onPick, onError));
    let release: (s: string) => void = () => {};
    const oldSig = new File(['s'], 'old.bin.sig');
    oldSig.text = () => new Promise((r) => (release = r));
    let older: Promise<void> = Promise.resolve();
    act(() => {
      older = result.current.onChange(change([new File(['a'], 'old.bin'), oldSig]));
    });
    const stale = result.current.latest();
    await act(() => result.current.onChange(change([new File(['b'], 'new.bin')])));
    expect(stale()).toBe(false);
    await act(async () => {
      release('sig');
      await older;
    });
    expect(onPick).toHaveBeenCalledTimes(1);
    expect(onPick).toHaveBeenCalledWith(
      expect.objectContaining({ name: 'new.bin' }),
      undefined,
      expect.any(Function)
    );
    expect(onError).not.toHaveBeenCalled();
  });
});
