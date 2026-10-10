// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState } from 'react';
import { FloatingHint } from '../../../molecules/FloatingHint';
import { Icon } from '../../../atoms/Icon';
import { Button } from '../../../atoms/Button';
import { Note } from '../../../atoms/Note';
import { Stack } from '../../../atoms/Stack';
import { FieldButton, FieldButtonLabel } from '../../../molecules/FieldButton';
import { FieldCard } from '../../../molecules/FieldCard';
import { FieldRow } from '../../../molecules/FieldRow';
import { StatusCard } from '../../../molecules/StatusCard';
import { useI18n } from '../../../../lib/i18n';
import {
  browseUPnP,
  getUPnPServers,
  isUPnPContainer,
  playUPnP,
  searchUPnPServers,
} from '../../../../lib/api';
import type { UPnPItem, UPnPServer } from '../../../../lib/types';
import { Chevron } from './styled';

type Folder = { id: string; title: string };
const ROOT_FOLDER = '0';

// One entry of a folder: a folder opens, a track plays.
function FolderEntry({
  item,
  disabled,
  onOpen,
  onPlay,
}: {
  item: UPnPItem;
  disabled: boolean;
  onOpen: () => void;
  onPlay: () => void;
}) {
  const folder = isUPnPContainer(item);
  return (
    <FieldButton disabled={disabled} onClick={folder ? onOpen : onPlay}>
      {folder ? <Icon.folder width="18" height="18" /> : <Icon.play width="14" height="14" />}
      <FieldButtonLabel>{item.title}</FieldButtonLabel>
      {folder && <Icon.chevron width="16" height="16" />}
    </FieldButton>
  );
}

// One media server, opened: its current folder, a way back up, and what is in it.
function ServerBrowser({
  server,
  speaker,
  onStatus,
}: {
  server: UPnPServer;
  speaker: string;
  onStatus: (text: string) => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [trail, setTrail] = useState<Folder[]>([]);
  const [items, setItems] = useState<UPnPItem[]>([]);
  const [busy, setBusy] = useState(false);
  // Only the newest browse may land; an older answer would jump back to where it was sent.
  const seq = useRef(0);

  const browse = async (next: Folder[]) => {
    const mine = ++seq.current;
    setBusy(true);
    const found = await browseUPnP(server.usn, next[next.length - 1]?.id ?? ROOT_FOLDER, speaker);
    if (mine !== seq.current) return;
    setBusy(false);
    if (!found) return onStatus(t('upnpBrowseFailed'));
    onStatus(''); // an earlier failure is over
    setItems(found);
    setTrail(next);
    setOpen(true);
  };

  const play = async (item: UPnPItem) => {
    if (!item.url) return;
    setBusy(true);
    try {
      const folder = trail[trail.length - 1]?.id ?? ROOT_FOLDER;
      await playUPnP({ url: item.url, name: item.title, server: server.usn, folder }, speaker);
      onStatus(`${t('playing')} ${item.title}`);
    } catch {
      onStatus(t('upnpPlayFailed'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <FieldCard>
      <FieldButton
        expanded={open}
        disabled={busy && !open}
        onClick={() => {
          if (!open) return void browse([]);
          seq.current += 1;
          setBusy(false);
          setOpen(false);
        }}
      >
        <FieldButtonLabel>{server.friendlyName}</FieldButtonLabel>
        <Chevron $open={open} aria-hidden="true">
          <Icon.chevron width="18" height="18" />
        </Chevron>
      </FieldButton>
      {open && trail.length > 0 && (
        <FieldButton disabled={busy} onClick={() => void browse(trail.slice(0, -1))}>
          <Icon.back width="16" height="16" />
          <FieldButtonLabel>{trail[trail.length - 1].title}</FieldButtonLabel>
        </FieldButton>
      )}
      {open &&
        items.map((item) => (
          <FolderEntry
            key={item.id}
            item={item}
            disabled={busy}
            onOpen={() => void browse([...trail, { id: item.id, title: item.title }])}
            onPlay={() => void play(item)}
          />
        ))}
      {open && !busy && !items.length && <FieldRow label={t('upnpEmptyFolder')} wideLabel />}
    </FieldCard>
  );
}

// Media servers (UPnP/DLNA, a NAS) on the network: folders open, tracks play on this
// speaker.
export function MediaServers({ speaker }: { speaker: string }) {
  const { t } = useI18n();
  // undefined while searching; null when the speaker could not say.
  const [servers, setServers] = useState<UPnPServer[] | null | undefined>(undefined);
  const [status, setStatus] = useState('');
  // Bumped by "search again"; each value is one discovery round.
  const [round, setRound] = useState(0);
  useEffect(() => {
    let current = true;
    const find = round ? searchUPnPServers : getUPnPServers;
    void find(speaker).then((found) => current && setServers(found));
    return () => {
      current = false;
    };
  }, [speaker, round]);
  const search = () => {
    setServers(undefined);
    setRound((r) => r + 1);
  };

  return (
    <Stack>
      <Note>{t('msDesc')}</Note>
      {servers?.length ? (
        <Stack gap={8}>
          {servers.map((server) => (
            <ServerBrowser
              key={server.usn}
              server={server}
              speaker={speaker}
              onStatus={setStatus}
            />
          ))}
        </Stack>
      ) : (
        <>
          <StatusCard busy={servers === undefined} icon={<Icon.search width="22" height="22" />}>
            {servers === undefined ? t('msSearching') : t('msNone')}
          </StatusCard>
          <Button variant="ghost" wide disabled={servers === undefined} onClick={search}>
            {t('msSearch')}
          </Button>
        </>
      )}
      {servers?.length ? (
        // A NAS that comes online later must be findable without leaving the page.
        <Button variant="ghost" wide onClick={search}>
          {t('msSearch')}
        </Button>
      ) : null}
      {status && <FloatingHint>{status}</FloatingHint>}
    </Stack>
  );
}
