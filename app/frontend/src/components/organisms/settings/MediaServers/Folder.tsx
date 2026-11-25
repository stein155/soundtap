// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

import { useEffect, useRef, useState, type ReactElement } from 'react';
import { EqualizerBars } from '../../../atoms/EqualizerBars';
import { Icon } from '../../../atoms/Icon';
import { FloatingHint } from '../../../molecules/FloatingHint';
import { SetItem } from '../../../molecules/SetItem';
import { SetMenu } from '../../../molecules/SetMenu';
import { Sheet } from '../../../molecules/Sheet';
import { SheetEmpty } from '../../../molecules/SheetEmpty';
import { SheetLoading } from '../../../molecules/SheetLoading';
import { useI18n } from '../../../../lib/i18n';
import { browseUPnP, isUPnPContainer, playUPnP } from '../../../../lib/api';
import type { UPnPItem, UPnPServer } from '../../../../lib/types';
import { Crumbs, FolderHead, FolderTitle, Track, TrackGlyph } from './styled';

interface Folder {
  id: string;
  title: string;
}
const ROOT_FOLDER = '0';

// One server's folders and tracks, one folder at a time.
function useFolder(server: UPnPServer, speaker: string) {
  const { t } = useI18n();
  const [trail, setTrail] = useState<Folder[]>([]);
  const [items, setItems] = useState<UPnPItem[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [playing, setPlaying] = useState<string | null>(null);
  // Only the newest browse may land; an older answer would jump back to where it was sent.
  const seq = useRef(0);
  const browse = async (next: Folder[]) => {
    const mine = ++seq.current;
    setBusy(true);
    const found = await browseUPnP(server.usn, next.at(-1)?.id ?? ROOT_FOLDER, speaker);
    if (mine !== seq.current) return;
    setBusy(false);
    if (!found) {
      setError(t('upnpBrowseFailed'));
      return;
    }
    setError('');
    setItems(found);
    setTrail(next);
  };
  const play = async (item: UPnPItem) => {
    if (!item.url) return;
    setBusy(true);
    try {
      const folder = trail.at(-1)?.id ?? ROOT_FOLDER;
      await playUPnP({ url: item.url, name: item.title, server: server.usn, folder }, speaker);
      setError('');
      setPlaying(item.id);
    } catch {
      setError(t('upnpPlayFailed'));
    } finally {
      setBusy(false);
    }
  };
  return { trail, items, busy, error, playing, browse, play };
}

// The folder's contents: its folders under one bar, its tracks under another.
function FolderBody({ folder }: { folder: ReturnType<typeof useFolder> }) {
  const { t } = useI18n();
  const { items, busy } = folder;
  const folders = items?.filter(isUPnPContainer) ?? [];
  const tracks = items?.filter((item) => !isUPnPContainer(item)) ?? [];
  return (
    <>
      {items === null && <SheetLoading />}
      {items?.length === 0 && <SheetEmpty>{t('upnpEmptyFolder')}</SheetEmpty>}
      {folders.length > 0 && (
        <SetMenu title={t('msFolders')}>
          {folders.map((item) => (
            <SetItem
              key={item.id}
              icon="folder"
              label={item.title}
              onClick={() => {
                if (!busy)
                  void folder.browse([...folder.trail, { id: item.id, title: item.title }]);
              }}
            />
          ))}
        </SetMenu>
      )}
      {tracks.length > 0 && (
        <SetMenu title={t('spotifyTracks')}>
          {tracks.map((item) => (
            <Track
              key={item.id}
              type="button"
              disabled={busy}
              $playing={folder.playing === item.id}
              onClick={() => void folder.play(item)}
            >
              <TrackGlyph aria-hidden="true">
                {folder.playing === item.id ? (
                  <EqualizerBars />
                ) : (
                  <Icon.play width="14" height="14" />
                )}
              </TrackGlyph>
              {item.title}
            </Track>
          ))}
        </SetMenu>
      )}
    </>
  );
}

// A media server opened on a page of its own: where in it this is, its folders (a tap goes
// deeper) and its tracks (a tap plays). Back goes up one folder, and from the top back to
// the list of servers; the X leaves the media servers.
export function FolderPage({
  server,
  speaker,
  onBack,
  onExit,
}: {
  server: UPnPServer;
  speaker: string;
  onBack: () => void;
  onExit: () => void;
}): ReactElement {
  const folder = useFolder(server, speaker);
  const { trail } = folder;
  const opened = useRef(false);
  useEffect(() => {
    if (opened.current) return;
    opened.current = true;
    void folder.browse([]);
  });
  const title = trail.at(-1)?.title ?? server.friendlyName;
  return (
    <Sheet
      onBack={trail.length ? () => void folder.browse(trail.slice(0, -1)) : onBack}
      onExit={onExit}
      eyebrow={server.friendlyName}
      ownHero
      label={title}
      scrollKey={trail.map((f) => f.id).join('>')}
    >
      <FolderHead>
        {trail.length > 1 && <Crumbs>{trail.map((f) => f.title).join(' › ')}</Crumbs>}
        <FolderTitle>{title}</FolderTitle>
      </FolderHead>
      <FolderBody folder={folder} />
      {folder.error && <FloatingHint error>{folder.error}</FloatingHint>}
    </Sheet>
  );
}
