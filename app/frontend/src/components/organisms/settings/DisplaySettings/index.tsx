// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useRef, useState } from 'react';
import { Button } from '../../../atoms/Button';
import { Hint } from '../../../atoms/Hint';
import { Spinner } from '../../../atoms/Spinner';
import { Stack } from '../../../atoms/Stack';
import { CheckRow } from '../../../molecules/CheckRow';
import { FieldButton, FieldButtonLabel } from '../../../molecules/FieldButton';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow, RowInput } from '../../../molecules/FieldRow';
import { SegmentedControl } from '../../../molecules/SegmentedControl';
import { useI18n } from '../../../../lib/i18n';
import { geocode } from '../../../../lib/api';
import type { DisplayConfig, Place } from '../../../../lib/types';

const placeLabel = (place: Place, withRegion = false) =>
  [place.name, withRegion ? place.region : '', place.country].filter(Boolean).join(', ');

// The town the standby clock keeps time for. The owner types it and only that name is
// looked up; the speaker never guesses where it stands.
function TownPicker({
  city,
  speaker,
  onPick,
}: {
  city: string;
  speaker: string;
  onPick: (place: Place) => void;
}) {
  const { t } = useI18n();
  const [editing, setEditing] = useState(!city);
  const [query, setQuery] = useState('');
  // null: nothing searched yet; []: nothing found (or the lookup failed).
  const [places, setPlaces] = useState<Place[] | null>(null);
  const [busy, setBusy] = useState(false);

  // Only the newest search lands, whether it came from the button or Enter: an older
  // answer would replace the results for what is typed now.
  const seq = useRef(0);
  const search = async () => {
    const q = query.trim();
    if (!q) return;
    const mine = ++seq.current;
    setBusy(true);
    const found = await geocode(q, speaker).catch(() => []);
    if (mine !== seq.current) return;
    setPlaces(found);
    setBusy(false);
  };
  const choose = (place: Place) => {
    seq.current += 1;
    setBusy(false);
    onPick(place);
    setEditing(false);
    setPlaces(null);
    setQuery('');
  };

  if (!editing)
    return (
      <FieldCard>
        <FieldRow label={city} wideLabel>
          <Button variant="inline" onClick={() => setEditing(true)}>
            {t('standbyCityChange')}
          </Button>
        </FieldRow>
      </FieldCard>
    );
  return (
    <>
      <FieldCard>
        <FieldRow label={t('standbyCity')} htmlFor="town-query">
          <RowInput
            id="town-query"
            $align="left"
            type="search"
            placeholder={t('standbyCityPlaceholder')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && void search()}
          />
          <Button variant="inline" onClick={() => void search()} disabled={busy || !query.trim()}>
            {busy ? <Spinner /> : t('standbyCitySearch')}
          </Button>
        </FieldRow>
        {places?.map((place) => (
          <FieldButton key={`${place.lat},${place.lon}`} onClick={() => choose(place)}>
            <FieldButtonLabel>{placeLabel(place, true)}</FieldButtonLabel>
          </FieldButton>
        ))}
      </FieldCard>
      {places?.length === 0 && <Hint error>{t('standbyCityNone')}</Hint>}
    </>
  );
}

// The ST20 standby screen: a clock for a picked town, optionally with its weather.
export function DisplaySettings({
  config,
  speaker,
  onChange,
}: {
  config: DisplayConfig;
  speaker: string;
  onChange: (patch: Partial<DisplayConfig>) => void;
}) {
  const { t } = useI18n();
  // Without a town the clock keeps the browser's zone, as stock shows its clock without one.
  const clockOn = !config.clockOff;
  return (
    <>
      <TownPicker
        city={config.city}
        speaker={speaker}
        onPick={(place) =>
          onChange({
            city: placeLabel(place),
            lat: place.lat,
            lon: place.lon,
            timezone: place.timezone,
            clockOff: false,
          })
        }
      />
      <Hint>{t('standbyCityHint')}</Hint>
      <Stack gap={8} offset={8}>
        <CheckRow
          on={clockOn}
          title={t('standbyClock')}
          onToggle={() => onChange({ clockOff: clockOn })}
        />
        {clockOn && !!config.city && (
          <CheckRow
            on={config.weatherOn}
            title={t('weatherLine')}
            onToggle={() => onChange({ weatherOn: !config.weatherOn })}
          />
        )}
        {clockOn && (
          <FieldCard>
            <FieldRow label={t('clockFormat')} layout="value">
              <SegmentedControl
                label={t('clockFormat')}
                value={config.hour12}
                options={[
                  { value: false, label: t('hour24') },
                  { value: true, label: t('hour12') },
                ]}
                onChange={(hour12) => onChange({ hour12 })}
              />
            </FieldRow>
            <FieldRow label={t('clockBrightness')} layout="value">
              <SegmentedControl
                label={t('clockBrightness')}
                value={config.brightness || 80}
                options={[25, 50, 80].map((value) => ({ value, label: `${value}%` }))}
                onChange={(brightness) => onChange({ brightness })}
              />
            </FieldRow>
          </FieldCard>
        )}
      </Stack>
    </>
  );
}
