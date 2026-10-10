// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect } from 'react';

// Tells the settings sheet whether this page holds unsaved edits, so leaving it can ask
// first. Leaving the page (unmounting) counts as clean.
export function useReportDirty(dirty: boolean, onDirty?: (dirty: boolean) => void) {
  useEffect(() => {
    onDirty?.(dirty);
  }, [dirty, onDirty]);
  useEffect(() => () => onDirty?.(false), [onDirty]);
}
