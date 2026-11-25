// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import type { ReactElement } from 'react';
import { Button } from '../../atoms/Button';
import { Stack } from '../../atoms/Stack';
import { StationLogo } from '../../atoms/StationLogo';
import { ListRow } from '../../molecules/ListRow';
import { SheetEmpty } from '../../molecules/SheetEmpty';
import { SheetGroup } from '../../molecules/SheetGroup';
import { SheetLoading } from '../../molecules/SheetLoading';
import { useI18n } from '../../../lib/i18n';
import { initials, logoURL } from '../../../lib/artwork';
import type { Station } from '../../../lib/types';
import type { TuneInBrowser } from '../../../hooks/useTuneInBrowser';

interface Props {
  tunein: TuneInBrowser;
  speaker: string;
  onPick: (station: Station) => void;
  // Picking stores on a preset instead of playing.
  onHold?: ((station: Station) => void) | undefined;
}

// The TuneIn tab's body: search results, or the current level of the directory.
export function TuneInResults({ tunein, speaker, onPick, onHold }: Props): ReactElement {
  const { t } = useI18n();
  const { query, results, searching, trail, level, browsing, failed } = tunein;
  const stationRows = (stations: Station[]) =>
    stations.map((station) => (
      <ListRow
        key={station.tuneInId}
        art={<StationLogo src={logoURL(station, speaker)} initials={initials(station.name)} />}
        title={station.name}
        subtitle={[station.genre, station.tagline || station.country].filter(Boolean).join(' · ')}
        onClick={() => onPick(station)}
        onHold={onHold && (() => onHold(station))}
      />
    ));

  if (searching || (!query.trim() && browsing)) return <SheetLoading />;
  if (failed)
    return (
      <SheetEmpty>
        {t('tuneinError')}
        <Stack offset={12}>
          <Button variant="ghost" onClick={tunein.retry}>
            {t('reconnect')}
          </Button>
        </Stack>
      </SheetEmpty>
    );
  if (query.trim()) {
    if (!results.length) return <SheetEmpty query={query}>{t('noStations')}</SheetEmpty>;
    return <SheetGroup>{stationRows(results)}</SheetGroup>;
  }
  if (!level.categories.length && !level.stations.length)
    return <SheetEmpty>{t('noStations')}</SheetEmpty>;
  // Stations first: a genre is opened to find something to play, and TuneIn lists its
  // sub-folders before the stations, which pushed them below the fold.
  return (
    <>
      {level.stations.length > 0 && (
        <SheetGroup title={t('stations')}>
          {stationRows(level.stations)}
          {level.more && (
            <ListRow
              title={level.more.title}
              onClick={() => level.more && tunein.open(level.more)}
            />
          )}
        </SheetGroup>
      )}
      {level.categories.length > 0 && (
        <SheetGroup title={trail[trail.length - 1]?.title || 'TuneIn'}>
          {level.categories.map((category) => (
            <ListRow
              key={category.path}
              title={category.title}
              onClick={() => tunein.open(category)}
            />
          ))}
        </SheetGroup>
      )}
    </>
  );
}
