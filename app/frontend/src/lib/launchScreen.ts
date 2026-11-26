// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// index.html's launch screen (#launch): the mark and wordmark over the page while the
// scripts load. The app calls this once it has committed its first render; the screen
// fades out after the frame with the app in it is painted, so nothing blank shows between.
export const LAUNCH_FADE_MS = 240;

export function dismissLaunchScreen(doc: Document = document): void {
  const launch = doc.getElementById('launch');
  if (!launch || launch.hasAttribute('data-leaving')) return;
  launch.setAttribute('data-leaving', '');
  const win = doc.defaultView ?? window;
  if (win.matchMedia('(prefers-reduced-motion: reduce)').matches) {
    launch.remove();
    return;
  }
  // Two frames: the first one paints the app under the launch screen, the second fades it.
  win.requestAnimationFrame(() => {
    win.requestAnimationFrame(() => {
      launch.classList.add('launch-out');
      const gone = () => launch.remove();
      launch.addEventListener('transitionend', gone, { once: true });
      // No transitionend when the page is hidden or the fade is skipped.
      win.setTimeout(gone, LAUNCH_FADE_MS + 100);
    });
  });
}
