// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactNode, SVGProps } from 'react';

export type SvgAttrs = SVGProps<SVGSVGElement>;

// One icon as data: the <svg> attributes and what it draws. index.tsx turns each
// into a component.
export interface IconSpec {
  svg: SvgAttrs;
  body: ReactNode;
}

// A line icon on the 24px grid with round caps and joins (the Radio v3 default).
export const line = (strokeWidth = '1.6'): SvgAttrs => ({
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
});

// A line icon with round caps and the default (mitre) joins.
export const capped = (strokeWidth = '1.6'): SvgAttrs => ({
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth,
  strokeLinecap: 'round',
});

// A filled glyph on the 24px grid.
export const solid: SvgAttrs = { viewBox: '0 0 24 24', fill: 'currentColor' };
