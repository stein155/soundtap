// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Serves the built SoundTap web UI with a fake speaker behind /api, for the README screenshots
// (shoot.mjs) and the end-to-end tests (app/frontend/e2e). No dependencies.
// Usage: node demo-server.mjs <dist-dir> [port]
//
// The speakers keep a little state, so a test can watch the page act on it: the volume a
// slider set, the name and Wi-Fi a setup sent, and a live /api/events stream as the real
// speaker pushes it. /api/demo/* is the tests' remote control, not part of SoundTap's API:
//   POST /api/demo/away   the speaker goes away: every /api call fails, streams drop
//   POST /api/demo/back   it answers again
//   POST /api/demo/reset  back to the state it started in
//   GET  /api/demo/state  what the page has sent: volumes, names, the Wi-Fi it joined
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join, normalize } from 'node:path';

const [dist, port = '8080'] = process.argv.slice(2);
const TYPES = {
  '.html': 'text/html',
  '.js': 'text/javascript',
  '.css': 'text/css',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.woff2': 'font/woff2',
  '.webmanifest': 'application/json',
};

const STATIONS = [
  ['s1', 'Radio Paradise', 'Eclectic mix, no ads', '#c8553d'],
  ['s2', 'KEXP 90.3 FM', 'Where the music matters', '#e0a030'],
  ['s3', 'NPO Radio 2', 'Het beste van toen en nu', '#3d6fc8'],
  ['s4', 'FIP', 'Radio France', '#d94f8a'],
  ['s5', 'Jazz24', 'Essential jazz', '#2f9c78'],
  ['s6', 'SomaFM Groove Salad', 'Ambient and downtempo', '#6a5acd'],
  ['s7', 'Radio Swiss Jazz', 'Jazz around the clock', '#2a8fa8'],
  ['s8', 'NTS 1', 'Radio from the underground', '#444444'],
].map(([tuneInId, name, tagline, color]) => ({ tuneInId, name, tagline, color }));
const logo = (s) => `/api/demo/logo/${s.tuneInId}.svg`;
const initials = (s) =>
  s.name
    .replace(/[^A-Za-z0-9 ]/g, '')
    .split(' ')
    .slice(0, 2)
    .map((w) => w[0])
    .join('');
const logoSvg = (s) =>
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" fill="${s.color}"/><text x="32" y="41" font-family="sans-serif" font-weight="700" font-size="26" text-anchor="middle" fill="#fff">${initials(s)}</text></svg>`;

