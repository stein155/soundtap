// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { fireEvent } from '@testing-library/react';
import { renderWithI18n } from '../../../../test/render';
import type { Preset } from '../../../../lib/types';
import { AlarmSettings } from '.';

describe('AlarmSettings', () => {
  it('saves the preset it shows when the stored one was cleared', () => {
    const onChange = vi.fn();
    const presets = [null, { name: 'Jazz' } as Preset];
    const { container } = renderWithI18n(
      <AlarmSettings
        alarm={{ enabled: true, time: '07:00', slot: 1 }}
        presets={presets}
        speakerName="Kitchen"
        onChange={onChange}
      />
    );
    expect((container.querySelector('#alarm-slot') as HTMLSelectElement).value).toBe('2');
    fireEvent.change(container.querySelector('#alarm-time')!, { target: { value: '08:00' } });
    expect(onChange).toHaveBeenCalledWith({ enabled: true, time: '08:00', slot: 2 });
  });
});
