// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import js from '@eslint/js';
import globals from 'globals';
import jsxA11y from 'eslint-plugin-jsx-a11y';
import reactHooks from 'eslint-plugin-react-hooks';
import tseslint from 'typescript-eslint';
import prettier from 'eslint-config-prettier';

export default tseslint.config(
  // schema.d.ts is generated from openapi.json (npm run api-types).
  { ignores: ['dist/', 'node_modules/', '*.config.js', 'src/lib/api/schema.d.ts'] },
  js.configs.recommended,
  ...tseslint.configs.strictTypeChecked,
  ...tseslint.configs.stylisticTypeChecked,
  jsxA11y.flatConfigs.strict,
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      globals: { ...globals.browser },
      parserOptions: { projectService: true, tsconfigRootDir: import.meta.dirname },
    },
    plugins: { 'react-hooks': reactHooks },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // TypeScript's noEmit build already catches unused imports/vars; the lint
      // rule would duplicate that with noisier semantics around types.
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      'no-empty': ['error', { allowEmptyCatch: true }],
      // Bug classes the type checker can see.
      '@typescript-eslint/no-deprecated': 'error',
      // '', 0 and false as "no value" stay deliberate; only null/undefined mix-ups are flagged.
      '@typescript-eslint/prefer-nullish-coalescing': [
        'error',
        { ignorePrimitives: { string: true, number: true, boolean: true } },
      ],
      '@typescript-eslint/switch-exhaustiveness-check': [
        'error',
        { considerDefaultExhaustiveForUnions: false, requireDefaultForNonUnion: true },
      ],
      // `onClick={() => setOpen(true)}`: a one-line arrow that hands back a call's void
      // result is the codebase's callback style; void used anywhere else is still flagged.
      '@typescript-eslint/no-confusing-void-expression': ['error', { ignoreArrowShorthand: true }],
      // Numbers print predictably in a template (`${pct}%`, `/api/presets/${slot}`);
      // undefined, null, objects and arrays are still flagged.
      '@typescript-eslint/restrict-template-expressions': [
        'error',
        {
          allowNumber: true,
          allowAny: false,
          allowArray: false,
          allowBoolean: false,
          allowNever: false,
          allowNullish: false,
          allowRegExp: false,
        },
      ],
      '@typescript-eslint/no-unnecessary-condition': 'error',
      // A React handler may be async when it catches its own failures.
      '@typescript-eslint/no-misused-promises': [
        'error',
        { checksVoidReturn: { attributes: false } },
      ],
      // Beyond the presets: rules that catch real slips at no cost to the idioms above.
      // '' and a missing string both mean "no text": the idiom prefer-nullish-coalescing's
      // ignorePrimitives keeps. Nullable numbers and booleans, objects and any compare explicitly.
      '@typescript-eslint/strict-boolean-expressions': [
        'error',
        {
          allowString: true,
          allowNumber: true,
          allowNullableObject: true,
          allowNullableString: true,
          allowNullableBoolean: false,
          allowNullableNumber: false,
          allowNullableEnum: false,
          allowAny: false,
        },
      ],
      '@typescript-eslint/promise-function-async': 'error',
      '@typescript-eslint/explicit-module-boundary-types': 'error',
      '@typescript-eslint/no-unsafe-type-assertion': 'error',
      'no-nested-ternary': 'error',
      '@typescript-eslint/consistent-type-exports': 'error',
      // Declaration merging gives a library interface a supertype (React's CSSProperties takes
      // custom properties in src/css-vars.d.ts); an interface with members still may not be empty.
      '@typescript-eslint/no-empty-object-type': [
        'error',
        { allowInterfaces: 'with-single-extends' },
      ],
      // vi.mock's importOriginal<typeof import('…')>() is the typed way to extend a mocked module.
      '@typescript-eslint/consistent-type-imports': ['error', { disallowTypeAnnotations: false }],
      // A React lifecycle override (componentDidCatch) implements the base class's contract.
      '@typescript-eslint/class-methods-use-this': ['error', { ignoreOverrideMethods: true }],
      '@typescript-eslint/default-param-last': 'error',
      '@typescript-eslint/method-signature-style': 'error',
      '@typescript-eslint/no-import-type-side-effects': 'error',
      '@typescript-eslint/no-loop-func': 'error',
      '@typescript-eslint/no-shadow': 'error',
      '@typescript-eslint/no-unnecessary-qualifier': 'error',
      '@typescript-eslint/no-useless-empty-export': 'error',
      '@typescript-eslint/prefer-readonly': 'error',
      '@typescript-eslint/require-array-sort-compare': 'error',
      curly: ['error', 'multi-line'],
      // `x == null` is the one loose comparison: null and undefined together.
      eqeqeq: ['error', 'always', { null: 'ignore' }],
      // console.error and console.warn are how a failure reaches the diagnostics.
      'no-console': ['error', { allow: ['error', 'warn'] }],
      'no-else-return': 'error',
      // `!!x` narrows x's type where Boolean(x) does not; +x and '' + x stay flagged.
      'no-implicit-coercion': ['error', { allow: ['!!'] }],
      'no-lonely-if': 'error',
      'no-param-reassign': 'error',
      'no-return-assign': 'error',
      'no-sequences': 'error',
      'no-useless-concat': 'error',
      'no-useless-rename': 'error',
      'no-useless-return': 'error',
      'no-var': 'error',
      'object-shorthand': 'error',
      'prefer-arrow-callback': 'error',
      'prefer-object-spread': 'error',
      'prefer-template': 'error',
      radix: 'error',
      // Size and shape: small, self-explaining units. No file is exempt: split a unit
      // that outgrows them instead of suppressing the rule.
      'max-lines': ['error', { max: 300, skipBlankLines: true, skipComments: true }],
      'max-lines-per-function': ['error', { max: 80, skipBlankLines: true, skipComments: true }],
      complexity: ['error', 15],
      'max-depth': ['error', 4],
      'max-params': ['error', 4],
    },
  },
  { files: ['**/*.{js,mjs}'], ...tseslint.configs.disableTypeChecked },
  {
    // Run by Node: the bundle budget, and the end-to-end tests in the Playwright image.
    files: ['scripts/**/*.mjs', 'e2e/**/*.mjs'],
    languageOptions: { globals: { ...globals.node } },
  },
  {
    files: ['src/lib/i18n/messages/*.ts'],
    rules: { 'max-lines': 'off' },
  },
  {
    // Tests: async mocks without await, vitest's any-typed matchers, and jsdom shims
    // of deprecated browser APIs are the idiom there.
    files: ['**/__tests__/**', '**/*.test.{ts,tsx}', 'src/test/**'],
    rules: {
      'max-lines': 'off',
      'max-lines-per-function': 'off',
      '@typescript-eslint/require-await': 'off',
      '@typescript-eslint/no-unsafe-assignment': 'off',
      '@typescript-eslint/no-base-to-string': 'off',
      '@typescript-eslint/no-deprecated': 'off',
      // jsdom keeps Location's members on the instance, so a test's stand-in location
      // spreads the real one and overrides what it watches.
      '@typescript-eslint/no-misused-spread': [
        'error',
        { allow: [{ from: 'lib', name: 'Location' }] },
      ],
    },
  },
  // Must come last: turns off stylistic rules that would fight Prettier.
  prettier
);
