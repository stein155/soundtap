// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useState } from 'react';
import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render } from '@testing-library/react';
import { SubPageContext } from '../../../hooks/useSubPage';
import { SubPage } from '.';

function Nested() {
  const [open, setOpen] = useState(true);
  return (
    <SubPage open onBack={() => {}} title="Network">
      <button onClick={() => setOpen(false)}>close</button>
      <SubPage open={open} onBack={() => {}} title="Join" children={null} />
    </SubPage>
  );
}

describe('SubPage', () => {
  // A page below a page used to leave the sheet's header without one.
  it('hands the header back to the page it was opened from', () => {
    const show = vi.fn();
    const { getByText } = render(
      <SubPageContext.Provider value={{ layer: null, show }}>
        <Nested />
      </SubPageContext.Provider>
    );
    fireEvent.click(getByText('close'));
    expect(show).toHaveBeenLastCalledWith(expect.objectContaining({ title: 'Network' }));
  });
});
