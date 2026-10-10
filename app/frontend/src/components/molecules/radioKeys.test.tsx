// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ChoiceList } from './ChoiceList';
import { SegmentedControl } from './SegmentedControl';

const options = [
  { value: '12', label: '12h' },
  { value: '24', label: '24h' },
];

// ARIA radio groups: one tab stop, arrows move focus and selection.
describe.each([
  ['ChoiceList', ChoiceList],
  ['SegmentedControl', SegmentedControl],
])('%s keyboard', (_name, Component) => {
  it('arrows select and focus the next radio, wrapping', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    const view = render(
      <Component label="Format" value="12" options={options} onChange={onChange} />
    );
    const radios = view.getAllByRole('radio');
    expect(radios.map((r) => r.tabIndex)).toEqual([0, -1]);
    radios[0].focus();
    await user.keyboard('{ArrowRight}');
    expect(onChange).toHaveBeenCalledWith('24');
    expect(document.activeElement).toBe(radios[1]);
    view.rerender(<Component label="Format" value="24" options={options} onChange={onChange} />);
    await user.keyboard('{ArrowRight}');
    expect(onChange).toHaveBeenLastCalledWith('12');
  });
});
