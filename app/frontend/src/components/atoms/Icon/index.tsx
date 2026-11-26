// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { devicesIcons } from './devices';
import { navigationIcons } from './navigation';
import { playbackIcons } from './playback';
import { settingsIcons } from './settings';
import { sourcesIcons } from './sources';
import type { IconSpec, SvgAttrs } from './svg';

// Inline line icons from the Radio v3 design (1.6px stroke, currentColor), plus the few
// the settings pages need that the design never drew (lock, display). The drawings
// live per group as data; this file only turns them into components.
const specs = {
  ...playbackIcons,
  ...navigationIcons,
  ...devicesIcons,
  ...sourcesIcons,
  ...settingsIcons,
};

export type IconName = keyof typeof specs;

type IconComponent = (p: SvgAttrs) => ReactElement;

function draw({ svg, body }: IconSpec): IconComponent {
  return function Icon(p) {
    return (
      <svg {...svg} {...p}>
        {body}
      </svg>
    );
  };
}

// Whether every icon of the set was drawn.
function drewAll<K extends string>(
  all: Record<K, IconSpec>,
  drawn: Partial<Record<K, IconComponent>>
): drawn is Record<K, IconComponent> {
  return Object.keys(all).every((name) => name in drawn);
}

// Every spec of a set as a component, under the same names.
function drawAll<K extends string>(all: Record<K, IconSpec>): Record<K, IconComponent> {
  const drawn: Partial<Record<K, IconComponent>> = {};
  for (const name in all) if (Object.hasOwn(all, name)) drawn[name] = draw(all[name]);
  if (!drewAll(all, drawn)) throw new Error('an icon was not drawn');
  return drawn;
}

export const Icon = drawAll(specs);
