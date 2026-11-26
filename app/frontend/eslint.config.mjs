// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { fixupPluginRules } from '@eslint/compat';
import js from '@eslint/js';
import globals from 'globals';
import jsxA11y from 'eslint-plugin-jsx-a11y';
import react from 'eslint-plugin-react';
import reactHooks from 'eslint-plugin-react-hooks';
import tseslint from 'typescript-eslint';
import prettier from 'eslint-config-prettier';

// Every rule on: ESLint's, typescript-eslint's, eslint-plugin-react's and react-hooks' full
// sets and jsx-a11y's strict one. A rule is configured to the project's idioms below rather
// than turned off; the few that are off say why, as app/.golangci.yml and ruff.toml do.
const allReactHooks = Object.fromEntries(
  Object.keys(reactHooks.rules).map((rule) => [`react-hooks/${rule}`, 'error'])
);

export default tseslint.config(
  // schema.d.ts is generated from openapi.json (npm run api-types).
  { ignores: ['dist/', 'node_modules/', '*.config.js', 'src/lib/api/schema.d.ts'] },
  js.configs.all,
  ...tseslint.configs.all,
  {
    ...react.configs.flat.all,
    // eslint-plugin-react 7 still calls the context methods ESLint 10 removed; ESLint's own
    // compatibility layer puts them back.
    plugins: { react: fixupPluginRules(react) },
    rules: { ...react.configs.flat.all.rules, ...react.configs.flat['jsx-runtime'].rules },
    settings: { react: { version: 'detect' } },
  },
  jsxA11y.flatConfigs.strict,
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      globals: { ...globals.browser },
      parserOptions: { projectService: true, tsconfigRootDir: import.meta.dirname },
    },
  },
  {
    rules: {
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      'no-empty': ['error', { allowEmptyCatch: true }],
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
      // A React handler may be async when it catches its own failures.
      '@typescript-eslint/no-misused-promises': [
        'error',
        { checksVoidReturn: { attributes: false } },
      ],
      // '' and a missing string both mean "no text": the idiom prefer-nullish-coalescing's
      // ignorePrimitives keeps. Numbers, nullable booleans, objects and any compare explicitly:
      // `{count && <X />}` would render a 0.
      '@typescript-eslint/strict-boolean-expressions': [
        'error',
        {
          allowString: true,
          allowNumber: false,
          allowNullableObject: true,
          allowNullableString: true,
          allowNullableBoolean: false,
          allowNullableNumber: false,
          allowNullableEnum: false,
          allowAny: false,
        },
      ],
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
      curly: ['error', 'multi-line'],
      // `x == null` is the one loose comparison: null and undefined together.
      eqeqeq: ['error', 'always', { null: 'ignore' }],
      // console.error and console.warn are how a failure reaches the diagnostics.
      'no-console': ['error', { allow: ['error', 'warn'] }],
      // `!!x` narrows x's type where Boolean(x) does not; +x and '' + x stay flagged.
      'no-implicit-coercion': ['error', { allow: ['!!'] }],
      // Size and shape: small, self-explaining units. No file is exempt: split a unit
      // that outgrows them instead of suppressing the rule.
      'max-lines': ['error', { max: 300, skipBlankLines: true, skipComments: true }],
      'max-lines-per-function': ['error', { max: 80, skipBlankLines: true, skipComments: true }],
      'max-statements': ['error', 25],
      complexity: ['error', 15],
      'max-depth': ['error', 4],
      '@typescript-eslint/max-params': ['error', { max: 4 }],

      // Configured to the project's idioms.
      // Components and styled components are PascalCase values; constants UPPER_CASE; object
      // keys mirror the HTTP API (snake_case) and HTTP headers, so properties are not judged.
      '@typescript-eslint/naming-convention': [
        'error',
        { selector: 'default', format: ['camelCase'], leadingUnderscore: 'allow' },
        { selector: 'import', format: null },
        {
          selector: 'variable',
          format: ['camelCase', 'PascalCase', 'UPPER_CASE'],
          leadingUnderscore: 'allow',
        },
        { selector: 'function', format: ['camelCase', 'PascalCase'] },
        { selector: 'parameter', format: ['camelCase', 'PascalCase'], leadingUnderscore: 'allow' },
        { selector: 'typeLike', format: ['PascalCase'] },
        { selector: 'enumMember', format: ['PascalCase', 'UPPER_CASE'] },
        { selector: ['property', 'method', 'accessor'], format: null },
      ],
      // `void promise` marks a promise that is deliberately not awaited (no-floating-promises).
      'no-void': ['error', { allowAsStatement: true }],
      // `for (let i = 0; i < n; i++)` stays; ++ anywhere else is flagged.
      'no-plusplus': ['error', { allowForLoopAfterthoughts: true }],
      // `hash | 0` keeps a string hash in 32 bits; every other bit operator is flagged.
      'no-bitwise': ['error', { int32Hint: true }],
      // State is a class field, as TypeScript writes it.
      'react/state-in-constructor': ['error', 'never'],
      // A value import and an `import type` from one module are two statements on purpose
      // (consistent-type-imports).
      'no-duplicate-imports': ['error', { allowSeparateTypeImports: true }],
      // `list[0]` keeps its `| undefined` (noUncheckedIndexedAccess); destructuring a DOM list's
      // iterator (`const [t] = e.touches`) would drop it. Objects destructure.
      '@typescript-eslint/prefer-destructuring': ['error', { array: false, object: true }],
      // A function declaration is hoisted, so a module reads top-down: the export first, its
      // helpers below. Variables and classes, which are not, still have to come first.
      '@typescript-eslint/no-use-before-define': ['error', { functions: false }],
      // Icon.check: the icon set is one namespace of lower-case glyph names.
      'react/jsx-pascal-case': ['error', { allowNamespace: true }],
      // TypeScript JSX lives in .tsx files.
      'react/jsx-filename-extension': ['error', { extensions: ['.tsx'], allow: 'as-needed' }],
      // A render prop (`trigger={(ask) => <Button onClick={ask} />}`) is a function the
      // child calls, not a component type.
      'react/no-unstable-nested-components': ['error', { allowAsProps: true }],
      // Brand names, units and punctuation are the same in every language; any other text
      // goes through t().
      'react/jsx-no-literals': [
        'error',
        {
          allowedStrings: ['AUX', 'Spotify', 'TuneIn', '·', '“', '”', '+', 'ms', '0 ms'],
        },
      ],

      // Off, each for the reason given.
      // Superseded by a TypeScript-aware rule that is on, or contradicting one.
      camelcase: 'off', // @typescript-eslint/naming-convention
      'max-params': 'off', // @typescript-eslint/max-params
      'no-eq-null': 'off', // contradicts eqeqeq's null: 'ignore' (x == null is the one loose compare)
      'react/prop-types': 'off', // TypeScript checks props at compile time
      'default-case': 'off', // @typescript-eslint/switch-exhaustiveness-check, which sees the union's cases
      'react/sort-comp': 'off', // @typescript-eslint/member-ordering, which orders the other way
      // React 19 dropped defaultProps for function components; an optional prop is undefined.
      'react/require-default-props': 'off',
      // strict-boolean-expressions above sees the types, so it catches the leaked 0 without
      // flagging every boolean &&.
      'react/jsx-no-leaked-render': 'off',
      // The exported surface is typed by explicit-module-boundary-types; inner callbacks infer.
      '@typescript-eslint/explicit-function-return-type': 'off',
      // React's and the DOM's own types (events, refs, ReactNode) are mutable library types;
      // the rule cannot hold without wrapping every parameter in a deep-readonly type.
      '@typescript-eslint/prefer-readonly-parameter-types': 'off',
      // undefined is TypeScript's "no value" (optional props and fields, `?:`).
      'no-undefined': 'off',
      // Inline arrows are React's documented handler pattern; nothing here memoizes children.
      'react/jsx-no-bind': 'off',
      // Pure taste the codebase does the other way, with no bug-finding value (each would
      // rewrite most files).
      'sort-keys': 'off', // keys follow meaning: the API's shape, CSS order
      'sort-imports': 'off', // imports go library first, then local, by meaning
      'react/jsx-sort-props': 'off', // props go by meaning, not alphabet
      'one-var': 'off', // one declaration per statement
      'capitalized-comments': 'off', // comments quote identifiers and code
      'id-length': 'off', // e, i, r and t in small callbacks
      'no-ternary': 'off', // the ternary is JSX's conditional
      'func-style': 'off', // components are declarations, small helpers arrow constants
      'no-inline-comments': 'off', // a trailing comment explains a value at its line
      '@typescript-eslint/no-magic-numbers': 'off', // px, ms and API constants read best in place
      'react/jsx-max-depth': 'off', // depth follows the design's markup
      'react/no-multi-comp': 'off', // small private sub-components live beside their parent
      'react/jsx-props-no-spreading': 'off', // wrappers forward react-aria's props
      'react/forbid-component-props': 'off', // className and style (CSS variables) on styled and react-aria components
      // `[channel, setOwn]`: the obvious setter name is often the API call it mirrors
      // (setChannel, setVolume), and a first-render snapshot keeps no setter at all.
      'react/hook-use-state': 'off',
      'react/jsx-handler-names': 'off', // handlers come from hooks as verbs (tunein.retry, volume.setGroupVolume)
      // A guard clause at the top of a loop keeps its body flat (max-depth).
      'no-continue': 'off',
    },
  },
  {
    // React code; e2e/ and scripts/ run in Node (Playwright's fixture `use` is no hook).
    files: ['src/**/*.{ts,tsx}'],
    plugins: { 'react-hooks': reactHooks },
    rules: {
      ...allReactHooks,
      // Reports React Compiler's own unimplemented features (try/finally), not code faults;
      // the build does not run the compiler.
      'react-hooks/todo': 'off',
    },
  },
  { files: ['**/*.{js,mjs}'], ...tseslint.configs.disableTypeChecked },
  {
    // Plain JavaScript has no type annotations to write.
    files: ['**/*.{js,mjs}'],
    rules: { '@typescript-eslint/explicit-module-boundary-types': 'off' },
  },
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
    files: ['**/__tests__/**', '**/*.test.{ts,tsx}', 'src/test/**', 'src/hooks/testFixtures.ts'],
    rules: {
      // A describe block holds one statement per case.
      'max-lines': 'off',
      'max-lines-per-function': 'off',
      'max-statements': 'off',
      '@typescript-eslint/require-await': 'off',
      '@typescript-eslint/no-unsafe-assignment': 'off',
      '@typescript-eslint/no-base-to-string': 'off',
      '@typescript-eslint/no-deprecated': 'off',
      // vi.fn() returns a value where the component expects a void callback.
      '@typescript-eslint/strict-void-return': 'off',
      // A test renders throwaway markup with its own text; the app's text is still checked.
      'react/jsx-no-literals': 'off',
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
