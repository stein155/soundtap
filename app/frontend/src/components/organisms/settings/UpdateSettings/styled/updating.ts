// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import styled, { css, keyframes } from 'styled-components';

type StepState = 'done' | 'now' | 'later';

const turn = keyframes`
  to { transform: rotate(360deg); }
`;

// Over the whole app while it is on show; Close leaves it for the app, the update goes on.
export const Page = styled.div`
  position: fixed;
  inset: 0;
  z-index: 90;
  display: flex;
  flex-direction: column;
  overflow-y: auto;
  background: var(--bg);

  &:focus {
    outline: none;
  }
`;

export const Head = styled.div`
  padding-top: env(safe-area-inset-top);
  background: var(--surface);
`;

// The eyebrow centred in the bar, Close at its end as on the other full pages.
export const TopLine = styled.div`
  position: relative;
  display: grid;
  place-items: center;
  height: 56px;
  margin: 0;
  padding: 0 50px;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.5px;
  text-transform: uppercase;
  color: var(--ink-3);
`;

export const Close = styled.button`
  position: absolute;
  top: 6px;
  right: 6px;
  display: grid;
  place-items: center;
  width: 44px;
  height: 44px;
  padding: 0;
  border: 0;
  background: none;
  color: var(--ink);
  cursor: pointer;

  svg {
    width: 22px;
    height: 22px;
  }
`;

// A line per step, filled up to the one under way.
export const Bars = styled.div`
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 4px;
  padding: 0 16px 14px;

  span {
    height: 3px;
    border-radius: 2px;
    background: var(--surface-3);
  }

  span[data-done] {
    background: var(--ink);
  }
`;

export const Hero = styled.div`
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
  padding: 10px 24px 24px;
  text-align: center;
`;

// The speaker on its grey disc inside a ring that keeps turning.
export const Ring = styled.div`
  position: relative;
  width: 148px;
  height: 148px;

  & > div {
    position: absolute;
    inset: 10px;
    padding: 20px;
    border-radius: 50%;
    background: var(--surface-2);
  }

  & > div svg {
    display: block;
    width: 100%;
    height: 100%;
  }
`;

export const Spin = styled.svg`
  position: absolute;
  inset: 0;
  fill: none;
  stroke-width: 4;
  animation: ${turn} 1.6s linear infinite;

  circle {
    stroke: var(--section-bar);
  }

  path {
    stroke: var(--ink);
    stroke-linecap: round;
  }

  @media (prefers-reduced-motion: reduce) {
    animation: none;
  }
`;

// How far the update is, small over the title, as the speaker reports it.
export const Percent = styled.p`
  margin: 0 0 -8px;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--ink-3);
`;

export const Intro = styled.div`
  h1 {
    margin: 0;
    font-size: 24px;
    font-weight: 600;
    letter-spacing: var(--tracking-title);
  }

  p {
    max-width: 30ch;
    margin: 8px auto 0;
    font-size: 14px;
    line-height: 1.5;
    color: var(--ink-3);
  }
`;

export const StepList = styled.ol`
  margin: 0;
  padding: 6px 0;
  list-style: none;
  border-top: 1px solid var(--border);
  background: var(--surface);
`;

// One plain line under the steps, where a model's update is known to linger.
export const StepNote = styled.p`
  margin: 0;
  padding: 2px 20px 8px;
  background: var(--surface);
  color: var(--ink-2);
  font-size: 13px;
  line-height: 1.45;
`;

export const StepRow = styled.li<{ $state: StepState }>`
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 10px 20px;
  font-size: 14px;
  font-weight: ${({ $state }) => ($state === 'now' ? 700 : 400)};
  color: ${({ $state }) => ($state === 'later' ? 'var(--ink-3)' : 'var(--ink)')};
`;

// Done: a filled disc with a tick; under way: a ring with a dot; to come: a pale ring.
export const Dot = styled.span<{ $state: StepState }>`
  width: 22px;
  height: 22px;
  flex: none;
  display: grid;
  place-items: center;
  border-radius: 50%;
  ${({ $state }) =>
    $state === 'done'
      ? css`
          background: var(--accent-fill);
          color: var(--on-accent-fill);

          svg {
            stroke-width: 3;
          }
        `
      : css`
          box-shadow: inset 0 0 0 2px ${$state === 'now' ? 'var(--ink)' : 'var(--surface-3)'};
        `}

  ${({ $state }) =>
    $state === 'now' &&
    css`
      &::after {
        content: '';
        width: 8px;
        height: 8px;
        border-radius: 50%;
        background: var(--ink);
      }
    `}
`;

// Keep it on the power, in the amber of a notice.
export const Warn = styled.div`
  display: flex;
  gap: 10px;
  margin: 14px 16px calc(24px + env(safe-area-inset-bottom));
  padding: 12px 14px;
  border-radius: var(--radius);
  background: var(--warn-bg);
  box-shadow: inset 0 0 0 1px var(--warn-line);
  color: var(--warn-ink);

  svg {
    flex: none;
    margin-top: 1px;
  }

  p {
    margin: 0;
    font-size: 13px;
    line-height: 1.45;
  }

  /* The way on to another speaker follows: it keeps the room below. */
  &:has(+ *) {
    margin-bottom: 0;
  }
`;

// The way on to another speaker, under the warning.
export const Other = styled.div`
  display: flex;
  justify-content: center;
  padding: 16px 16px calc(24px + env(safe-area-inset-bottom));
`;

// The way back to a closed update screen, small over the dock: the speaker and how far it is.
export const Pill = styled.button`
  position: absolute;
  bottom: calc(100% + 10px);
  left: 50%;
  max-width: calc(100% - 32px);
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  border: 0;
  border-radius: 999px;
  background: var(--ink);
  color: var(--on-ink);
  font: inherit;
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
  box-shadow: var(--shadow-lg);
  transform: translateX(-50%);
  cursor: pointer;

  span {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  svg {
    flex: none;
  }
`;
