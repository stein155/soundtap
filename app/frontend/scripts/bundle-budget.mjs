// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Holds the production build to a size: run after `vite build` by `npm run build`, so `make
// web`, `make test-web` and CI fail on a bundle that outgrew it. The page is served by the
// speaker and opened on phones over Wi-Fi, often on the speaker's own setup network.
//
//   node scripts/bundle-budget.mjs [dist]   (default: $SOUNDTAP_WEB_OUT_DIR, as vite.config.js)
//
// A limit leaves headroom over the build it was set on (in the comments), not room to grow
// into: a change that needs more should first move code out of the first page (React.lazy,
// as settings and the search are) or drop a dependency, and raise a limit only after that.
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { gzipSync } from 'node:zlib';
import { fileURLToPath } from 'node:url';

export const LIMITS = {
  // Any one script, minified: React alone is 219 kB, the largest on 2026-11-20.
  chunk: 250_000,
  // The scripts the first page loads (index.html's entry and its preloads), gzipped as a
  // browser receives them: 186 kB on 2026-11-20, from 216 kB before settings, the search
  // and the setup page loaded on demand.
  initialGzip: 210_000,
};

const kB = (bytes) => `${(bytes / 1000).toFixed(1)} kB`;

// Every script in dist/assets, and which of them index.html loads before the page shows.
export function measure(dist) {
  const assets = join(dist, 'assets');
  const chunks = readdirSync(assets)
    .filter((f) => f.endsWith('.js'))
    .map((file) => {
      const body = readFileSync(join(assets, file));
      return { file, size: statSync(join(assets, file)).size, gzip: gzipSync(body).length };
    });
  const html = readFileSync(join(dist, 'index.html'), 'utf8');
  const first = new Set(
    [...html.matchAll(/(?:src|href)="\.?\/?assets\/(?<file>[^"]+\.js)"/gu)].map(
      (m) => m.groups.file
    )
  );
  const initial = chunks.filter((c) => first.has(c.file));
  if (initial.length !== first.size) throw new Error(`index.html names a script not in ${assets}`);
  return { chunks, initial };
}

// What is over its limit, one line each; empty when the build fits.
export function check({ chunks, initial }, limits = LIMITS) {
  const over = chunks
    .filter((c) => c.size > limits.chunk)
    .map((c) => `${c.file} is ${kB(c.size)}, over the ${kB(limits.chunk)} a script may be`);
  const gzip = initial.reduce((sum, c) => sum + c.gzip, 0);
  if (gzip > limits.initialGzip)
    over.push(
      `the first page loads ${kB(gzip)} of script (gzip), over its ${kB(limits.initialGzip)}`
    );
  return over;
}

function main() {
  const dist = resolve(
    process.argv[2] ?? process.env.SOUNDTAP_WEB_OUT_DIR ?? '../internal/web/dist'
  );
  const measured = measure(dist);
  const { chunks, initial } = measured;
  const largest = chunks.reduce((a, b) => (b.size > a.size ? b : a));
  const raw = initial.reduce((sum, c) => sum + c.size, 0);
  const gzip = initial.reduce((sum, c) => sum + c.gzip, 0);
  process.stdout.write(
    `bundle budget: the first page loads ${initial.length} scripts, ${kB(raw)} (${kB(gzip)} gzip, ` +
      `limit ${kB(LIMITS.initialGzip)}); largest script ${largest.file} ${kB(largest.size)} ` +
      `(limit ${kB(LIMITS.chunk)})\n`
  );
  const over = check(measured);
  for (const line of over) console.error(`bundle budget: ${line}`);
  if (over.length) {
    console.error(
      'bundle budget: see app/frontend/scripts/bundle-budget.mjs before raising a limit'
    );
    process.exit(1);
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
