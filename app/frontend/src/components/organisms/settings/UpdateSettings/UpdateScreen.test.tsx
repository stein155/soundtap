// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { act, fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import { room } from '../../../../hooks/testFixtures';
import { openUpdateScreen, setRun } from '../../../../hooks/updates';
import type { VersionInfo } from '../../../../lib/types';
import { UpdateNotice, UpdateScreen } from './UpdateScreen';
import { UpdateSettings } from '.';

vi.mock('../../../../lib/api', () => ({
  getReleases: vi.fn().mockResolvedValue(null),
  getVersion: vi.fn().mockResolvedValue(null),
}));

const rooms = [
  room({ id: 'kitchen', name: 'Kitchen', model: 'SoundTouch 10' }),
  room({ id: 'office', name: 'Office', model: 'SoundTouch 20' }),
];
const title = { name: 'The speaker is updating itself' };

// The app's layer: the screen over every page, the notice on the dock.
const app = (onOther = vi.fn()) =>
  renderWithI18n(
    <>
      <UpdateScreen rooms={rooms} onOther={onOther} />
      <UpdateNotice rooms={rooms} />
    </>
  );

const downloading = (speaker: string, progress: number | null = 42) =>
  act(() => setRun(speaker, { phase: 'busy', stage: 'download', progress, text: '' }));

describe('UpdateScreen', () => {
  it('shows nothing while no speaker updates', () => {
    const { container } = app();
    expect(container.textContent).toBe('');
  });

  it('follows the speaker it is open for, with its name and how far it is', () => {
    const { getByRole, getByText } = app();
    downloading('kitchen');
    act(() => openUpdateScreen('kitchen'));
    expect(getByRole('alertdialog', title)).toBeTruthy();
    expect(getByText('Update · Kitchen')).toBeTruthy();
    expect(getByText('42% done')).toBeTruthy();
  });

  // Leaving the screen leaves the update running: the notice says how far it is, and
  // brings the screen back.
  it('can be closed while the update goes on, and comes back from the notice', () => {
    const { getByRole, queryByRole } = app();
    downloading('kitchen');
    act(() => openUpdateScreen('kitchen'));
    expect(queryByRole('button', { name: /^Update · Kitchen/u })).toBeNull();
    fireEvent.click(getByRole('button', { name: 'Close' }));
    expect(queryByRole('alertdialog')).toBeNull();
    downloading('kitchen', 57);
    const notice = getByRole('button', { name: 'Update · Kitchen · 57% done' });
    fireEvent.click(notice);
    expect(getByRole('alertdialog', title)).toBeTruthy();
  });

  it('goes on to the speakers, closing the screen', () => {
    const onOther = vi.fn();
    const { getByRole, queryByRole } = app(onOther);
    downloading('kitchen');
    act(() => openUpdateScreen('kitchen'));
    fireEvent.click(getByRole('button', { name: 'Choose another speaker' }));
    expect(onOther).toHaveBeenCalledOnce();
    expect(queryByRole('alertdialog')).toBeNull();
    expect(getByRole('button', { name: /^Update · Kitchen/u })).toBeTruthy();
  });

  it('says on the notice when the update is done', () => {
    const { getByRole } = app();
    act(() => setRun('office', { phase: 'updated', stage: 'check', text: 'Updated to 0.2.0' }));
    expect(getByRole('button', { name: 'Update · Office · Updated to 0.2.0' })).toBeTruthy();
  });

  it('forgets a failed update: no screen, no notice', () => {
    const { container } = app();
    downloading('kitchen');
    act(() => openUpdateScreen('kitchen'));
    act(() => setRun('kitchen', { phase: 'error', text: 'Update failed.' }));
    expect(container.textContent).toBe('');
  });
});

const firmware: VersionInfo = {
  version: '0.1.0',
  os: '0.1.0',
  app: '0.1.0',
  commit: '',
  updatable: true,
  updateMode: 'firmware',
  api: 1,
  capabilities: [],
};

// The release lists the Updates page asks for have answered.
const settled = async () =>
  act(async () => {
    await Promise.resolve();
  });

describe('UpdateSettings and the update screen', () => {
  // The speaker's Updates page is a way back to the screen of the update it runs.
  it('opens the screen again when the speaker’s Updates page is opened mid-update', async () => {
    downloading('kitchen');
    const { getByRole } = renderWithI18n(
      <>
        <UpdateScreen rooms={rooms} />
        <UpdateSettings speaker="kitchen" version={firmware} />
      </>
    );
    await settled();
    expect(getByRole('alertdialog', title)).toBeTruthy();
  });

  it('leaves the screen shut on the Updates page of a speaker that does not update', async () => {
    downloading('kitchen');
    const { queryByRole } = renderWithI18n(
      <>
        <UpdateScreen rooms={rooms} />
        <UpdateSettings speaker="office" version={firmware} />
      </>
    );
    await settled();
    expect(queryByRole('alertdialog')).toBeNull();
  });
});
