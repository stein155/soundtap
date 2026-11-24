// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Shared domain types for the SoundTap UI. The API client (api.ts) produces these;
// hooks and components consume them. Kept in one place so the shapes stay in sync.
// What soundtapd answers comes from api/schema.d.ts, generated from
// app/internal/web/openapi.json (`npm run api-types`); Go tests hold that file to the
// handlers. Change a response there, regenerate, and the compiler finds every reader here.
import type { components } from './api/schema';

type Schemas = components['schemas'];

// Normalised now-playing. `standby` true means idle/off; the other fields are
// only populated when not in standby (kept optional so callers can read them
// without narrowing, matching the original loose JS access).
export interface NowPlaying {
  standby: boolean;
  source?: string;
  stationName?: string;
  track?: string;
  artist?: string;
  album?: string;
  playStatus?: string;
  art?: string;
  tuneInId?: string | null;
  canSkip?: boolean; // next and previous work through the transport keys
  failed?: boolean; // this selection ended without ever playing
}

// One recently-played entry. location is empty for non-replayable sources
// (Spotify, AirPlay, Bluetooth).
export type RecentEntry = Schemas['RecentEntry'];

// What saving a favorite sends to the speaker.
export type FavoriteEntry = Schemas['Favorite'];

// One entry of GET /api/presets, before getPresets normalises it into a Preset.
export type ApiPreset = Schemas['Preset'];

// One preset slot (1..6), or null for an empty slot.
export interface Preset {
  slot: number;
  source?: string;
  name: string;
  tuneInId: string | null;
  location: string;
  logo: string;
}

// A drill-down category in the TuneIn browse tree (genre, region, …).
export interface BrowseCategory {
  title: string;
  path: string; // proxy-relative path to fetch its contents (/Browse.ashx?...)
}

// One level of the browse tree: sub-categories and/or stations.
export interface BrowseResult {
  // TuneIn's "more stations" page for this level, shown as a load-more row.
  more?: BrowseCategory;
  categories: BrowseCategory[];
  stations: Station[];
}

// A TuneIn search result / catalog station.
export interface Station {
  tuneInId: string;
  name: string;
  tagline: string;
  genre: string;
  country: string;
  logo: string;
}

// Derived playback state the UI renders (see useSpeaker).
export type PlayerStatus = 'idle' | 'starting' | 'buffering' | 'playing' | 'paused';

export interface PlayerStation {
  name: string;
  art: string;
  tuneInId: string | null;
  track: string;
  artist: string;
  // The speaker's source (SPOTIFY, TUNEIN, ...) once now-playing reports one.
  source?: string;
}

export interface Player {
  status: PlayerStatus;
  station: PlayerStation | null;
}

// A tap target passed to playOptimistic: a preset or a search result.
export interface PlayTarget {
  name: string;
  tuneInId?: string | null;
  logo?: string;
}

export type BassCaps = Schemas['BassCaps'];

// The paired bass module and rears of a soundbar (SoundTouch 300).
export type Soundbar = Schemas['Soundbar'];

// One soundbar tone level with the range the soundbar allows.
export type ToneControl = Schemas['ToneControl'];

// A soundbar's tone and speaker levels.
export type Tone = Schemas['Tone'];

export type NetworkInfo = Schemas['NetworkInfo'];

// DisplayConfig is the ST20 standby screen; its timezone is the browser's.
export type DisplayConfig = Schemas['Display'];

export type MqttConfig = Schemas['Settings']['mqtt'];

// The wake-up alarm: at `time` (24h) it plays preset `slot`. The time is read in the
// standby clock's zone, else in `timezone`: the zone of the browser that saved it.
export type AlarmConfig = Schemas['Alarm'];

// Household multiroom preferences. Stored on every speaker; the UI writes them to all.
export type MultiroomPrefs = Schemas['Settings']['multiroom'];

// The receivers other apps play through, switched for the whole household.
export type IntegrationsPrefs = Schemas['Settings']['integrations'];

// GET /api/settings: name, model, bass and network are absent when the speaker could not
// be asked, soundbar on a speaker that is not one.
export type Settings = Schemas['Settings'];

// A crash on the speaker written up as a GitHub issue the owner opens and submits: the
// exact title and body the link carries, redacted on the speaker (ADR 0126).
export type CrashReport = Schemas['CrashReport'];

export type WifiNetwork = Schemas['WifiNetwork'];

// A speaker this one found on the network. /api/devices has always known about the other
// speakers; until the multiroom section listed them there was nowhere to see them. These
// are the fields of one that the UI keeps.
export type SpeakerDevice = Pick<
  Schemas['Device'],
  'id' | 'name' | 'ip' | 'model' | 'online' | 'self'
>;

export type MultiroomClient = Schemas['MultiroomClient'];

// updateMode: 'firmware' is a speaker running SoundTap Core, where updatable says whether
// the app can fetch its own releases; 'unmanaged' is not an installed instance at all.
export type UpdateMode = VersionInfo['updateMode'];

export type VersionInfo = Schemas['Version'];

export type FirmwareReport = Schemas['FirmwareReport'];

// The owner key (ADR 0048): whether one is set, the key and its fingerprint.
export type OwnerKey = Schemas['OwnerKey'];

export type ReleaseRef = Schemas['ReleaseRef'];

export type Releases = Schemas['Releases'];

// What went wrong, for the UI to say in its own language (ADR 0091). A speaker on an older
// build sends no code, and a newer one may send a code this page does not know yet.
export type ApiErrorCode = Schemas['Error']['code'];

// send()/getJSON() reject with an Error carrying the HTTP status and the speaker's code.
export interface ApiError extends Error {
  status?: number;
  code?: string;
}

// startUpdate resolves to the raw response shape (not thrown on non-2xx).
export interface UpdateResult {
  ok: boolean;
  status: number;
  body: { status?: string; to?: string; error?: string; [k: string]: unknown };
}

// UPnP ContentDirectory discovery — /api/upnp/servers and /api/upnp/browse.
export type UPnPServer = Schemas['UPnPServer'];

export type UPnPItem = Schemas['UPnPItem'];
