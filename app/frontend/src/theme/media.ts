// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Layout breakpoints. Media queries cannot read CSS custom properties, so they live here.
//
// desktop: a screen with room for the wide column. Width alone would also catch a phone held
//          sideways (844×390).
export const desktop = '(min-width: 720px) and (min-height: 600px)';
