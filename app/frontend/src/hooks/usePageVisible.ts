// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useSyncExternalStore } from 'react';

const subscribe = (onChange: () => void) => {
  document.addEventListener('visibilitychange', onChange);
  return () => document.removeEventListener('visibilitychange', onChange);
};
const visible = () => document.visibilityState !== 'hidden';

// Whether the page is on screen. Pollers and the live stream stop while it is not (a
// background tab, a phone with the screen off), and catch up the moment it is.
export function usePageVisible() {
  return useSyncExternalStore(subscribe, visible, () => true);
}
