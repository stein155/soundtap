// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render } from '@testing-library/react';
import { useEscape } from '../../../hooks/useEscape';
import { SheetModal } from '.';

const touch = (y: number, x = 0) => [{ clientX: x, clientY: y }];

function swipe(el: Element, dy: number, dx = 0) {
  fireEvent.touchStart(el, { touches: touch(0, 200) });
  fireEvent.touchMove(el, { touches: touch(dy, 200 + dx) });
  fireEvent.touchEnd(el, { changedTouches: touch(dy, 200 + dx) });
}

describe('SheetModal', () => {
  it('closes on a swipe down, not on a short or sideways one', () => {
    const onClose = vi.fn();
    const { getByText } = render(
      <SheetModal open onClose={onClose} label="Player" variant="full">
        <p>Body</p>
      </SheetModal>
    );
    const body = getByText('Body');
    swipe(body, 25);
    swipe(body, 200, 400);
    expect(onClose).not.toHaveBeenCalled();
    swipe(body, 200);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes on the phone back, which then stays in the app', () => {
    const onClose = vi.fn();
    render(
      <SheetModal open onClose={onClose} label="Presets">
        <p>Body</p>
      </SheetModal>
    );
    expect(window.history.state).toEqual({ soundtapOverlay: true });
    window.dispatchEvent(new PopStateEvent('popstate'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes the drawer on a swipe to the left, not down', () => {
    const onClose = vi.fn();
    const { getByText } = render(
      <SheetModal open onClose={onClose} label="Menu" variant="drawer">
        <p>Menu</p>
      </SheetModal>
    );
    const body = getByText('Menu');
    swipe(body, 200);
    expect(onClose).not.toHaveBeenCalled();
    swipe(body, 0, -200);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes the inner page first when both open together', () => {
    const outer = vi.fn();
    const inner = vi.fn();
    function Inner() {
      useEscape(true, inner);
      return <button>Time</button>;
    }
    const { getByText, unmount } = render(
      <SheetModal open onClose={outer} label="Settings">
        <Inner />
      </SheetModal>
    );
    fireEvent.keyDown(getByText('Time'), { key: 'Escape' });
    expect(inner).toHaveBeenCalledTimes(1);
    expect(outer).not.toHaveBeenCalled();
    unmount();
  });

  it('closes one level on Escape with focus inside the top one', () => {
    const outer = vi.fn();
    const inner = vi.fn();
    const stack = (open: boolean) => (
      <SheetModal open onClose={outer} label="Settings">
        <SheetModal open={open} onClose={inner} label="Alarm">
          <button>Time</button>
        </SheetModal>
      </SheetModal>
    );
    // The inner one opens later, as a sub-page does: the stack follows the order of opening.
    const { getByText, rerender, unmount } = render(stack(false));
    rerender(stack(true));
    fireEvent.keyDown(getByText('Time'), { key: 'Escape' });
    expect(inner).toHaveBeenCalledTimes(1);
    expect(outer).not.toHaveBeenCalled();
    unmount();
  });
});
