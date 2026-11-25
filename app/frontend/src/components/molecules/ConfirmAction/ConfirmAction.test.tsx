// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { act, fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../test/render';
import { ConfirmAction } from '.';

describe('ConfirmAction', () => {
  it('asks in a dialog and keeps the button busy while the action runs', async () => {
    let finish: () => void = vi.fn();
    const onConfirm = vi.fn(async () => new Promise<void>((r) => (finish = r)));
    const { getByRole, findByRole } = renderWithI18n(
      <ConfirmAction label="Start" warning="Sure?" confirmLabel="Commit" onConfirm={onConfirm} />
    );
    fireEvent.click(getByRole('button', { name: 'Start' }));
    const dialog = await findByRole('dialog', { name: 'Start' });
    expect(dialog.textContent).toContain('Sure?');
    expect(onConfirm).not.toHaveBeenCalled();
    fireEvent.click(getByRole('button', { name: 'Commit' }));
    expect(onConfirm).toHaveBeenCalledOnce();
    expect(getByRole('button', { name: 'Start' })).toHaveProperty('disabled', true);
    await act(async () => finish());
    expect(getByRole('button', { name: 'Start' })).toHaveProperty('disabled', false);
  });

  it('does nothing on Cancel', async () => {
    const onConfirm = vi.fn();
    const { getByRole, findByRole } = renderWithI18n(
      <ConfirmAction label="Start" warning="Sure?" confirmLabel="Commit" onConfirm={onConfirm} />
    );
    fireEvent.click(getByRole('button', { name: 'Start' }));
    await findByRole('dialog');
    fireEvent.click(getByRole('button', { name: 'Cancel' }));
    expect(onConfirm).not.toHaveBeenCalled();
  });
});
