// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useState, type Dispatch, type SetStateAction } from 'react';
import { browseTuneIn, searchTuneIn } from '../lib/api';
import { tuneInLocale } from '../lib/i18n';
import type { BrowseCategory, BrowseResult, Station } from '../lib/types';

const SEARCH_DEBOUNCE_MS = 350;
const ROOT = '';

// null stations / result: TuneIn could not be reached for that query or level.
interface Found {
  query: string;
  stations: Station[] | null;
}
// key is lang|path, so an answer for another language never stands in for this one.
type Level = { key: string; result: BrowseResult | null } | null;

export interface TuneInBrowser {
  query: string;
  setQuery: Dispatch<SetStateAction<string>>;
  results: Station[];
  searching: boolean;
  trail: BrowseCategory[];
  level: BrowseResult;
  browsing: boolean;
  failed: boolean;
  open: (category: BrowseCategory) => void;
  canGoUp: boolean;
  goUp: () => void;
  retry: () => void;
}

// What the search shows for q. While a changed query is on its way the last answer stays
// on screen, so each keystroke does not blank the list for a spinner; a spinner only
// shows before the first answer.
function searchView(q: string, found: Found) {
  const searched = found.query === q;
  const keeping = !!q && !searched && (found.stations?.length ?? 0) > 0;
  return {
    results: ((searched || keeping) && found.stations) || [],
    searching: !!q && !searched && !keeping,
    failed: searched && found.stations === null,
  };
}

// What the directory shows at path: the level already answered in this opening, else the
// last answer when it is for path.
function browseView(key: string, known: BrowseResult | undefined, level: Level) {
  const browsed = !!known || level?.key === key;
  return {
    level: known ?? ((browsed && level?.result) || { categories: [], stations: [] }),
    browsing: !browsed,
    failed: !known && browsed && level?.result === null,
  };
}

// TuneIn for the station picker: live search while there is a query, and a walk
// through TuneIn's directory (genres, regions, ...) while there is not. Each answer is
// kept with what it answers, so "loading" is simply an answer for something else, and a
// slow response for a level the user already left is never shown.
// `active`: the TuneIn tab is showing (it fetches). `opened`: the sheet is open; only a new
// opening resets the search, so a trip to the Spotify tab and back keeps it.
export function useTuneInBrowser(active: boolean, lang: string, opened = active): TuneInBrowser {
  const [query, setQuery] = useState('');
  const [trail, setTrail] = useState<BrowseCategory[]>([]);
  const [found, setFound] = useState<Found>({
    query: '',
    stations: [],
  });
  const [level, setLevel] = useState<Level>(null);
  // Bumped by "try again": asks TuneIn once more for what is on screen.
  const [attempt, setAttempt] = useState(0);
  // Every level answered in this opening, so going back shows it at once.
  const [levels, setLevels] = useState<Partial<Record<string, BrowseResult>>>({});

  // Every opening starts at the top with an empty search box.
  const [wasOpened, setWasOpened] = useState(opened);
  if (opened !== wasOpened) {
    setWasOpened(opened);
    if (opened) {
      setQuery('');
      setFound({ query: '', stations: [] });
      setTrail([]);
      setLevels({});
    }
  }

  const path = trail[trail.length - 1]?.path ?? ROOT;
  const levelKey = `${lang}|${path}`;
  const known = levels[levelKey];
  useEffect(() => {
    if (!active || known) return;
    let current = true;
    void browseTuneIn(path || undefined, tuneInLocale(lang)).then((result) => {
      if (!current) return;
      setLevel({ key: levelKey, result });
      if (result) setLevels((all) => ({ ...all, [levelKey]: result }));
    });
    return () => {
      current = false;
    };
  }, [active, path, lang, levelKey, known, attempt]);

  const q = query.trim();
  useEffect(() => {
    if (!q) return;
    let current = true;
    const id = setTimeout(async () => {
      const stations = await searchTuneIn(q);
      if (current) setFound({ query: q, stations });
    }, SEARCH_DEBOUNCE_MS);
    return () => {
      current = false;
      clearTimeout(id);
    };
  }, [q, attempt]);

  const search = searchView(q, found);
  const browse = browseView(levelKey, known, level);
  return {
    query,
    setQuery,
    results: search.results,
    searching: search.searching,
    trail,
    level: browse.level,
    browsing: browse.browsing,
    // The last answer for what is on screen was "TuneIn unreachable".
    failed: q ? search.failed : browse.failed,
    open: (category: BrowseCategory) => setTrail([...trail, category]),
    canGoUp: !q && trail.length > 0,
    goUp: () => setTrail(trail.slice(0, -1)),
    // Forget the failed answer (so it shows as loading again) and ask once more.
    retry: () => {
      setFound({ query: '', stations: [] });
      setLevel(null);
      setAttempt((n) => n + 1);
    },
  };
}
