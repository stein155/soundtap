// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect } from 'react';

// The tab title, and the label iOS gives a home-screen shortcut ("Keuken", not
// "SoundTap"), follow the selected room.
function showTitle(name: string) {
  document.title = name ? `${name} · SoundTap` : 'SoundTap';
  document
    .querySelector('meta[name="apple-mobile-web-app-title"]')
    ?.setAttribute('content', name || 'SoundTap');
}

export function useDocumentTitle(name: string): void {
  useEffect(() => showTitle(name), [name]);
}
