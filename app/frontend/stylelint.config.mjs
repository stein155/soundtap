// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Every rule on: stylelint-config-recommended for mistakes, the rules of
// stylelint-config-standard spelled out (it is not a dependency), and stricter limits on
// top. Prettier owns the layout; these own what the sheets say and how they say it.
const kebab = '^([a-z][a-z0-9]*)(-[a-z0-9]+)*$';

export default {
  extends: ['stylelint-config-recommended'],
  rules: {
    // Specificity order is off: the sheets go per component, not by selector weight.
    'no-descending-specificity': null,

    // Notation, as stylelint-config-standard has it.
    'alpha-value-notation': [
      'percentage',
      {
        exceptProperties: [
          'opacity',
          'fill-opacity',
          'flood-opacity',
          'stop-opacity',
          'stroke-opacity',
        ],
      },
    ],
    'color-function-notation': 'modern',
    'color-hex-length': 'short',
    'font-family-name-quotes': 'always-where-recommended',
    'function-name-case': 'lower',
    'function-url-quotes': 'always',
    'hue-degree-notation': 'angle',
    'import-notation': 'string',
    'keyframe-selector-notation': 'percentage-unless-within-keyword-only-block',
    'length-zero-no-unit': true,
    'lightness-notation': 'percentage',
    // Range syntax (width <= 380px) needs Safari 16.4; an older iPhone would drop the query.
    'media-feature-range-notation': 'prefix',
    'number-max-precision': 4,
    'selector-attribute-quotes': 'always',
    'selector-not-notation': 'complex',
    'selector-pseudo-element-colon-notation': 'double',
    'selector-type-case': 'lower',
    'value-keyword-case': 'lower',

    // Names: kebab-case classes, custom properties and keyframes.
    'custom-property-pattern': [kebab, { message: 'Expected a kebab-case custom property' }],
    'keyframes-name-pattern': [kebab, { message: 'Expected kebab-case keyframes' }],
    'selector-class-pattern': [kebab, { message: 'Expected a kebab-case class' }],

    // No redundancy.
    // overflow: x y needs Safari 15; the two longhands work everywhere.
    'declaration-block-no-redundant-longhand-properties': [
      true,
      { ignoreShorthands: ['overflow'] },
    ],
    'shorthand-property-no-redundant-values': true,

    // No vendor prefixes where the standard property does the job.
    'at-rule-no-vendor-prefix': true,
    'media-feature-name-no-vendor-prefix': true,
    'property-no-vendor-prefix': true,
    'selector-no-vendor-prefix': true,
    'value-no-vendor-prefix': true,

    // Stricter than standard: colours are tokens or values, specificity stays flat and
    // !important needs a reason at the line that uses it.
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
