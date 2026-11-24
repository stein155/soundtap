// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState } from 'react';
import { flushSync } from 'react-dom';
import type * as React from 'react';
import { Icon } from '../../atoms/Icon';
import { EntryRow, SearchEntry } from '../../molecules/SearchEntry';
import { Sheet } from '../../molecules/Sheet';
import { SheetModal } from '../../molecules/SheetModal';
import { SetItem } from '../../molecules/SetItem';
import { SetMenu } from '../../molecules/SetMenu';
import { CustomStation } from '../settings/CustomStation';
import { TopBar } from '../TopBar';
import { TuneInResults } from '../TuneInResults';
import { SpotifyLibrary } from '../SpotifyLibrary';
import { useEscape } from '../../../hooks/useEscape';
import { useTuneInBrowser } from '../../../hooks/useTuneInBrowser';
import { useSpotifyLibrary } from '../../../hooks/useSpotifyLibrary';
import { useI18n } from '../../../lib/i18n';
import type { Station } from '../../../lib/types';
import { ClearButton, SearchBox, SearchInput } from './styled';

export type Service = 'tunein' | 'spotify';
// Which service the page opens on. `search` opens straight on the search field instead of the
// service's page.
export type SearchMode = { service?: Service; search?: boolean };

interface Props {
  mode: SearchMode;
  speaker: string;
  onMenu: () => void;
  onPresets: () => void;
  onClose: () => void;
  onPick: (station: Station) => void;
  onHoldStation?: (station: Station) => void;
  onHoldSpotify?: (item: { uri: string; name: string; artwork?: string }) => void;
  onSpotifyPlayed: () => void;
}

const serviceOf = (mode: SearchMode): Service => mode.service ?? 'tunein';

const BRANDS = {
  tunein: { label: 'TuneIn', icon: <Icon.tuneinMark /> },
  spotify: { label: 'Spotify', icon: <Icon.spotifyMark /> },
};

// Where the page keeps its scroll position: one per level of whichever tree is open.
function scrollKeyOf(
  service: Service,
  inSearch: boolean,
  tunein: ReturnType<typeof useTuneInBrowser>,
  spotify: ReturnType<typeof useSpotifyLibrary>
): string {
  if (service === 'spotify')
    return `spotify|${spotify.trail.map((p) => (p.kind === 'liked' ? 'liked' : p.uri)).join('>')}`;
  return `tunein|${inSearch ? 'search' : tunein.trail.map((c) => c.path).join('>')}`;
}

// The search field in the top bar. Enter ("Search" on a phone keyboard) puts the keyboard
// away so the results show.
function SearchField({
  input,
  query,
  setQuery,
}: {
  input: React.RefObject<HTMLInputElement | null>;
  query: string;
  setQuery: (query: string) => void;
}) {
  const { t } = useI18n();
  return (
    <form
      role="search"
      onSubmit={(e) => {
        e.preventDefault();
        input.current?.blur();
      }}
    >
      <SearchBox>
        <SearchInput
          ref={input}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('searchPlaceholder')}
          aria-label={t('searchPlaceholder')}
          type="search"
          enterKeyHint="search"
          autoCapitalize="none"
          autoCorrect="off"
          spellCheck={false}
        />
        {query && (
          <ClearButton type="button" onClick={() => setQuery('')} aria-label={t('clear')}>
            <Icon.close width="16" height="16" />
          </ClearButton>
        )}
      </SearchBox>
    </form>
  );
}

// The page's own state: which service shows and whether the search field is open. Every
// opening starts on the service it was opened for.
function useOpening(mode: SearchMode) {
  const [service, setService] = useState<Service>(serviceOf(mode));
  const [searching, setSearching] = useState(!!mode.search);
  const input = useRef<HTMLInputElement | null>(null);
  // The field goes away while it holds focus: the bar it opened from takes focus.
  const [leftSearch, setLeftSearch] = useState(false);
  const [openedFor, setOpenedFor] = useState(mode);
  if (mode !== openedFor) {
    setOpenedFor(mode);
    setService(serviceOf(mode));
    setSearching(!!mode.search);
  }
  const opensOnSearch = !!mode.search;
  useEffect(() => {
    if (opensOnSearch) input.current?.focus();
  }, [opensOnSearch]);
  // Focused inside the tap: a phone only brings up the keyboard for one.
  const openSearch = () => {
    flushSync(() => setSearching(true));
    input.current?.focus();
  };
  const leaveSearch = () => {
    setLeftSearch(true);
    setSearching(false);
  };
  return { service, searching, input, leftSearch, openSearch, leaveSearch };
}

