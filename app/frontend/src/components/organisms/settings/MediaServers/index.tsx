// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState, type ReactElement } from 'react';
import { Icon } from '../../../atoms/Icon';
import { Button } from '../../../atoms/Button';
import { SetItem } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { SheetModal } from '../../../molecules/SheetModal';
import { StatusCard } from '../../../molecules/StatusCard';
import { useI18n } from '../../../../lib/i18n';
import {
  browseUPnP,
  getUPnPServers,
  isUPnPContainer,
  searchUPnPServers,
} from '../../../../lib/api';
import type { UPnPServer } from '../../../../lib/types';
import { FolderPage } from './Folder';
import { Intro, SearchAgain } from './styled';

// The servers on the network: read once, and searched for again on request.
function useServers(speaker: string) {
  // undefined while searching; null when the speaker could not say.
  const [servers, setServers] = useState<UPnPServer[] | null | undefined>(undefined);
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
  return { servers, search };
}

// The names of each server's top-level folders ("Music, Audiobooks"), the line under its
// name: its root is read once per server while the page is open, a server found again by
// "search again" included. Nothing shows while a read runs, when it fails or when the root
// holds no folders.
function useRootFolders(speaker: string, servers: UPnPServer[] | null | undefined) {
  const [folders, setFolders] = useState<Record<string, string>>({});
  const asked = useRef(new Set<string>());
  useEffect(() => {
    for (const { usn } of servers ?? []) {
      if (asked.current.has(usn)) continue;
      asked.current.add(usn);
      void browseUPnP(usn, '0', speaker).then((items) => {
        const names = (items ?? []).filter(isUPnPContainer).map((item) => item.title);
        const line = names.filter(Boolean).join(', ');
        if (line) setFolders((known) => ({ ...known, [usn]: line }));
      });
    }
  }, [speaker, servers]);
  return folders;
}

// Media servers (UPnP/DLNA, a NAS) on the network, as a list: a tap opens a server on a page
// of its own, where folders open deeper and tracks play on this speaker. onExit leaves the
// media servers altogether.
export function MediaServers({
  speaker,
  onExit,
}: {
  speaker: string;
  onExit?: () => void;
}): ReactElement {
  const { t } = useI18n();
  const { servers, search } = useServers(speaker);
  const folders = useRootFolders(speaker, servers);
  const [opened, setOpened] = useState<UPnPServer | null>(null);
  return (
    <>
      <Intro>{t('msDesc')}</Intro>
      {servers && servers.length > 0 ? (
        <SetMenu title={t('msFound')}>
          {servers.map((server) => (
            <SetItem
              key={server.usn}
              icon="server"
              label={server.friendlyName}
              desc={folders[server.usn]}
              onClick={() => setOpened(server)}
            />
          ))}
        </SetMenu>
      ) : (
        <StatusCard busy={servers === undefined} icon={<Icon.search width="22" height="22" />}>
          {servers === undefined ? t('msSearching') : t('msNone')}
        </StatusCard>
      )}
      {/* A NAS that comes online later must be findable without leaving the page. */}
      <SearchAgain>
        <Button variant="ghost" wide disabled={servers === undefined} onClick={search}>
          <Icon.search width="16" height="16" aria-hidden="true" />
          {t('msSearch')}
        </Button>
      </SearchAgain>
      <SheetModal
        open={!!opened}
        onClose={() => setOpened(null)}
        label={opened?.friendlyName ?? ''}
        variant="page"
      >
        {(close) =>
          opened && (
            <FolderPage
              server={opened}
              speaker={speaker}
              onBack={close}
              onExit={() => {
                close();
                onExit?.();
              }}
            />
          )
        }
      </SheetModal>
    </>
  );
}
