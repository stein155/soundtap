// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import type { ChangeEvent } from 'react';
import { useSignedPick } from './useSignedPick';

// A change event from a real file input holding `files` (jsdom builds no FileList, so the
// input lists them as an array, which is all the hook reads of it).
const change = (files: File[]): ChangeEvent<HTMLInputElement> => {
  const input = document.createElement('input');
  input.type = 'file';
  Object.defineProperty(input, 'files', { value: files });
  return {
    nativeEvent: new Event('change'),
    target: input,
    currentTarget: input,
    bubbles: true,
    cancelable: false,
    defaultPrevented: false,
    eventPhase: 0,
    isTrusted: true,
    timeStamp: 0,
    type: 'change',
    preventDefault: vi.fn(),
    isDefaultPrevented: () => false,
    stopPropagation: vi.fn(),
    isPropagationStopped: () => false,
    persist: vi.fn(),
  };
};

describe('useSignedPick', () => {
  it('hands over the file with its signature', async () => {
    const onPick = vi.fn();
    const { result } = renderHook(() => useSignedPick(onPick, vi.fn()));
    const sig = new File([' abc \n'], 'a.bin.sig');
    sig.text = async () => ' abc \n';
    await act(async () => result.current.onChange(change([new File(['a'], 'a.bin'), sig])));
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
    let release: (s: string) => void = vi.fn();
    const oldSig = new File(['s'], 'old.bin.sig');
    oldSig.text = async () => new Promise((r) => (release = r));
    let older: Promise<void> = Promise.resolve();
    act(() => {
      older = result.current.onChange(change([new File(['a'], 'old.bin'), oldSig]));
    });
    const stale = result.current.latest();
    await act(async () => result.current.onChange(change([new File(['b'], 'new.bin')])));
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
