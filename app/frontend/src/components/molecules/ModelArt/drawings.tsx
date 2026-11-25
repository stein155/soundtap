// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';

// fill names one of the gradients or patterns in Defs, for this drawing instance.
export interface DrawingProps {
  fill: (name: string) => string;
}

// The gradients and grille the drawings fill with, named per instance: several drawings
// are on screen at once.
export function Defs({ id }: { id: string }): ReactElement {
  return (
    <defs>
      <linearGradient id={`${id}gb`} x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stopColor="#2e2f32" />
        <stop offset="1" stopColor="#36373b" />
      </linearGradient>
      <linearGradient id={`${id}gt`} x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stopColor="#555658" />
        <stop offset="1" stopColor="#47484a" />
      </linearGradient>
      <linearGradient id={`${id}gw`} x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stopColor="#4a4a4a" />
        <stop offset="1" stopColor="#3a3a3a" />
      </linearGradient>
      <linearGradient id={`${id}gf`} x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stopColor="#262626" />
        <stop offset="1" stopColor="#121212" />
      </linearGradient>
      <linearGradient id={`${id}gp`} x1="0" y1="0" x2="0" y2="1">
        <stop offset="0" stopColor="#3c3c3e" />
        <stop offset="1" stopColor="#0a0a0a" />
      </linearGradient>
      <pattern id={`${id}grille`} width="1.6" height="1.6" patternUnits="userSpaceOnUse">
        <circle cx=".8" cy=".8" r=".35" fill="#000" opacity=".35" />
      </pattern>
      <linearGradient id={`${id}gs`} x1="0" y1="0" x2="1" y2="0">
        <stop offset="0" stopColor="#8d8d8d" />
        <stop offset=".5" stopColor="#e4e4e4" />
        <stop offset="1" stopColor="#8d8d8d" />
      </linearGradient>
    </defs>
  );
}

// The ST10's four top buttons.
const BUTTONS = [0, 1, 2, 3];

// A tall body: lighter cap to 29 %, a thin silver band, a fine grille.
export function St10({ fill }: DrawingProps): ReactElement {
  return (
    <>
      <rect x="23" y="8" width="54" height="84" rx="3.5" fill={fill('gb')} />
      <rect x="23" y="8" width="54" height="84" rx="3.5" fill={fill('grille')} />
      <path d="M23 11.5a3.5 3.5 0 0 1 3.5-3.5h47a3.5 3.5 0 0 1 3.5 3.5V32H23Z" fill={fill('gt')} />
      <path d="M38 8h24v2.4a1 1 0 0 1-1 1H39a1 1 0 0 1-1-1Z" fill="#7a7b7e" />
      <g fill="#8a8a8a">
        {BUTTONS.map((i) => (
          <circle key={i} cx={40.25 + i * 6.5} cy={14.5} r={0.8} />
        ))}
      </g>
      <rect x="23" y="32" width="54" height="2.4" fill={fill('gs')} />
    </>
  );
}

const ST20_BODY = 'M8 24q0-3 3-3h78q3 0 3 3l.7 25.5-.7 25.5q0 3-3 3H11q-3 0-3-3L7.3 49.5Z';

// A wide body: silver top cap with the button row, a glossy centre panel.
export function St20({ fill }: DrawingProps): ReactElement {
  return (
    <>
      <path d={ST20_BODY} fill={fill('gb')} />
      <path d={ST20_BODY} fill={fill('grille')} />
      <path d="M11 21h78q3 0 3 3v2.4H8V24q0-3 3-3Z" fill={fill('gs')} />
      <rect x="36" y="22.3" width="28" height="2" rx="1" fill="#2a2a2a" />
      <rect x="43" y="31.5" width="14.5" height="37.5" rx="1.6" fill={fill('gp')} />
      <rect x="44.5" y="65.5" width="11.5" height="2" rx=".6" fill={fill('gs')} />
      <circle cx="50.2" cy="36" r=".7" fill="#cfcfcf" />
    </>
  );
}

const ST300_FRONT = 'M3 51.9h94v9.6q0 1.5-1.5 1.5h-91q-1.5 0-1.5-1.5Z';

// A soundbar seen from the front and a little above: glass top, grille front.
export function St300({ fill }: DrawingProps): ReactElement {
  return (
    <>
      <path d="M9 40h82l6 11H3Z" fill={fill('gp')} />
      <path d="M30 40h40l-18 11H6Z" fill="#fff" opacity=".07" />
      <path d="M3 51h94v.9H3Z" fill="#7a7a7a" />
      <path d={ST300_FRONT} fill={fill('gb')} />
      <path d={ST300_FRONT} fill={fill('grille')} />
    </>
  );
}

const WLA_TOP = 'M26 21h48q9 0 11 8l6 21q1 5-5 5H14q-6 0-5-5l6-21q2-8 11-8Z';

// A low box: the lighter top face narrows to the back, a darker front band.
export function Wla({ fill }: DrawingProps): ReactElement {
  return (
    <>
      <path d="M9 52h82l-.5 17q-.5 10-10 10H19.5q-9.5 0-10-10Z" fill={fill('gf')} />
      <path d={WLA_TOP} fill={fill('gw')} />
      <path d={WLA_TOP} fill="none" stroke="#8a8a8a" strokeOpacity=".55" strokeWidth=".7" />
      <rect x="43" y="36" width="14" height="5" rx="2.5" fill="#2b2b2b" />
      <path d="M14 55h72" stroke="#262626" strokeWidth=".8" />
      <circle cx="50" cy="60.5" r=".8" fill="#e6e6e6" />
      <circle cx="50" cy="69" r=".8" fill="#e6e6e6" />
    </>
  );
}
