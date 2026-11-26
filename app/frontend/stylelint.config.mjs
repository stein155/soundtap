// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Every rule on: stylelint-config-standard (which extends stylelint-config-recommended) for
// mistakes and notation, and stricter limits on top. Prettier owns the layout; these own
// what the sheets say and how they say it. A rule set apart from standard says why.
export default {
  extends: ['stylelint-config-standard'],
  rules: {
    // Specificity order is off: the sheets go per component, not by selector weight.
    'no-descending-specificity': null,

    // Where the project differs from standard.
    // The two SPDX header lines stand together, a comment under a comment.
    'comment-empty-line-before': [
      'always',
      { except: ['first-nested'], ignore: ['stylelint-commands', 'after-comment'] },
    ],
    // Range syntax (width <= 380px) needs Safari 16.4; an older iPhone would drop the query.
    'media-feature-range-notation': 'prefix',
    // overflow: x y needs Safari 15; the two longhands work everywhere.
    'declaration-block-no-redundant-longhand-properties': [
      true,
      { ignoreShorthands: ['overflow'] },
    ],

    // Stricter than standard: a zero takes no unit in a custom property either, no
    // -webkit-box, colours are tokens or values, specificity stays flat and !important
    // needs a reason at the line that uses it.
    'length-zero-no-unit': true,
    'value-no-vendor-prefix': true,
    'color-named': 'never',
    'declaration-no-important': true,
    'font-weight-notation': 'numeric',
    'max-nesting-depth': 2,
    'selector-max-compound-selectors': 4,
    'selector-max-id': 0,
    'selector-max-universal': 1,
    'time-min-milliseconds': 100,
  },
};
