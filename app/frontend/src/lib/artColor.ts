// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// The main colour of a picture, for the wash over the blurred artwork on the now-playing
// screen, as Stockholm has. Null when the picture may not be read: a server that sends no
// CORS header taints the canvas.
const SIZE = 16;
// Stockholm washes with the colour as it is; on pale artwork that left white text
// unreadable, so no channel goes above this.
const MAX_CHANNEL = 110;

export type RGB = [number, number, number];

// The commonest colour of RGBA pixels, in coarse buckets. Near-black and near-white
// count only when nothing else is there: they say little about a picture.
export function mainColor(rgba: ArrayLike<number>): RGB | null {
  const buckets = new Map<number, { n: number; sum: RGB }>();
  let fallback: RGB | null = null;
  for (let i = 0; i + 3 < rgba.length; i += 4) {
    const rgb = opaquePixel(rgba, i);
    if (!rgb) continue;
    fallback ??= rgb;
    const high = Math.max(...rgb);
    if (high < 24 || Math.min(...rgb) > 232) continue;
    const key = (rgb[0] >> 5) * 64 + (rgb[1] >> 5) * 8 + (rgb[2] >> 5);
    const bucket = buckets.get(key) ?? { n: 0, sum: [0, 0, 0] };
    bucket.n++;
    bucket.sum = [bucket.sum[0] + rgb[0], bucket.sum[1] + rgb[1], bucket.sum[2] + rgb[2]];
    buckets.set(key, bucket);
  }
  let best: { n: number; sum: RGB } | null = null;
  for (const bucket of buckets.values()) if (!best || bucket.n > best.n) best = bucket;
  if (!best) return fallback && limit(fallback);
  const { n, sum } = best;
  return limit([sum[0] / n, sum[1] / n, sum[2] / n]);
}

// The colour of the pixel at i, or null when it is mostly transparent. The caller's loop
// bound keeps i..i+3 inside rgba: the fallbacks never apply.
function opaquePixel(rgba: ArrayLike<number>, i: number): RGB | null {
  if ((rgba[i + 3] ?? 0) < 128) return null;
  return [rgba[i] ?? 0, rgba[i + 1] ?? 0, rgba[i + 2] ?? 0];
}

function limit(rgb: RGB): RGB {
  const scale = Math.min(1, MAX_CHANNEL / Math.max(1, ...rgb));
  return [Math.round(rgb[0] * scale), Math.round(rgb[1] * scale), Math.round(rgb[2] * scale)];
}

export async function artColor(src: string): Promise<RGB | null> {
  return new Promise((resolve) => {
    const img = new Image();
    img.crossOrigin = 'anonymous';
    img.onerror = () => resolve(null);
    img.onload = () => {
      try {
        const canvas = document.createElement('canvas');
        canvas.width = canvas.height = SIZE;
        const ctx = canvas.getContext('2d');
        ctx?.drawImage(img, 0, 0, SIZE, SIZE);
        resolve(ctx ? mainColor(ctx.getImageData(0, 0, SIZE, SIZE).data) : null);
      } catch {
        resolve(null);
      }
    };
    img.src = src;
  });
}
