// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it } from 'vitest';
import { renderWithI18n } from '../../../../test/render';
import { Updating } from './Updating';
import { must } from '../../../../test/must';

describe('Updating', () => {
  it('marks the steps before the one under way as done', () => {
    const { getByRole, getAllByRole } = renderWithI18n(
      <Updating name="Kitchen" model="SoundTouch 10" stage="restart" done={false} />
    );
    expect(getByRole('alertdialog', { name: 'The speaker is updating itself' })).toBeTruthy();
    const steps = getAllByRole('listitem');
    expect(steps.map((s) => s.getAttribute('aria-current'))).toEqual([null, null, 'step', null]);
    expect(must(steps[2], 'the third step').textContent).toBe('Restarting');
  });

  it('offers to reload once the new version answers', () => {
    const { getByRole, queryByRole, rerender } = renderWithI18n(
      <Updating name="Kitchen" model="SoundTouch 10" stage="download" done={false} />
    );
    expect(queryByRole('button', { name: 'Reload now' })).toBeNull();
    rerender(<Updating name="Kitchen" model="SoundTouch 10" stage="check" done />);
    expect(getByRole('button', { name: 'Reload now' })).toBeTruthy();
  });

  it('shows how far it is only when the speaker says', () => {
    const { queryByText, getByText, rerender } = renderWithI18n(
      <Updating name="Kitchen" model="SoundTouch 10" stage="download" done={false} />
    );
    expect(queryByText(/% done/)).toBeNull();
    rerender(
      <Updating name="Kitchen" model="SoundTouch 10" stage="download" done={false} progress={62} />
    );
    expect(getByText('62% done')).toBeTruthy();
    rerender(
      <Updating name="Kitchen" model="SoundTouch 10" stage="download" done={false} progress={0} />
    );
    expect(getByText('0% done')).toBeTruthy();
  });

  // Stock warns that the soundbar's last step can sit a while; only the ST300 says so.
  it('tells an ST300 that the last step can take a while', () => {
    const slow = 'On the SoundTouch 300 the last step can take a while; leave it on.';
    const { queryByText, getByText, rerender } = renderWithI18n(
      <Updating name="TV" model="SoundTouch 10" stage="restart" done={false} />
    );
    expect(queryByText(slow)).toBeNull();
    rerender(<Updating name="TV" model="SoundTouch 300" stage="restart" done={false} />);
    expect(getByText(slow)).toBeTruthy();
  });
});
