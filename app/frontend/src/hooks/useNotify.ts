// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { createContext, useContext } from 'react';
import type { Notify } from './useErrorReporter';

// The page's toast, for parts deep in a page (Settings) that report a failure without
// a place of their own for it. Outside a page that provides it, nothing is shown.
export const NotifyContext = createContext<Notify | null>(null);

// The toast when there is one, else null: the caller then shows the text itself.
export function useNotify(): Notify | null {
  return useContext(NotifyContext);
}
