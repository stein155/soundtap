// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRef, type ChangeEvent } from 'react';
import { splitSignature } from '../lib/signature';

// The file picker both upload flows share: a hidden <input type=file multiple> whose pick is
// a file with, optionally, its .sig (ADR 0048). Reading a .sig is async, so an older pick can
// finish after a newer one and must not replace it: `onPick` and `onError` only hear of
// the newest pick. `onPick` also gets a check that stays true while that pick is the newest;
// `latest()` hands out the same check later (for the install after a pick).
export function useSignedPick(
  onPick: (file: File | null, signature: string | undefined, current: () => boolean) => void,
  onError: (err: unknown) => void
) {
  const inputRef = useRef<HTMLInputElement>(null);
  const picks = useRef(0);

  const onChange = async (e: ChangeEvent<HTMLInputElement>) => {
    const pick = ++picks.current;
    const reading = splitSignature(e.target.files);
    e.target.value = '';
    try {
      const picked = await reading;
      if (pick === picks.current)
        onPick(picked.file, picked.signature, () => pick === picks.current);
    } catch (err) {
      if (pick === picks.current) onError(err);
    }
  };

  const latest = () => {
    const pick = picks.current;
    return () => pick === picks.current;
  };

  return { inputRef, onChange, latest };
}
