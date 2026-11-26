// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render, waitFor } from '@testing-library/react';
import { useEscape } from '../../../hooks/useEscape';
import { SheetModal } from '.';

const touch = (y: number, x = 0) => [{ clientX: x, clientY: y }];

// A running CSS animation as getAnimations() lists it.
function fake(animationName: string, target: Element, pseudoElement: string | null) {
  const a = {
    animationName,
    playState: 'running',
    effect: { target, pseudoElement },
    finished: Promise.resolve(),
    commitStyles: vi.fn(),
    cancel: vi.fn(() => {
      a.playState = 'idle';
    }),
  };
  return a;
}

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

  // The exit keyframes have no start of their own: a sheet let go far enough leaves from
  // the finger's offset, the dim from how far it had lightened, with no jump back first.
  it('leaves a swiped sheet and its dim where the finger let go', () => {
    const height = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(800);
    const onClose = vi.fn();
    const { getByText } = render(
      <SheetModal open onClose={onClose} label="Player" variant="full">
        <p>Body</p>
      </SheetModal>
    );
    const body = getByText('Body');
    const sheet = body.closest<HTMLElement>('.sheet');
    const overlay = body.closest<HTMLElement>('.overlay');
    if (!sheet || !overlay) throw new Error('no layer');

    fireEvent.touchStart(body, { touches: touch(0, 200) });
    fireEvent.touchMove(body, { touches: touch(200, 200) });
    expect(overlay.hasAttribute('data-pulling')).toBe(true);
    expect(sheet.style.willChange).toBe('transform');
    fireEvent.touchEnd(body, { changedTouches: touch(200, 200) });

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(sheet.style.transform).toBe('translateY(200px)');
    expect(overlay.style.getPropertyValue('--pull')).toBe('0.750');
    expect(overlay.hasAttribute('data-pulling')).toBe(false);
    expect(sheet.style.willChange).toBe('');
    // It leaves at the finger's speed, not from rest (home.css data-flung).
    expect(overlay.hasAttribute('data-flung')).toBe(true);
    expect(overlay.style.getPropertyValue('--ease-fling')).toMatch(/^cubic-bezier\(0\.25, /u);
    height.mockRestore();
  });

  // The owner kept it open (a question first, say): it does not stay where it was let go.
  it('springs a swiped sheet back when its owner keeps it open', async () => {
    const height = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(800);
    const onClose = vi.fn();
    const { getByText } = render(
      <SheetModal open onClose={onClose} label="Player" variant="full">
        <p>Body</p>
      </SheetModal>
    );
    const body = getByText('Body');
    const sheet = body.closest<HTMLElement>('.sheet');
    const overlay = body.closest<HTMLElement>('.overlay');
    if (!sheet || !overlay) throw new Error('no layer');

    swipe(body, 200);
    expect(onClose).toHaveBeenCalledTimes(1);
    await waitFor(() => {
      expect(sheet.style.transform).toBe('');
    });
    expect(overlay.style.getPropertyValue('--pull')).toBe('');
    expect(overlay.hasAttribute('data-flung')).toBe(false);
    expect(overlay.style.getPropertyValue('--ease-fling')).toBe('');
    expect(overlay.hasAttribute('data-pulling')).toBe(false);
    height.mockRestore();
  });

  // A finger on a sheet still sliding in takes it from where it is, not from open.
  it('takes over a sheet still coming in from where its entrance got to', () => {
    const height = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(800);
    const { getByText } = render(
      <SheetModal open onClose={vi.fn()} label="Player" variant="full">
        <p>Body</p>
      </SheetModal>
    );
    const body = getByText('Body');
    const sheet = body.closest<HTMLElement>('.sheet');
    const overlay = body.closest<HTMLElement>('.overlay');
    if (!sheet || !overlay) throw new Error('no layer');
    const slideIn = fake('slide-in', sheet, null);
    const dimIn = fake('dim-in', overlay, '::before');
    Object.assign(sheet, { getAnimations: () => [slideIn] });
    Object.assign(overlay, { getAnimations: () => [dimIn, slideIn] });
    const real = window.getComputedStyle.bind(window);
    vi.stubGlobal('getComputedStyle', (el: Element, pseudo?: string | null) =>
      el === sheet ? { transform: 'matrix(1, 0, 0, 1, 0, 300)' } : real(el, pseudo)
    );

    fireEvent.touchStart(body, { touches: touch(0, 200) });
    fireEvent.touchMove(body, { touches: touch(100, 200) });
    expect(slideIn.cancel).toHaveBeenCalled();
    expect(dimIn.cancel).toHaveBeenCalled();
    expect(sheet.style.transform).toBe('translateY(400px)');
    expect(overlay.style.getPropertyValue('--pull')).toBe('0.500');
    fireEvent.touchEnd(body, { changedTouches: touch(100, 200) });
    vi.unstubAllGlobals();
    height.mockRestore();
  });

  // Closed while still coming in: the overlay's exit freezes the entrance where it got to.
  it('freezes a sheet closed during its entrance as its exit starts', () => {
    const view = render(
      <SheetModal open onClose={vi.fn()} label="Player">
        <p>Body</p>
      </SheetModal>
    );
    const sheet = view.getByText('Body').closest<HTMLElement>('.sheet');
    const overlay = view.getByText('Body').closest<HTMLElement>('.overlay');
    if (!sheet || !overlay) throw new Error('no layer');
    const slideIn = fake('slide-in', sheet, null);
    Object.assign(sheet, { getAnimations: () => [slideIn] });
    Object.assign(overlay, { getAnimations: () => [slideIn] });

    view.rerender(
      <SheetModal open={false} onClose={vi.fn()} label="Player">
        <p>Body</p>
      </SheetModal>
    );
    expect(slideIn.commitStyles).toHaveBeenCalledTimes(1);
    expect(slideIn.cancel).toHaveBeenCalledTimes(1);
  });

  it('springs a short swipe back, dim and all', () => {
    const height = vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(800);
    const onClose = vi.fn();
    const { getByText } = render(
      <SheetModal open onClose={onClose} label="Player" variant="full">
        <p>Body</p>
      </SheetModal>
    );
    const body = getByText('Body');
    const sheet = body.closest<HTMLElement>('.sheet');
    const overlay = body.closest<HTMLElement>('.overlay');
    if (!sheet || !overlay) throw new Error('no layer');

    fireEvent.touchStart(body, { touches: touch(0, 200) });
    fireEvent.touchMove(body, { touches: touch(25, 200) });
    expect(overlay.style.getPropertyValue('--pull')).not.toBe('');
    fireEvent.touchEnd(body, { changedTouches: touch(25, 200) });

    expect(onClose).not.toHaveBeenCalled();
    expect(sheet.style.transform).toBe('');
    expect(overlay.style.getPropertyValue('--pull')).toBe('');
    expect(overlay.hasAttribute('data-pulling')).toBe(false);
    height.mockRestore();
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
      return <button type="button">Time</button>;
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
          <button type="button">Time</button>
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
