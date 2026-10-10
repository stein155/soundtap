// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import { configDefaults } from 'vitest/config';
import react from '@vitejs/plugin-react';

// This frontend is built to static assets and embedded into the SoundTap Go
// binary (see internal/web). In production SoundTap serves both this bundle and
// the whole /api/* surface (including the /api/tunein and /api/logo proxies)
// from the same origin, so no proxy rewriting is needed at runtime.
//
// For local `vite dev`, point every /api call at a running SoundTap instance: `make dev`
// sets SOUNDTAP_URL to its soundtapd -dev. The browser's Host header is kept (no
// changeOrigin): soundtapd refuses a write whose Origin names another host than the one it
// was reached under, and accepts localhost (internal/web/guard.go).
const SOUNDTAP_TARGET = process.env.SOUNDTAP_URL || 'http://localhost';
const outDir = process.env.SOUNDTAP_WEB_OUT_DIR || '../internal/web/dist';

export default defineConfig({
  plugins: [react()],
  base: './',
  // Use the automatic JSX runtime everywhere (incl. Vitest's transform) so test
  // files need not import React explicitly.
  esbuild: { jsx: 'automatic' },
  build: {
    // Build straight into the Go embed dir (internal/web/dist) so `go build`
    // always bundles the latest UI.
    outDir,
    emptyOutDir: true,
    assetsDir: 'assets',
    // React and React Aria change only with a dependency bump: kept apart from the app's own
    // code, an app update leaves them in the browser's cache. scripts/bundle-budget.mjs holds
    // every chunk, and what the first page loads, to a size (npm run build).
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            { name: 'react', test: /node_modules[\\/](react|react-dom|scheduler)[\\/]/ },
            {
              name: 'aria',
              test: /node_modules[\\/](@react-aria|@react-stately|@react-types|@internationalized|react-aria|react-aria-components|react-stately|@swc[\\/]helpers|clsx)[\\/]/,
            },
          ],
        },
      },
    },
  },
  server: {
    proxy: {
      '/api': { target: SOUNDTAP_TARGET, changeOrigin: false },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setup.ts',
    // e2e/ runs in Playwright against the demo speaker (make test-e2e), not here.
    exclude: [...configDefaults.exclude, 'e2e/**'],
    environmentOptions: { jsdom: { url: 'http://localhost/' } },
    css: true,
    // The integration tests render the whole app; on a busy CI runner one can pass 15 s.
    testTimeout: 30000,
  },
});
