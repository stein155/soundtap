// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { apiFetch, apiURL } from '../base';
import { deadline, text, SLOW_READ_TIMEOUT_MS } from './http';
import type { Station, BrowseCategory, BrowseResult } from '../types';

const MAX_SEARCH_RESULTS = 30;

// One OPML outline as TuneIn sends it: JSON, each field checked before it is used.
interface Outline {
  type?: unknown;
  item?: unknown;
  key?: unknown;
  guide_id?: unknown;
  text?: unknown;
  name?: unknown;
  subtext?: unknown;
  locale?: unknown;
  image?: unknown;
  URL?: unknown;
  children?: unknown;
}

// outlines reads a list of outlines; anything that is not an object is no outline.
const outlines = (items: unknown): Outline[] =>
  Array.isArray(items)
    ? items.filter((item: unknown): item is Outline => typeof item === 'object' && item !== null)
    : [];

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
    const body = await opmlBody(r);
    const results: Station[] = [];
    const walk = (items: unknown) => {
      for (const item of outlines(items)) {
        const station = itemToStation(item);
        if (station) results.push(station);
        walk(item.children); // nothing to walk unless it is a list
      }
    };
    walk(body);
    return results.slice(0, MAX_SEARCH_RESULTS);
  } catch {
    return null;
  }
}

// The outlines of an OPML answer. A null answer fails, as reading its body always did.
async function opmlBody(r: Response): Promise<unknown> {
  const json: unknown = await r.json();
  if (json === null) throw new Error('TuneIn answered null');
  const fields: { body?: unknown } = typeof json === 'object' ? json : {};
  return fields.body;
}

// itemToStation maps a TuneIn OPML "audio" outline to a Station, or null when it
// isn't a real station (no s-prefixed guide id).
function itemToStation(item: Outline): Station | null {
  const guideId = text(item.guide_id);
  const isStation = (item.type === 'audio' || item.item === 'station') && guideId.startsWith('s');
  if (!isStation) return null;
  return {
    tuneInId: guideId,
    name: text(item.text) || text(item.name),
    tagline: text(item.subtext),
    genre: '',
    country: text(item.locale).split('-')[1] ?? '',
    logo: text(item.image),
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
    const body = await opmlBody(r);
    const walk = (items: unknown) => {
      for (const item of outlines(items)) {
        const station = itemToStation(item);
        if (station) {
          stations.push(station);
          continue;
        }
        // A "link" outline with a URL is a drill-down category (genre/region).
        const title = text(item.text);
        if (item.type === 'link' && typeof item.URL === 'string' && title) {
          // Shows, podcasts and episodes are not radio stations this speaker can play:
          // listed as folders they only led to "no stations found".
          if (isShowLink(item)) continue;
          const p = browsePath(item.URL);
          if (p) {
            const category = { title, path: p };
            if (item.key === 'nextStations') more = category;
            else categories.push(category);
            continue;
          }
        }
        // A grouping outline (e.g. "Stations") nests its entries as children.
        walk(item.children); // nothing to walk unless it is a list
      }
    };
    walk(body);
  } catch {
    return null;
  }
  return { categories, stations, more };
}

function isShowLink(item: Outline): boolean {
  const url = text(item.URL);
  return (
    item.item === 'show' ||
    item.key === 'nextShows' ||
    /^[pt]\d/u.test(text(item.guide_id)) ||
    /Tune\.ashx|[?&]c=(?:pbrowse|podcast)\b/iu.test(url)
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
    const category = id ? DEAD_CATEGORY[id] : undefined;
    if (category && u.searchParams.has('filter')) {
      u.searchParams.delete('id');
      u.searchParams.set('c', category);
    }
    return u.pathname + u.search;
  } catch {
    return '';
  }
}
