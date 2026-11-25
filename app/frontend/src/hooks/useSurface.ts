// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { createContext, useContext } from 'react';

// Whether a card sits on the page ('raised': it casts a shadow) or inside a card that
// already does ('inset': it is outlined instead). A provider sets 'inset' for everything
// below it, so its fields and cards need no prop for it.
export type Surface = 'raised' | 'inset';

const SurfaceContext = createContext<Surface>('raised');

export const useSurface = (): Surface => useContext(SurfaceContext);

// The shadow of a card on the current surface.
export const cardShadow = (surface: Surface): string =>
  surface === 'inset' ? '0 0 0 1px var(--border)' : 'var(--shadow-sm)';
