// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import { Updating } from './Updating';
import { must } from '../../../../test/must';

const close = () => vi.fn();

describe('Updating', () => {
  it('marks the steps before the one under way as done', () => {
    const { getByRole, getAllByRole } = renderWithI18n(
      <Updating
        name="Kitchen"
        model="SoundTouch 10"
        stage="restart"
        done={false}
        onClose={close()}
      />
    );
    expect(getByRole('alertdialog', { name: 'The speaker is updating itself' })).toBeTruthy();
    const steps = getAllByRole('listitem');
    expect(steps.map((s) => s.getAttribute('aria-current'))).toEqual([null, null, 'step', null]);
    expect(must(steps[2], 'the third step').textContent).toBe('Restarting');
  });

  it('offers to reload once the new version answers', () => {
    const onClose = close();
    const { getByRole, queryByRole, rerender } = renderWithI18n(
      <Updating
        name="Kitchen"
        model="SoundTouch 10"
        stage="download"
        done={false}
        onClose={onClose}
      />
    );
    expect(queryByRole('button', { name: 'Reload now' })).toBeNull();
    rerender(
      <Updating name="Kitchen" model="SoundTouch 10" stage="check" done onClose={onClose} />
    );
    expect(getByRole('button', { name: 'Reload now' })).toBeTruthy();
  });

  it('shows how far it is only when the speaker says', () => {
    const onClose = close();
    const { queryByText, getByText, rerender } = renderWithI18n(
      <Updating
        name="Kitchen"
        model="SoundTouch 10"
        stage="download"
        done={false}
        onClose={onClose}
      />
    );
    expect(queryByText(/% done/u)).toBeNull();
    rerender(
      <Updating
        name="Kitchen"
        model="SoundTouch 10"
        stage="download"
        done={false}
        progress={62}
        onClose={onClose}
      />
    );
    expect(getByText('62% done')).toBeTruthy();
    rerender(
      <Updating
        name="Kitchen"
        model="SoundTouch 10"
        stage="download"
        done={false}
        progress={0}
        onClose={onClose}
      />
    );
    expect(getByText('0% done')).toBeTruthy();
  });

  // Stock warns that the soundbar's last step can sit a while; only the ST300 says so.
  it('tells an ST300 that the last step can take a while', () => {
    const onClose = close();
    const slow = 'On the SoundTouch 300 the last step can take a while; leave it on.';
    const { queryByText, getByText, rerender } = renderWithI18n(
      <Updating name="TV" model="SoundTouch 10" stage="restart" done={false} onClose={onClose} />
    );
    expect(queryByText(slow)).toBeNull();
    rerender(
      <Updating name="TV" model="SoundTouch 300" stage="restart" done={false} onClose={onClose} />
    );
    expect(getByText(slow)).toBeTruthy();
  });

  // The update goes on without the page: Close, Escape and the phone's back all leave it, and
  // the warning to keep it plugged in is on it all the while.
  it('can be left, by Close or Escape, and keeps the warning in view', () => {
    const onClose = close();
    const { getByRole, getByText } = renderWithI18n(
      <Updating
        name="Kitchen"
        model="SoundTouch 10"
        stage="download"
        done={false}
        onClose={onClose}
      />
    );
    expect(getByText('Keep it plugged in.')).toBeTruthy();
    fireEvent.click(getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('goes on to another speaker when asked', () => {
    const onOther = vi.fn();
    const { getByRole, queryByRole, rerender } = renderWithI18n(
      <Updating
        name="Kitchen"
        model="SoundTouch 10"
        stage="download"
        done={false}
        onClose={close()}
      />
    );
    expect(queryByRole('button', { name: 'Choose another speaker' })).toBeNull();
    rerender(
      <Updating
        name="Kitchen"
        model="SoundTouch 10"
        stage="download"
        done={false}
        onClose={close()}
        onOther={onOther}
      />
    );
    fireEvent.click(getByRole('button', { name: 'Choose another speaker' }));
    expect(onOther).toHaveBeenCalledTimes(1);
  });
});
