// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Complete values of the types the hook tests pass around: each test states only what it
// cares about, and the rest is a plain default the type checker has seen whole.
import { vi } from 'vitest';
import type { Room } from './useRooms';
import type { SelectedRoom } from './useSelectedRoom';
import type {
  MultiroomClient,
  NowPlaying,
  PlayerStation,
  Preset,
  RecentEntry,
  ReleaseRef,
  Releases,
  Station,
  VersionInfo,
} from '../lib/types';
import type { RecentItem } from '../lib/recent';
import type { SpotifyPlaylist, SpotifyStatus, SpotifyTrack } from '../lib/spotify';

export const room = (over: Partial<Room> = {}): Room => ({
  id: 'kitchen',
  name: 'Kitchen',
  model: 'SoundTouch 10',
  ip: '192.0.2.10',
  online: true,
  self: false,
  now: null,
  volume: 20,
  sources: [],
  linkedTo: null,
  ...over,
});

export const nowPlaying = (over: Partial<NowPlaying> = {}): NowPlaying => ({
  standby: false,
  ...over,
});

export const version = (over: Partial<VersionInfo> = {}): VersionInfo => ({
  version: '0.1.0',
  os: '0.1.0',
  app: '0.1.0',
  commit: '',
  updatable: true,
  updateMode: 'firmware',
  api: 1,
  capabilities: [],
  ...over,
});

export const releaseRef = (over: Partial<ReleaseRef> = {}): ReleaseRef => ({
  tag: 'app-v0.1.0',
  name: '',
  notes: '',
  publishedAt: '',
  ...over,
});

export const releases = (over: Partial<Releases> = {}): Releases => ({
  current: 'app-v0.1.0',
  updatable: true,
  stable: null,
  ...over,
});

export const recentEntry = (over: Partial<RecentEntry> = {}): RecentEntry => ({
  source: 'TUNEIN',
  name: 'Jazz',
  location: 's1',
  art: '',
  stoppedAt: '2026-01-01T00:00:00Z',
  ...over,
});

export const recentItem = (over: Partial<RecentItem> = {}): RecentItem => ({
  key: 'tunein:s1',
  name: 'Jazz',
  art: '',
  tuneInId: 's1',
  spotifyUri: null,
  source: 'TUNEIN',
  stoppedAt: '2026-01-01T00:00:00Z',
  locations: ['s1'],
  ...over,
});

export const preset = (over: Partial<Preset> = {}): Preset => ({
  slot: 1,
  name: 'Jazz',
  tuneInId: 's1',
  location: 's1',
  logo: '',
  ...over,
});

export const station = (over: Partial<Station> = {}): Station => ({
  tuneInId: 's1',
  name: 'Jazz',
  tagline: '',
  genre: '',
  country: '',
  logo: '',
  ...over,
});

export const playerStation = (over: Partial<PlayerStation> = {}): PlayerStation => ({
  name: 'Jazz',
  art: '',
  tuneInId: 's1',
  track: '',
  artist: '',
  ...over,
});

export const multiroomClient = (over: Partial<MultiroomClient> = {}): MultiroomClient => ({
  id: 'client',
  name: '',
  host: '',
  ip: '192.0.2.20',
  percent: 100,
  muted: false,
  groupId: '',
  current: false,
  member: false,
  connected: false,
  latency: 0,
  ...over,
});

export const spotifyStatus = (over: Partial<SpotifyStatus> = {}): SpotifyStatus => ({
  available: false,
  authenticated: false,
  connected: false,
  reauthRequired: false,
  state: '',
  ...over,
});

export const spotifyPlaylist = (name: string): SpotifyPlaylist => ({
  uri: `spotify:playlist:${name}`,
  name,
  total: 0,
});

export const spotifyTrack = (name: string): SpotifyTrack => ({
  uri: `spotify:track:${name}`,
  name,
  artists: [],
  durationMs: 0,
});

type Live = SelectedRoom['live'];
type RoomsState = SelectedRoom['roomsState'];

// The speaker followed live, idle and quiet; every action a mock the test can watch.
export const live = (over: Partial<Live> = {}): Live => ({
  player: { status: 'idle', station: null },
  nowPlaying: null,
  volume: 20,
  presets: [],
  loading: false,
  refreshPresets: vi.fn(),
  volumeFailures: 0,
  unreachable: false,
  refreshNowPlaying: vi.fn(),
  changeVolume: vi.fn(),
  playOptimistic: vi.fn(),
  stopOptimistic: vi.fn(),
  releaseStopHold: vi.fn(),
  cancelPending: vi.fn(),
  gaveUp: null,
  nudge: vi.fn(),
  ...over,
});

export const roomsState = (over: Partial<RoomsState> = {}): RoomsState => ({
  rooms: [],
  loaded: true,
  leaders: {},
  refresh: vi.fn(),
  patch: vi.fn(),
  regroup: vi.fn(),
  reconnect: vi.fn(),
  loadState: vi.fn(),
  ...over,
});

// What useSelectedRoom hands the page: no rooms unless the test names them.
export const selectedRoom = ({
  live: liveOver,
  roomsState: stateOver,
  ...over
}: Partial<Omit<SelectedRoom, 'live' | 'roomsState'>> & {
  live?: Partial<Live>;
  roomsState?: Partial<RoomsState>;
} = {}): SelectedRoom => ({
  rooms: [],
  lead: null,
  members: [],
  speaker: '',
  select: vi.fn(),
  ...over,
  live: live(liveOver),
  roomsState: roomsState(stateOver),
});
