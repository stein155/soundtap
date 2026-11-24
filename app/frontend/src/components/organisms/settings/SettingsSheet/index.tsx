// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Sheet } from '../../../molecules/Sheet';
import { Integration, Integrations, SettingsOverview, type Receiver } from '../SettingsOverview';
import { SpeakerSettings } from '../SpeakerSettings';
import { Mqtt } from '../Mqtt';
import { useI18n, type I18nKey } from '../../../../lib/i18n';
import type { Room } from '../../../../hooks/useRooms';
import { SubPageContext, type SubPage } from '../../../../hooks/useSubPage';

interface Props {
  rooms: Room[];
  onRenamed: () => void;
  onExit: () => void;
  // Open at this speaker's page instead of the overview.
  speakerId?: string | null;
  // Whether the page on screen holds unsaved edits, for whatever closes the sheet.
  onDirty?: (dirty: boolean) => void;
}

type Page = 'integrations' | 'mqtt' | Receiver;
const PAGE_TITLE: Record<Page, I18nKey | { brand: string }> = {
  integrations: 'integrations',
  mqtt: 'page_mqtt',
  spotify: { brand: 'Spotify Connect' },
  airplay: { brand: 'AirPlay' },
  applehome: { brand: 'Apple Home' },
};
// The page the back button returns to; the rest go back to where they were opened.
const PARENT: Partial<Record<Page, Page>> = {
  mqtt: 'integrations',
  spotify: 'integrations',
  airplay: 'integrations',
  applehome: 'integrations',
};

// Where in settings the sheet is: the overview, a speaker, or a page below either.
type Location = { speakerId: string | null; page: Page | null };
const OVERVIEW: Location = { speakerId: null, page: null };

type T = ReturnType<typeof useI18n>['t'];

// The small line above the headline.
function eyebrowOf(t: T, sub: SubPage | null, room: Room | null, page: Page | null) {
  if (sub) return room?.name || t('settings');
  if (room) return page ? room.name : t('speaker');
  return page ? t('settings') : null;
}

function headlineOf(t: T, sub: SubPage | null, room: Room | null, page: Page | null) {
  if (sub) return sub.title;
  const title = page && PAGE_TITLE[page];
  if (title) return typeof title === 'string' ? t(title) : title.brand;
  return room ? room.name : t('settings');
}

// The Integrations list and the pages below it; null for any other page.
function integrationPage(
  page: Page | null,
  rooms: Room[],
  open: (page: Page) => void,
  onDirty: (dirty: boolean) => void
) {
  if (page === 'integrations') return <Integrations rooms={rooms} onOpen={open} />;
  if (page === 'spotify' || page === 'airplay' || page === 'applehome')
    return <Integration rooms={rooms} which={page} />;
  if (page === 'mqtt') return <Mqtt rooms={rooms} onDirty={onDirty} />;
  return null;
}

// The Settings tab, in two levels: an overview and one page per speaker; the integrations
// and MQTT open as pages of their own. The back button goes up one level.
// Every visit starts afresh, at the overview or at the speaker it was opened for.
export function SettingsSheet({
  rooms,
  onRenamed,
  onExit,
  speakerId = null,
  onDirty: tellDirty,
}: Props) {
  const { t } = useI18n();
  const [at, setAt] = useState<Location>({ speakerId, page: null });
  // A row's own page (language, Wi-Fi, ...), drawn over the list it came from.
  const [sub, setSub] = useState<SubPage | null>(null);
  const [layer, setLayer] = useState<HTMLDivElement | null>(null);
  const host = useMemo(() => ({ layer, show: setSub }), [layer]);

  // Whether the page on screen holds unsaved edits: leaving it asks first.
  const [dirty, setDirty] = useState(false);
  const onDirty = useCallback((d: boolean) => setDirty(d), []);
  useEffect(() => tellDirty?.(dirty), [dirty, tellDirty]);
  const leave = (go: () => void) => () => {
    if (dirty && !window.confirm(t('discardChanges'))) return;
    setDirty(false);
    go();
  };

  const room = rooms.find((r) => r.id === at.speakerId) ?? null;
  const nested = at.page !== null || room !== null;
  const up = leave(() =>
    at.page ? setAt({ ...at, page: PARENT[at.page] ?? null }) : setAt(OVERVIEW)
  );
  // A row's page goes back to its list without asking: its edits belong to that list.
  const back = sub ? sub.back : up;

  const settingsPage = () => {
    if (room)
      return (
        // Keyed by speaker: no section may carry one speaker's state onto another.
        <SpeakerSettings
          key={room.id}
          room={room}
          rooms={rooms}
          onRenamed={onRenamed}
          onDirty={onDirty}
        />
      );
    return (
      <SettingsOverview
        rooms={rooms}
        onSpeaker={(speakerId) => setAt({ speakerId, page: null })}
        onIntegrations={() => setAt({ speakerId: null, page: 'integrations' })}
      />
    );
  };
  const body =
    integrationPage(at.page, rooms, (page) => setAt({ speakerId: null, page }), onDirty) ??
    settingsPage();

  return (
    <Sheet
      onExit={onExit}
      scrollKey={`${at.speakerId}|${at.page}|${sub?.title}`}
      onBack={nested || sub ? back : undefined}
      backLabel={t('back')}
      eyebrow={eyebrowOf(t, sub, room, at.page)}
      headline={headlineOf(t, sub, room, at.page)}
      label={t('settings')}
    >
      <SubPageContext.Provider value={host}>
        <div hidden={!!sub}>{body}</div>
        <div ref={setLayer} hidden={!sub} />
      </SubPageContext.Provider>
    </Sheet>
  );
}
