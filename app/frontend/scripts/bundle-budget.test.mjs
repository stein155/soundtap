// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { check, measure } from './bundle-budget.mjs';

let dist = '';

// A built page as vite writes it: index.html naming its entry and preloads, and a lazy chunk.
function build(files) {
  dist = mkdtempSync(join(tmpdir(), 'budget-'));
  mkdirSync(join(dist, 'assets'));
  writeFileSync(
    join(dist, 'index.html'),
    '<script type="module" crossorigin src="./assets/index-a.js"></script>' +
      '<link rel="modulepreload" crossorigin href="./assets/react-b.js">'
  );
  for (const [name, size] of Object.entries(files))
    writeFileSync(join(dist, 'assets', name), 'x'.repeat(size));
  return dist;
}

describe('bundle budget', () => {
  afterEach(() => rmSync(dist, { recursive: true, force: true }));

  it('counts only what the first page loads towards its limit', () => {
    const measured = measure(build({ 'index-a.js': 10, 'react-b.js': 20, 'Settings-c.js': 30 }));
    expect(measured.initial.map((c) => c.file).sort()).toEqual(['index-a.js', 'react-b.js']);
    expect(measured.chunks).toHaveLength(3);
  });

  it('passes a build that fits', () => {
    const measured = measure(build({ 'index-a.js': 100, 'react-b.js': 100 }));
    expect(check(measured, { chunk: 1000, initialGzip: 1000 })).toEqual([]);
  });

  it('names a script over the limit, lazy or not', () => {
    const measured = measure(build({ 'index-a.js': 10, 'react-b.js': 10, 'Settings-c.js': 600 }));
    const over = check(measured, { chunk: 500, initialGzip: 100_000 });
    expect(over).toHaveLength(1);
    expect(over[0]).toContain('Settings-c.js');
  });

  it('fails a first page that grew past its gzip limit', () => {
    const measured = measure(build({ 'index-a.js': 10, 'react-b.js': 10 }));
    const over = check(measured, { chunk: 1000, initialGzip: 1 });
    expect(over).toEqual([expect.stringContaining('the first page loads')]);
  });

  it('refuses an index.html that names a script the build does not have', () => {
    expect(() => measure(build({ 'index-a.js': 10 }))).toThrow(/not in/u);
  });
});
