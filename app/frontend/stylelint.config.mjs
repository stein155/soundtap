// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Mistakes only (unknown properties, duplicates, invalid values); Prettier owns the layout.
// Specificity order is off: the sheets go per component, not by selector weight.
export default {
  extends: ['stylelint-config-recommended'],
  rules: { 'no-descending-specificity': null },
};
