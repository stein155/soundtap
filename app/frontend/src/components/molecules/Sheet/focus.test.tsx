// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { act, fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it } from 'vitest';
import { Sheet } from '.';

function Levels() {
  const [level, setLevel] = useState('list');
  return (
    <Sheet
      onBack={level === 'list' ? undefined : () => setLevel('list')}
      backLabel="Back"
      eyebrow=""
      headline="Page"
      scrollKey={level}
    >
      {level === 'list' ? (
        <>
          <button type="button">Sound</button>
          <button type="button" onClick={() => setLevel('language')}>
            Language
          </button>
        </>
      ) : (
        <p>English</p>
      )}
    </Sheet>
  );
}

describe('Sheet focus', () => {
  it('lands on the page going deeper and on the opening row going back', () => {
    render(<Levels />);
    const row = screen.getByText('Language');
    act(() => row.focus());
    fireEvent.click(row);
    expect(document.activeElement).toBe(screen.getByText('English').closest('[tabindex="-1"]'));
    fireEvent.click(screen.getByLabelText('Back'));
    expect(document.activeElement).toBe(screen.getByText('Language'));
  });
});
