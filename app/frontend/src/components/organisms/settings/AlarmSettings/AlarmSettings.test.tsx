// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import { AlarmSettings, presetChoice } from '.';
import { must } from '../../../../test/must';
import { preset } from '../../../../hooks/testFixtures';

describe('AlarmSettings', () => {
  it('saves the preset it shows when the stored one was cleared', () => {
    const onChange = vi.fn();
    const presets = [null, preset({ name: 'Jazz' })];
    const { container } = renderWithI18n(
      <AlarmSettings
        alarm={{ enabled: true, time: '07:00', slot: 1 }}
        presets={presets}
        speakerName="Kitchen"
        onChange={onChange}
      />
    );
    expect(must(container.querySelector<HTMLSelectElement>('#alarm-slot'), 'the slot').value).toBe(
      '2'
    );
    fireEvent.change(must(container.querySelector('#alarm-time'), 'the time'), {
      target: { value: '08:00' },
    });
    expect(onChange).toHaveBeenCalledWith({ enabled: true, time: '08:00', slot: 2 });
  });

  it('names the preset, and the service when it is not radio', () => {
    const presets = [
      preset({ name: 'Radio 2', source: 'TUNEIN' }),
      null,
      preset({ name: 'Sunday morning', source: 'SPOTIFY' }),
    ];
    const { getByLabelText, getByRole } = renderWithI18n(
      <AlarmSettings
        alarm={{ enabled: true, time: '07:00', slot: 3 }}
        presets={presets}
        speakerName="Kitchen"
        onChange={vi.fn()}
      />
    );
    expect(getByLabelText('Preset')).toBeTruthy();
    expect(getByRole('option', { name: '1 · Radio 2' })).toBeTruthy();
    expect(getByRole('option', { name: '3 · Sunday morning (Spotify)' })).toBeTruthy();
    expect(presetChoice(4, { name: 'Jazz' })).toBe('4 · Jazz');
  });
});