// TuneIn's page: the search row over the categories, or the results of a search.
function TuneInPage({
  tunein,
  inSearch,
  search,
  speaker,
  onPick,
  onHold,
}: {
  tunein: ReturnType<typeof useTuneInBrowser>;
  inSearch: boolean;
  // Opens the search field; takeFocus when the field just went away while focused.
  search: { open: () => void; takeFocus: boolean };
  speaker: string;
  onPick: (station: Station) => void;
  onHold?: (station: Station) => void;
}) {
  const { t } = useI18n();
  const atTop = !inSearch && !tunein.canGoUp;
  // A stream TuneIn does not list, by its URL: a way to play, so it lives with the radio.
  const [custom, setCustom] = useState(false);
  return (
    <>
      {atTop && (
        <EntryRow>
          <SearchEntry
            label={t('searchTuneIn')}
            onPress={search.open}
            takeFocus={search.takeFocus}
          />
        </EntryRow>
      )}
      {(!inSearch || tunein.query.trim()) && (
        <TuneInResults tunein={tunein} speaker={speaker} onPick={onPick} onHold={onHold} />
      )}
      {atTop && (
        <SetMenu>
          <SetItem icon="plus" label={t('customStationAdd')} onClick={() => setCustom(true)} />
        </SetMenu>
      )}
      <SheetModal
        open={custom}
        onClose={() => setCustom(false)}
        label={t('customStation')}
        variant="page"
      >
        {(close) => (
          <Sheet onExit={close} headline={t('customStation')} label={t('customStation')}>
            <CustomStation
              onStream={(station) => {
                onPick(station);
                close();
              }}
            />
          </Sheet>
        )}
      </SheetModal>
    </>
  );
}

// Pick something to play, or to store on a preset: TuneIn or the Spotify library. Only a
// preset can hold either, so only then the page offers the choice; otherwise it shows the
// service it was opened for. A main page as in Stockholm: the top bar stays and names the
// source, and the bars at the bottom stay under it. A tap on the search row brings the field
// into the top bar with the keyboard. The back chevron walks back up whichever tree is open;
// the menu and the phone's back leave the page.
export function SearchSheet({
  mode,
  speaker,
  onMenu,
  onPresets,
  onClose,
  onPick,
  onHoldStation,
  onHoldSpotify,
  onSpotifyPlayed,
}: Props) {
  const { lang } = useI18n();
  const { service, searching, input, leftSearch, openSearch, leaveSearch } = useOpening(mode);
  const tunein = useTuneInBrowser(service === 'tunein', lang, true);
  const spotify = useSpotifyLibrary(
    service === 'spotify',
    onHoldSpotify,
    () => {
      onSpotifyPlayed();
      onClose();
    },
    true
  );

  const browser = service === 'tunein' ? tunein : spotify;
  const inSearch = service === 'tunein' && searching;
  const closeSearch = () => {
    tunein.setQuery('');
    leaveSearch();
  };
  const searchField = <SearchField input={input} query={tunein.query} setQuery={tunein.setQuery} />;
  const up = inSearch ? closeSearch : browser.canGoUp ? browser.goUp : undefined;
  useEscape(true, up ?? onClose);
  return (
    <Sheet
      scrollKey={scrollKeyOf(service, inSearch, tunein, spotify)}
      label={BRANDS[service].label}
      bar={
        <TopBar
          onMenu={onMenu}
          onPresets={onPresets}
          icon={BRANDS[service].icon}
          title={BRANDS[service].label}
          // Back and Escape leave the search, and what was typed, before the page.
          onBack={up}
          onCloseField={closeSearch}
          field={inSearch ? searchField : undefined}
        />
      }
    >
      {service === 'tunein' ? (
        <TuneInPage
          tunein={tunein}
          inSearch={inSearch}
          search={{ open: openSearch, takeFocus: leftSearch }}
          speaker={speaker}
          onPick={onPick}
          onHold={onHoldStation}
        />
      ) : (
        <SpotifyLibrary library={spotify} />
      )}
    </Sheet>
  );
}
