// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from 'react';
import { Button } from 'react-aria-components';
import { Icon } from '../../atoms/Icon';
import { PREF, readPref, writePref } from '../../../lib/storage';

// How long a scroll must pause to count as settled, in a browser without scrollend.
const SETTLE_MS = 150;

interface Props {
  ids: string[];
  // The selected page: centred, with its full content.
  active: string;
  // A swipe came to rest on this page, or an arrow asks for it.
  onSelect: (id: string) => void;
  // One page's content, and whether it is the selected one.
  page: (id: string, active: boolean) => ReactNode;
  // One dot per page: its accessible name, in the order of ids.
  names: string[];
  dotsLabel: string;
  prevLabel: string;
  nextLabel: string;
  // The one-time tip ("Swipe to switch speaker"), shown once per browser.
  tip: string;
}

type Latest = { ids: string[]; active: string; onSelect: (id: string) => void };

// Every page is as wide as the first, and page k is centred at scrollLeft = k * width.
const pageWidth = (el: HTMLDivElement | null) =>
  el?.firstElementChild?.getBoundingClientRect().width || 0;

// Selects the page a swipe comes to rest on. Returns the page a finger has dragged into the
// middle: selecting it waits until the swipe settles, the dots do not.
function useSwipeSettle(
  track: RefObject<HTMLDivElement | null>,
  sliding: RefObject<boolean>,
  latest: RefObject<Latest>
) {
  const [passing, setPassing] = useState('');
  useEffect(() => {
    const el = track.current;
    if (!el) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const nearest = () => {
      const w = pageWidth(el);
      const all = latest.current.ids;
      return w ? all[Math.min(all.length - 1, Math.max(0, Math.round(el.scrollLeft / w)))] : '';
    };
    const settle = () => {
      const { active: now, onSelect: select } = latest.current;
      const id = nearest();
      sliding.current = false;
      setPassing('');
      if (id && id !== now) select(id);
    };
    const follow = () => {
      if (!sliding.current) setPassing(nearest());
    };
    // scrollend fires when the slide stopped; without it, wait for the scrolling to pause.
    const native = 'onscrollend' in window;
    const onScroll = () => {
      clearTimeout(timer);
      timer = setTimeout(settle, SETTLE_MS);
    };
    const [type, listener] = native
      ? (['scrollend', settle] as const)
      : (['scroll', onScroll] as const);
    el.addEventListener(type, listener);
    el.addEventListener('scroll', follow, { passive: true });
    return () => {
      clearTimeout(timer);
      el.removeEventListener(type, listener);
      el.removeEventListener('scroll', follow);
    };
  }, [track, sliding, latest]);
  return passing;
}

// Native scroll-snap: the pages sit side by side and the neighbours peek in at the edges.
// The selected page is the data (`active`); the scroll position follows it (sliding, unless
// the person prefers reduced motion) and a swipe that settles on another page selects it.
export function Carousel(props: Props) {
  const { ids, active, onSelect, page, names, dotsLabel, prevLabel, nextLabel, tip } = props;
  const track = useRef<HTMLDivElement>(null);
  const placed = useRef(false);
  // The scroll is on its way to a page already selected, by a dot or an arrow.
  const sliding = useRef(false);
  const latest = useRef({ ids, active, onSelect });
  const [first] = useState(active);
  const [tipOn] = useState(() => ids.length > 1 && !readPref(PREF.swipeTip));
  const at = ids.indexOf(active);

  useLayoutEffect(() => {
    latest.current = { ids, active, onSelect };
  });
  const passing = useSwipeSettle(track, sliding, latest);

  // Opening puts the selected page in the middle at once; a later selection (dots, arrows)
  // slides to it. A page that joins or leaves the list only keeps the selected one in the
  // middle: sliding there would show the card travel across another speaker. Only those two
  // move the scroll, never a re-render mid-swipe.
  const key = ids.join('|');
  const shown = useRef(active);
  useLayoutEffect(() => {
    const el = track.current;
    const left = at * pageWidth(el);
    if (el && at >= 0 && Math.abs(el.scrollLeft - left) > 1) {
      const slide =
        placed.current &&
        shown.current !== active &&
        !window.matchMedia('(prefers-reduced-motion: reduce)').matches;
      sliding.current = slide;
      if (!slide) el.scrollLeft = left;
      else if ('scrollTo' in el) el.scrollTo({ left, behavior: 'smooth' });
    }
    placed.current = true;
    shown.current = active;
    // `key` too: a page that joins or leaves runs this even where `at` stays the same.
  }, [active, at, key]);

  useEffect(() => {
    if (tipOn) writePref(PREF.swipeTip, '1');
  }, [tipOn]);

  return (
    <>
      <div className="spk-tray">
        <div className="spk-track" ref={track} data-single={ids.length < 2 || undefined}>
          {ids.map((id) => (
            <div key={id} className="spk-page" data-active={id === active || undefined}>
              {page(id, id === active)}
            </div>
          ))}
        </div>
        {at > 0 && (
          <Button
            className="spk-arrow prev"
            aria-label={prevLabel}
            onPress={() => onSelect(ids[at - 1])}
          >
            <Icon.chevron />
          </Button>
        )}
        {at >= 0 && at < ids.length - 1 && (
          <Button
            className="spk-arrow next"
            aria-label={nextLabel}
            onPress={() => onSelect(ids[at + 1])}
          >
            <Icon.chevron />
          </Button>
        )}
        {tipOn && active === first && <p className="spk-tip">{tip}</p>}
      </div>
      {ids.length > 1 && (
        <Dots
          ids={ids}
          on={passing || active}
          names={names}
          label={dotsLabel}
          onSelect={onSelect}
        />
      )}
    </>
  );
}

interface DotsProps {
  ids: string[];
  on: string;
  names: string[];
  label: string;
  onSelect: (id: string) => void;
}

// One dot per page; the one in the middle is filled.
function Dots({ ids, on, names, label, onSelect }: DotsProps) {
  return (
    <div className="dots" role="group" aria-label={label}>
      {ids.map((id, i) => (
        <Button
          key={id}
          className="dot"
          data-on={id === on || undefined}
          aria-label={names[i]}
          aria-current={id === on || undefined}
          onPress={() => onSelect(id)}
        />
      ))}
    </div>
  );
}
