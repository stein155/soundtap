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
  return (p) => (
    <svg {...svg} {...p}>
      {body}
    </svg>
  );
}

export const Icon = Object.fromEntries(
  Object.entries(specs).map(([name, spec]) => [name, draw(spec)])
) as Record<IconName, IconComponent>;