// Invented library, drawn by artSvgFor: no real covers or account data in the README.
const hue = (seed) => [...seed].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 360, 7);
const artSvgFor = (seed) => {
  const h = hue(seed);
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 400"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="hsl(${h},62%,58%)"/><stop offset="1" stop-color="hsl(${(h + 50) % 360},58%,30%)"/></linearGradient></defs><rect width="400" height="400" fill="url(#g)"/><circle cx="${120 + h % 160}" cy="${130 + h % 140}" r="${70 + h % 70}" fill="#fff" fill-opacity=".14"/></svg>`;
};
const coverOf = (seed) => `/api/demo/cover/${encodeURIComponent(seed)}.svg`;
const artist = (name) => ({
  uri: `spotify:artist:${name.toLowerCase().replace(/\W/g, '')}`,
  name,
  artwork: coverOf(name),
});
const ARTISTS = ['Ada Lindqvist', 'The Harbour Lights', 'Noor & Mats', 'Blue Meridian', 'Kaito Sand', 'Velvet Ferry'].map(artist);
const TRACKS = [
  ['Slow Tide', 0],
  ['Paper Lanterns', 1],
  ['Second Summer', 2],
  ['Northern Line', 3],
  ['Quiet Engines', 4],
  ['Long Way Round', 5],
  ['Glasshouse', 0],
  ['After the Rain', 3],
].map(([name, a]) => ({
  uri: `spotify:track:${name.toLowerCase().replace(/\W/g, '')}`,
  name,
  artists: [{ uri: ARTISTS[a].uri, name: ARTISTS[a].name }],
  album: { uri: `spotify:album:${a}`, name: `${ARTISTS[a].name} Sessions` },
  artwork: coverOf(name),
  durationMs: 184000 + name.length * 9000,
}));
const PLAYLISTS = ['Sunday Morning', 'Late Night Jazz', 'Kitchen Disco', 'Deep Focus', 'Road Trip', 'Rainy Day'].map((name) => ({
  uri: `spotify:playlist:${name.toLowerCase().replace(/\W/g, '')}`,
  name,
  owner: 'SoundTap',
  artwork: coverOf(name),
  total: TRACKS.length,
}));
const spotifyRecent = [
  ...PLAYLISTS.slice(0, 3).map((p) => ({ kind: 'playlist', uri: p.uri, name: p.name, artwork: p.artwork })),
  ...ARTISTS.slice(0, 2).map((a) => ({ kind: 'artist', uri: a.uri, name: a.name, artwork: a.artwork })),
];

function spotify(path) {
  if (path === '/api/spotify/status') {
    return { available: true, authenticated: true, connected: true, reauthRequired: false, state: 'connected' };
  }
  if (path === '/api/spotify/me/playlists') return { items: PLAYLISTS, total: PLAYLISTS.length };
  if (path === '/api/spotify/me/tracks') return { items: TRACKS, total: TRACKS.length };
  if (path === '/api/spotify/me/recent') return spotifyRecent;
  if (path === '/api/spotify/me/following') return ARTISTS;
  const list = path.match(/^\/api\/spotify\/playlists\/([^/]+)$/);
  if (list) {
    const p = PLAYLISTS.find((x) => x.uri.endsWith(`:${list[1]}`)) ?? PLAYLISTS[0];
    return { ...p, tracks: TRACKS };
  }
  const art = path.match(/^\/api\/spotify\/artists\/([^/]+)$/);
  if (art) {
    return {
      artist: ARTISTS.find((x) => x.uri.endsWith(`:${art[1]}`)) ?? ARTISTS[0],
      topTracks: TRACKS,
      albums: [],
      playlists: PLAYLISTS.slice(0, 2),
    };
  }
  return null;
}

const DEVICES = [
  { id: 'woonkamer', name: 'Woonkamer', ip: '192.168.1.20', model: 'ST10', online: true, self: true },
  { id: 'keuken', name: 'Keuken', ip: '192.168.1.21', model: 'ST20', online: true, self: false },
  { id: 'slaapkamer', name: 'Slaapkamer', ip: '192.168.1.22', model: 'WLA', online: true, self: false },
];
const VOLUMES = { woonkamer: 28, keuken: 18, slaapkamer: 0 };
// What /api/wifi/scan finds from the setup access point.
const SURVEY = [
  { ssid: 'HomeNet', signal: 'excellent', secure: true },
  { ssid: 'Neighbours', signal: 'fair', secure: true },
  { ssid: 'Café Guest', signal: 'poor', secure: false },
];
// What the page changes; /api/demo/reset puts it back.
let devices;
let volumes;
let joined;
let away;
function reset() {
  devices = DEVICES.map((d) => ({ ...d }));
  volumes = { ...VOLUMES };
  joined = null;
  away = false;
}
reset();
const now = {
  woonkamer: {
    source: 'TUNEIN',
    station: 'Radio Paradise',
    stationId: 's1',
    track: 'I Am Stretched On Your Grave',
    artist: "Sinéad O'Connor",
    playStatus: 'PLAY_STATE',
    art: coverOf('Stretched'),
  },
  keuken: {
    source: 'TUNEIN',
    station: 'Jazz24',
    stationId: 's5',
    track: 'Take Five',
    artist: 'The Dave Brubeck Quartet',
    playStatus: 'PLAY_STATE',
    art: '',
  },
  slaapkamer: { source: 'STANDBY' },
};
const presets = STATIONS.slice(0, 6).map((s, i) => ({
  slot: i + 1,
  source: 'TUNEIN',
  name: s.name,
  stationId: s.tuneInId,
  location: '',
  logo: logo(s),
}));
const station = (s) => ({
  type: 'audio',
  item: 'station',
  guide_id: s.tuneInId,
  text: s.name,
  subtext: s.tagline,
  image: logo(s),
  locale: 'en-US',
});
const settings = {
  model: 'ST10',
  language: 'en',
  host: 'woonkamer.local',
  bass: { actual: 0, min: -9, max: 0, default: 0 },
  integrations: { airplay: true, spotify: true },
  // The rest of a complete /api/settings answer (app/frontend/src/test/settings.ts).
  autoOff: 0,
  auxName: '',
  mqtt: { enabled: false, host: '', port: 1883, username: '', hasPassword: false, baseTopic: '', discoveryPrefix: '', tls: false },
  hasDisplay: false,
  display: { clockOff: false, hour12: false, city: '', lat: 0, lon: 0, timezone: '', weatherOn: false },
  alarm: { enabled: false, time: '07:00', slot: 1 },
  multiroom: { keepGroups: false, syncVolume: false },
};

// A GET of `path` on speaker `dev`: the answer, or null for a 404.
function api(path, dev, url) {
  if (path === '/api/devices') return { devices };
  if (path === '/api/now') return now[dev] ?? { source: 'STANDBY' };
  if (path === '/api/volume') return { volume: volumes[dev] ?? 0 };
  if (path === '/api/presets') return presets;
  if (path === '/api/sleep') return { minutes: 0, endsAt: '' };
  if (path === '/api/user-sources' || path === '/api/favorites') return [];
  if (path === '/api/recent') {
    return STATIONS.slice(2, 8).map((s, i) => ({
      source: 'TUNEIN',
      name: s.name,
      location: `/v1/playback/station/${s.tuneInId}`,
      art: logo(s),
      stoppedAt: `2026-11-0${8 - i}T17:30:00Z`,
    }));
  }
  if (path === '/api/channel') return { channel: 'stereo' };
  if (path === '/api/wifi/saved') return { networks: [{ id: 'aG9tZQ==', ssid: 'Home' }] };
  if (path.startsWith('/api/spotify/')) return spotify(path);
  if (path === '/api/settings') {
    return { ...settings, name: devices.find((d) => d.id === dev).name };
  }
  if (path === '/api/version') return { version: '0.1.0', os: '0.1.0', app: '0.1.0', updatable: false };
  if (path === '/api/releases') return { current: '0.1.0', updatable: false, stable: null, betas: [] };
  if (path === '/api/multiroom') return [];
  if (path === '/api/bluetooth') {
    return { adapter: false, alias: '', powered: false, discoverable: false, devices: [] };
  }
  if (path === '/api/mqtt/status') return { connected: false, lastError: '' };
  if (path === '/api/upnp/servers') return [];
  if (path.startsWith('/api/tunein/')) {
    const q = (url.searchParams.get('query') ?? '').toLowerCase();
    const list = q ? STATIONS.filter((s) => (s.name + s.tagline).toLowerCase().includes(q)) : STATIONS;
    return { body: list.map(station) };
  }
  return null;
}

// The live stream of each speaker, as soundtapd sends it: a state push now and on every
// change, and a ping every few seconds so the page can tell a stream that died.
const streams = new Set();
const stateOf = (dev) => ({ now: now[dev] ?? { source: 'STANDBY' }, volume: volumes[dev] ?? 0, presets: 'demo' });
const event = (name, data) => `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`;
function push(dev) {
  for (const s of streams) if (s.dev === dev) s.res.write(event('state', stateOf(dev)));
}
function stream(req, res, dev) {
  res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
  res.write('retry: 1000\n\n');
  res.write(event('state', stateOf(dev)));
  const s = { res, dev, ping: setInterval(() => res.write(event('ping', {})), 5000) };
  streams.add(s);
  req.on('close', () => {
    clearInterval(s.ping);
    streams.delete(s);
  });
}
function dropStreams() {
  for (const s of streams) {
    clearInterval(s.ping);
    s.res.end();
  }
  streams.clear();
}

const body = (req) =>
  new Promise((resolve) => {
    let text = '';
    req.on('data', (chunk) => (text += chunk));
    req.on('end', () => {
      try {
        resolve(JSON.parse(text || '{}'));
      } catch {
        resolve({});
      }
    });
  });

// The writes the page makes that a test looks for; every other one is accepted and forgotten.
function post(path, dev, sent) {
  if (path === '/api/wifi/scan') return { networks: SURVEY };
  if (path === '/api/wifi') joined = { ssid: sent.ssid, security: sent.security };
  if (path === '/api/volume' && typeof sent.volume === 'number') {
    volumes[dev] = Math.max(0, Math.min(100, Math.round(sent.volume)));
    push(dev);
  }
  if (path === '/api/settings' && typeof sent.name === 'string') {
    const d = devices.find((x) => x.id === dev);
    if (d) d.name = sent.name;
  }
  return {};
}

function demo(path, res) {
  const json = (v) => {
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify(v));
  };
  if (path === '/api/demo/away') {
    away = true;
    dropStreams();
  } else if (path === '/api/demo/back') away = false;
  else if (path === '/api/demo/reset') {
    dropStreams();
    reset();
  } else if (path !== '/api/demo/state') return false;
  json({ away, volumes, joined, names: Object.fromEntries(devices.map((d) => [d.id, d.name])) });
  return true;
}

