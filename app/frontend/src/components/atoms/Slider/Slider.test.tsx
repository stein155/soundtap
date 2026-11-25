// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render } from '@testing-library/react';
import { Slider } from '.';

// jsdom reports a zero-size box, so the geometry is stubbed.
function stubRect(el: Element, { left = 0, width = 200 } = {}) {
  el.getBoundingClientRect = () => new DOMRect(left, 0, width, 24);
}

describe('Slider', () => {
  it('maps a press to its range, clamped, and follows a captured drag', () => {
    const onChange = vi.fn();
    const { getByRole } = render(<Slider value={20} onChange={onChange} label="Volume" />);
    const el = getByRole('slider', { name: 'Volume' });
    stubRect(el);
    fireEvent.pointerDown(el, { clientX: 150, pointerId: 1 });
    expect(onChange).toHaveBeenLastCalledWith(75);
    el.hasPointerCapture = () => true;
    fireEvent.pointerMove(el, { clientX: 999, pointerId: 1 });
    expect(onChange).toHaveBeenLastCalledWith(100);
    fireEvent.pointerMove(el, { clientX: -40, pointerId: 1 });
    expect(onChange).toHaveBeenLastCalledWith(0);
  });

  it('steps with the keyboard and jumps with Home/End', () => {
    const onChange = vi.fn();
    const { getByRole } = render(<Slider value={50} onChange={onChange} label="Volume" step={2} />);
    const el = getByRole('slider');
    fireEvent.keyDown(el, { key: 'ArrowRight' });
    expect(onChange).toHaveBeenLastCalledWith(52);
    fireEvent.keyDown(el, { key: 'End' });
    expect(onChange).toHaveBeenLastCalledWith(100);
  });

  it('works on a negative range such as bass', () => {
    const onChange = vi.fn();
    const { getByRole } = render(
      <Slider value={0} onChange={onChange} label="Bass" min={-9} max={0} origin={0} />
    );
    const el = getByRole('slider');
    stubRect(el, { width: 90 });
    fireEvent.pointerDown(el, { clientX: 0, pointerId: 1 });
    expect(onChange).toHaveBeenLastCalledWith(-9);
  });

  // A finger nudges on a tap and only drags sideways; a vertical swipe is a scroll.
  it('nudges on a tap and ignores a vertical swipe on a touch screen', () => {
    const onChange = vi.fn();
    const { getByRole } = render(<Slider value={30} onChange={onChange} label="Volume" />);
    const el = getByRole('slider');
    stubRect(el);
    fireEvent.pointerDown(el, { clientX: 180, clientY: 10, pointerId: 2, pointerType: 'touch' });
    fireEvent.pointerUp(el, { clientX: 180, clientY: 10, pointerId: 2, pointerType: 'touch' });
    expect(onChange).toHaveBeenLastCalledWith(40);
    onChange.mockClear();
    fireEvent.pointerDown(el, { clientX: 50, clientY: 10, pointerId: 3, pointerType: 'touch' });
    fireEvent.pointerMove(el, { clientX: 52, clientY: 60, pointerId: 3, pointerType: 'touch' });
    fireEvent.pointerUp(el, { clientX: 52, clientY: 60, pointerId: 3, pointerType: 'touch' });
    expect(onChange).not.toHaveBeenCalled();
    fireEvent.pointerDown(el, { clientX: 50, clientY: 10, pointerId: 4, pointerType: 'touch' });
    fireEvent.pointerMove(el, { clientX: 150, clientY: 12, pointerId: 4, pointerType: 'touch' });
    expect(onChange).toHaveBeenLastCalledWith(75);
  });
});
