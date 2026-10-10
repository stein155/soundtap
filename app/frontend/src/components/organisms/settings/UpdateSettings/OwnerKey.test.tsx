// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, waitFor } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import * as api from '../../../../lib/api';
import { needsPress } from '../../../../lib/base';
import { OwnerKey } from './OwnerKey';

vi.mock('../../../../lib/api', () => ({
  getOwnerKey: vi.fn(),
  setOwnerKey: vi.fn(),
  removeOwnerKey: vi.fn(),
}));

const KEY = '88omIN8FRazo9PIEnIHv3JUqYTQBzN+wnqpexHvX+MY=';
const none = { available: true, active: false };
const active = { available: true, active: true, key: KEY, fingerprint: '0123456789abcdef' };

describe('OwnerKey', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows nothing on a speaker without an environment, or one that cannot say', async () => {
    for (const answer of [{ available: false, active: false }, null]) {
      vi.mocked(api.getOwnerKey).mockResolvedValue(answer);
      const { container, unmount } = renderWithI18n(<OwnerKey speaker="Kitchen" />);
      await waitFor(() => expect(api.getOwnerKey).toHaveBeenCalledWith('Kitchen'));
      expect(container.textContent).toBe('');
      unmount();
    }
  });

  it('sets a key after asking, and then shows its fingerprint', async () => {
    vi.mocked(api.getOwnerKey).mockResolvedValue(none);
    vi.mocked(api.setOwnerKey).mockResolvedValue(active);
    const { findByText, getByLabelText, getByRole, queryByRole } = renderWithI18n(
      <OwnerKey speaker="Kitchen" />
    );
    expect(await findByText('No key set. Only SoundTap releases install.')).toBeTruthy();
    const set = getByRole('button', { name: 'Trust this key' }) as HTMLButtonElement;
    expect(set.disabled).toBe(true);
    // Not a key: said, and nothing can be sent.
    fireEvent.change(getByLabelText('Public key (base64)'), {
      target: { value: 'ssh-ed25519 AAAA' },
    });
    expect(getByRole('alert').textContent).toContain('Not an ed25519 public key');
    expect(set.disabled).toBe(true);
    // The unused bits of the last character must be zero, as the speaker reads it.
    fireEvent.change(getByLabelText('Public key (base64)'), {
      target: { value: KEY.slice(0, 42) + 'N=' },
    });
    expect(set.disabled).toBe(true);
    fireEvent.change(getByLabelText('Public key (base64)'), { target: { value: ` ${KEY}\n` } });
    expect(queryByRole('alert')).toBeNull();
    fireEvent.click(set);
    expect(api.setOwnerKey).not.toHaveBeenCalled();
    fireEvent.click(getByRole('button', { name: 'Trust this key' }));
    await waitFor(() => expect(api.setOwnerKey).toHaveBeenCalledWith(KEY, 'Kitchen'));
    expect(await findByText('Active · fingerprint 0123456789abcdef')).toBeTruthy();
    expect(getByRole('button', { name: 'Remove the key' })).toBeTruthy();
  });

  it('removes the key after asking', async () => {
    vi.mocked(api.getOwnerKey).mockResolvedValue(active);
    vi.mocked(api.removeOwnerKey).mockResolvedValue(none);
    const { findByRole, getByRole, findByText } = renderWithI18n(<OwnerKey speaker="" />);
    fireEvent.click(await findByRole('button', { name: 'Remove the key' }));
    expect(api.removeOwnerKey).not.toHaveBeenCalled();
    fireEvent.click(getByRole('button', { name: 'Remove the key' }));
    await waitFor(() => expect(api.removeOwnerKey).toHaveBeenCalledWith(''));
    expect(await findByText('No key set. Only SoundTap releases install.')).toBeTruthy();
  });

  it('says why when the speaker was not confirmed', async () => {
    vi.mocked(api.getOwnerKey).mockResolvedValue(active);
    vi.mocked(api.removeOwnerKey).mockRejectedValue(
      Object.assign(new Error('hold a button on the speaker to confirm'), {
        status: 403,
        code: 'not_confirmed',
      })
    );
    const { findByRole, getByRole } = renderWithI18n(<OwnerKey speaker="" />);
    fireEvent.click(await findByRole('button', { name: 'Remove the key' }));
    fireEvent.click(getByRole('button', { name: 'Remove the key' }));
    expect((await findByRole('alert')).textContent).toBe(
      'The key could not be removed. (nobody held a button on the speaker in time)'
    );
  });

  it('waits for the press on the speaker, as soundtapd does', () => {
    expect(needsPress('PUT', '/api/owner-key')).toBe(true);
    expect(needsPress('DELETE', '/api/devices/abc/api/owner-key')).toBe(true);
    expect(needsPress('GET', '/api/owner-key')).toBe(false);
  });
});
