// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect } from 'vitest';
import { act, fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../test/render';
import { ConfirmAction } from '.';

describe('ConfirmAction', () => {
  it('keeps Cancel disabled while the confirmed operation runs', async () => {
    let finish: () => void = () => {};
    const { getByText, getAllByRole } = renderWithI18n(
      <ConfirmAction
        label="Start"
        warning="Sure?"
        confirmLabel="Commit"
        onConfirm={() => new Promise<void>((r) => (finish = r))}
      />
    );
    fireEvent.click(getByText('Start'));
    fireEvent.click(getByText('Commit'));
    const [cancel, commit] = getAllByRole('button');
    expect(commit).toHaveProperty('disabled', true);
    expect(cancel).toHaveProperty('disabled', true);
    await act(async () => finish());
    expect(getByText('Start')).toBeTruthy();
  });
});
