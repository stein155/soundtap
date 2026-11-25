// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect } from 'vitest';
import { splitSignature } from './signature';

describe('splitSignature', () => {
  it('takes the .sig next to the image, whatever the pick order', async () => {
    const stu = new File(['stu'], 'SoundTap-Core-0.1.0.stu');
    const sig = new File(['c2ln\n'], 'SoundTap-Core-0.1.0.stu.sig');
    expect(await splitSignature([sig, stu])).toEqual({ file: stu, signature: 'c2ln' });
  });

  it('leaves the signature out when none was picked', async () => {
    const bin = new File(['bin'], 'SoundTap-app-0.1.0-armv7.bin');
    expect(await splitSignature([bin])).toEqual({ file: bin, signature: undefined });
  });
});
