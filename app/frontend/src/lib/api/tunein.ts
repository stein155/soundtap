// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { apiFetch, apiURL } from '../base';
import { deadline, text, SLOW_READ_TIMEOUT_MS } from './http';
import type { Station, BrowseCategory, BrowseResult } from '../types';

const MAX_SEARCH_RESULTS = 30;

// TuneIn OPML search via the same-origin /api/tunein proxy. The original walked
// a nested OPML body, which the same-origin /api/tunein proxy passes through. null when
// TuneIn could not be reached.
export async function searchTuneIn(query: string): Promise<Station[] | null> {
  try {
    const r = await apiFetch(
      apiURL(
        `/api/tunein/Search.ashx?query=${encodeURIComponent(query)}&types=station&render=json`
      ),
      { signal: deadline(SLOW_READ_TIMEOUT_MS) }
    );
    if (!r.ok) throw new Error(`search -> ${r.status}`);
    const data = (await r.json()) as { body?: unknown };
    const results: Station[] = [];
    const walk = (items: unknown) => {
      if (!Array.isArray(items)) return;
      for (const item of items as Record<string, unknown>[]) {
        const station = itemToStation(item);
        if (station) results.push(station);
        if (item.children) walk(item.children);
      }
    };
    walk(data.body);
    return results.slice(0, MAX_SEARCH_RESULTS);
  } catch {
    return null;
  }
}

// itemToStation maps a TuneIn OPML "audio" outline to a Station, or null when it
// isn't a real station (no s-prefixed guide id).
function itemToStation(item: Record<string, unknown>): Station | null {
  const isStation =
    (item.type === 'audio' || item.item === 'station') && text(item.guide_id).startsWith('s');
  if (!isStation) return null;
  return {
    tuneInId: item.guide_id as string,
    name: (item.text as string) || (item.name as string) || '',
    tagline: (item.subtext as string) || '',
    genre: '',
    country: text(item.locale).split('-')[1] || '',
    logo: (item.image as string) || '',
  };
}

// browseTuneIn walks one level of TuneIn's Browse.ashx directory. path is a
// proxy-relative path (defaults to the root browse); it returns the drill-down
// categories and any stations at this level; null when TuneIn could not be reached.
export async function browseTuneIn(
  path = '/Browse.ashx?render=json',
  locale?: string
): Promise<BrowseResult | null> {
  const categories: BrowseCategory[] = [];
  const stations: Station[] = [];
  let more: BrowseCategory | undefined;
  try {
    // Force the caller's locale onto every request (not just the root): a child
    // URL that somehow lost it still comes back translated, so labels never
    // revert to English mid-drill.
    let full = path;
    if (locale) {
      const u = new URL(path, 'http://x');
      u.searchParams.set('locale', locale);
      u.searchParams.set('render', 'json');
      full = u.pathname + u.search;
    }
    const r = await apiFetch(apiURL(`/api/tunein${full}`), {
      signal: deadline(SLOW_READ_TIMEOUT_MS),
    });
    if (!r.ok) throw new Error(`browse -> ${r.status}`);
    const data = (await r.json()) as { body?: unknown };
    const walk = (items: unknown) => {
      if (!Array.isArray(items)) return;
      for (const item of items as Record<string, unknown>[]) {
        const station = itemToStation(item);
        if (station) {
          stations.push(station);
          continue;
        }
        // A "link" outline with a URL is a drill-down category (genre/region).
        if (item.type === 'link' && typeof item.URL === 'string' && item.text) {
          // Shows, podcasts and episodes are not radio stations this speaker can play:
          // listed as folders they only led to "no stations found".
          if (isShowLink(item)) continue;
          const p = browsePath(item.URL);
          if (p) {
            const category = { title: item.text as string, path: p };
            if (item.key === 'nextStations') more = category;
            else categories.push(category);
            continue;
          }
        }
        // A grouping outline (e.g. "Stations") nests its entries as children.
        if (item.children) walk(item.children);
      }
    };
    walk(data.body);
  } catch {
    return null;
  }
  return { categories, stations, more };
}

function isShowLink(item: Record<string, unknown>): boolean {
  const url = text(item.URL);
  return (
    item.item === 'show' ||
    item.key === 'nextShows' ||
    /^[pt]\d/.test(text(item.guide_id)) ||
    /Tune\.ashx|[?&]c=(pbrowse|podcast)\b/i.test(url)
  );
}

// browsePath turns an absolute TuneIn Browse URL into a proxy-relative path with
// render=json forced, so it goes back through /api/tunein. Empty on a bad URL.
function browsePath(url: string): string {
  try {
    const u = new URL(url);
    u.searchParams.set('render', 'json');
    // TuneIn's "By Language" node links its Music/Talk/Sports children to dead
    // category ids (id=c424724/5/6) that return "No stations available", while the
    // equivalent c=<category> browse with the same &filter=l<lang> is populated.
    // Rewrite so By Language actually reaches stations.
    // Hardcoded TuneIn category ids — if TuneIn renumbers them the
    // branch dead-ends again (shows the empty state); revisit if that happens.
    const DEAD_CATEGORY: Record<string, string> = {
      c424724: 'music',
      c424725: 'talk',
      c424726: 'sports',
    };
    const id = u.searchParams.get('id');
    if (id && DEAD_CATEGORY[id] && u.searchParams.has('filter')) {
      u.searchParams.delete('id');
      u.searchParams.set('c', DEAD_CATEGORY[id]);
    }
    return u.pathname + u.search;
  } catch {
    return '';
  }
}
