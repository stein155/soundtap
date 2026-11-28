// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../../../test/render';
import { nowPlaying, room } from '../../../hooks/testFixtures';
import type { UpdateRun } from '../../../hooks/updates';
import { Dock } from '.';

const kitchen = room({
  name: 'Kitchen',
  now: nowPlaying({
    source: 'TUNEIN',
    stationName: 'Radio Foo',
    tuneInId: 's1',
    playStatus: 'PLAY_STATE',
  }),
});

const dock = (update: UpdateRun | null = null) =>
  renderWithI18n(
    <Dock
      lead={kitchen}
      members={[]}
      starting={false}
      unreachable={false}
      update={update}
      notice={<p>notice</p>}
      onTransport={vi.fn()}
      onPlayer={vi.fn()}
      onChoose={vi.fn()}
      onSpeaker={vi.fn()}
      onVolume={vi.fn()}
    />
  );

describe('Dock', () => {
  it('lets the speaker be played and turned up while it does not update', () => {
    const { getByRole, getByText } = dock();
    expect(getByRole('button', { name: 'Volume Kitchen' })).toBeEnabled();
    expect(getByRole('button', { name: /· Kitchen$/u })).toBeEnabled();
    expect(getByText('notice')).toBeTruthy();
  });

  // Its controls wait while it updates; its lines say how far it is. The speaker bar still
  // opens the speakers, to pick another one meanwhile.
  it('holds the controls of a speaker that updates and says how far it is', () => {
    const { getByRole, getAllByText } = dock({
      phase: 'busy',
      stage: 'download',
      progress: 42,
      text: '',
    });
    expect(getByRole('button', { name: 'Volume Kitchen' })).toBeDisabled();
    expect(getByRole('button', { name: /· Kitchen$/u })).toBeDisabled();
    expect(getByRole('button', { name: 'Speaker: Kitchen' })).toBeEnabled();
    expect(getAllByText('Updating… 42% done')).toHaveLength(2);
  });

  it('says it updates without a number while the speaker restarts', () => {
    const { getAllByText } = dock({ phase: 'busy', stage: 'restart', progress: 99, text: '' });
    expect(getAllByText('Updating…')).toHaveLength(2);
  });
});
