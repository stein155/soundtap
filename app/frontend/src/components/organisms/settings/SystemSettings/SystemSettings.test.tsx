// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { afterEach, describe, it, expect, vi } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import * as api from '../../../../lib/api';
import { SystemSettings } from '.';

vi.mock('../../../../lib/api', () => ({
  getSupportBundle: vi.fn(),
  factoryReset: vi.fn(),
  restoreBackup: vi.fn(),
}));

describe('SystemSettings support bundle', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('saves the archive the speaker sends under its own name', async () => {
    const blob = new Blob(['zip'], { type: 'application/zip' });
    vi.mocked(api.getSupportBundle).mockResolvedValue({
      blob,
      name: 'soundtap-support-20261120-101500.zip',
    });
    const createObjectURL = vi.fn().mockReturnValue('blob:support');
    // jsdom has no object URLs; a subclass keeps the rest of URL as it is.
    vi.stubGlobal(
      'URL',
      class extends URL {
        static createObjectURL = createObjectURL;
        static revokeObjectURL = vi.fn();
      }
    );
    const saved: { href: string; download: string }[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
      this: HTMLAnchorElement
    ) {
      saved.push({ href: this.href, download: this.download });
    });

    const { getByRole } = renderWithI18n(<SystemSettings speaker="kitchen" />);
    fireEvent.click(getByRole('button', { name: 'Support bundle' }));

    await waitFor(() => expect(saved).toHaveLength(1));
    expect(api.getSupportBundle).toHaveBeenCalledWith('kitchen');
    expect(createObjectURL).toHaveBeenCalledWith(blob);
    expect(saved[0]).toEqual({
      href: 'blob:support',
      download: 'soundtap-support-20261120-101500.zip',
    });
  });

  it('says why when nobody pressed a button on the speaker', async () => {
    vi.mocked(api.getSupportBundle).mockRejectedValue(
      Object.assign(new Error('press a button'), { status: 403, code: 'not_confirmed' })
    );
    const { getByRole, findByRole } = renderWithI18n(<SystemSettings speaker="" />);
    const button = getByRole('button', { name: 'Support bundle' });
    fireEvent.click(button);

    const alert = await findByRole('alert');
    expect(alert.textContent).toContain('The support bundle could not be downloaded.');
    expect(button.hasAttribute('disabled')).toBe(false);
  });
});
