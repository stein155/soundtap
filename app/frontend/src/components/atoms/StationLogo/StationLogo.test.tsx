// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect } from 'vitest';
import { fireEvent, render } from '@testing-library/react';
import { StationLogo } from '.';
import { must } from '../../../test/must';

describe('StationLogo', () => {
  it('shows the lettermark until the image loads, then only the image', () => {
    const { container, getByText } = render(<StationLogo src="/logo.png" initials="NR" />);
    const img = must(container.querySelector('img'), 'the logo');
    expect(getByText('NR')).toBeVisible();
    expect(img).toHaveStyle({ opacity: '0' });
    fireEvent.load(img);
    expect(getByText('NR')).not.toBeVisible();
    expect(img).toHaveStyle({ opacity: '1' });
  });

  it('keeps the lettermark when the image is broken or missing', () => {
    const { container, rerender } = render(<StationLogo src="/broken.png" initials="NR" />);
    fireEvent.error(must(container.querySelector('img'), 'the logo'));
    expect(container.querySelector('img')).toBeNull();
    rerender(<StationLogo src={null} initials="KI" />);
    expect(container).toHaveTextContent('KI');
  });

  it('starts over for a new image', () => {
    const { container, rerender } = render(<StationLogo src="/a.png" initials="A" />);
    fireEvent.error(must(container.querySelector('img'), 'the logo'));
    rerender(<StationLogo src="/b.png" initials="B" />);
    expect(container.querySelector('img')).not.toBeNull();
  });
});
