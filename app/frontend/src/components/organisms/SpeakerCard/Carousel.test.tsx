// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { act, fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { Carousel } from './Carousel';

const show = (onSelect = vi.fn()) => {
  render(
    <Carousel
      ids={['a', 'b']}
      active="a"
      onSelect={onSelect}
      page={(id) => <p>{id}</p>}
      names={['Keuken', 'Zolder']}
      dotsLabel="Rooms"
      prevLabel="prev"
      nextLabel="next"
      tip="tip"
    />
  );
  const track = document.querySelector('.spk-track') as HTMLElement;
  const page = track.firstElementChild as HTMLElement;
  page.getBoundingClientRect = () => ({ width: 300 }) as DOMRect;
  return track;
};

describe('Carousel dots', () => {
  it('follow the finger before the swipe settles', () => {
    const onSelect = vi.fn();
    const track = show(onSelect);
    expect(screen.getByRole('button', { name: 'Keuken' })).toHaveAttribute('data-on');

    act(() => {
      track.scrollLeft = 200; // past the middle, still moving
      fireEvent.scroll(track);
    });

    expect(screen.getByRole('button', { name: 'Zolder' })).toHaveAttribute('data-on');
    expect(screen.getByRole('button', { name: 'Keuken' })).not.toHaveAttribute('data-on');
    expect(onSelect).not.toHaveBeenCalled();
  });
});

describe('Carousel scroll', () => {
  const card = (ids: string[], active: string) => (
    <Carousel
      ids={ids}
      active={active}
      onSelect={vi.fn()}
      page={(id) => <p>{id}</p>}
      names={ids}
      dotsLabel="Rooms"
      prevLabel="prev"
      nextLabel="next"
      tip="tip"
    />
  );

  it('keeps the selected page in place, without sliding, when another page leaves', () => {
    window.matchMedia = vi.fn(() => ({ matches: false })) as unknown as typeof window.matchMedia;
    const { rerender } = render(card(['a', 'b', 'c'], 'c'));
    const track = document.querySelector('.spk-track') as HTMLElement;
    const scrollTo = vi.fn();
    track.scrollTo = scrollTo;
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockReturnValue({ width: 300 } as DOMRect);

    rerender(card(['b', 'c'], 'c'));

    expect(scrollTo).not.toHaveBeenCalled();
    expect(track.scrollLeft).toBe(300);
    vi.restoreAllMocks();
  });
});