createServer(async (req, res) => {
  const url = new URL(req.url, 'http://x');
  let path = url.pathname;
  const send = (code, type, body) => {
    res.writeHead(code, { 'content-type': type });
    res.end(body);
  };
  if (path.startsWith('/api/') || path.includes('/api/')) {
    const m = path.match(/\/api\/demo\/logo\/(s\d+)\.svg$/);
    if (m) return send(200, 'image/svg+xml', logoSvg(STATIONS.find((s) => s.tuneInId === m[1])));
    const cover = path.match(/\/api\/demo\/cover\/(.+)\.svg$/);
    if (cover) return send(200, 'image/svg+xml', artSvgFor(decodeURIComponent(cover[1])));
    if (path.startsWith('/api/demo/') && demo(path, res)) return;
    if (away) return send(503, 'text/plain', 'away');
    const routed = path.match(/^\/api\/devices\/([^/]+)(\/api\/.*)$/);
    const dev = routed ? routed[1] : 'woonkamer';
    if (routed) path = routed[2];
    if (path === '/api/events') return stream(req, res, dev);
    if (path.includes('/events')) return send(404, 'text/plain', 'no stream');
    if (req.method !== 'GET') return send(200, 'application/json', JSON.stringify(post(path, dev, await body(req))));
    const answer = api(path, dev, url);
    return answer === null
      ? send(404, 'text/plain', 'not found')
      : send(200, 'application/json', JSON.stringify(answer));
  }
  try {
    const file = join(dist, normalize(path === '/' ? '/index.html' : path));
    send(200, TYPES[extname(file)] ?? 'application/octet-stream', await readFile(file));
  } catch {
    send(404, 'text/plain', 'not found');
  }
}).listen(Number(port));
