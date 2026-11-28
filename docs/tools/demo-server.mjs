// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Serves the built SoundTap web UI with a fake speaker behind /api, for the README screenshots
// (shoot.mjs) and the end-to-end tests (app/frontend/e2e). No dependencies.
// Usage: node demo-server.mjs <dist-dir> [port] [host]
// It listens on 127.0.0.1 only, where its callers reach it (they run beside it, in the same
// container or on the same machine); give a host (0.0.0.0, say) to serve other machines too.
//
// The speakers keep a little state, so a test can watch the page act on it: the volume a
// slider set, the name and Wi-Fi a setup sent, and a live /api/events stream as the real
// speaker pushes it. /api/demo/* is the tests' remote control, not part of SoundTap's API:
//   POST /api/demo/away   the speaker goes away: every /api call fails, streams drop
//   POST /api/demo/back   it answers again
//   POST /api/demo/reset  back to the state it started in
//   POST /api/demo/offer-update  every speaker offers app 0.2.0; one asked to update then
//                         downloads it for ever, 42% of the way (an update the page follows)
//   GET  /api/demo/state  what the page has sent: volumes, names, the Wi-Fi it joined,
//                         the speakers asked to update
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { extname, join, normalize } from "node:path";

const [dist, port = "8080", host = "127.0.0.1"] = process.argv.slice(2);
const TYPES = {
  ".html": "text/html",
  ".js": "text/javascript",
  ".css": "text/css",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".woff2": "font/woff2",
  ".webmanifest": "application/json",
};

const STATIONS = [
  ["s1", "Radio Paradise", "Eclectic mix, no ads", "#c8553d"],
  ["s2", "KEXP 90.3 FM", "Where the music matters", "#e0a030"],
  ["s3", "NPO Radio 2", "Het beste van toen en nu", "#3d6fc8"],
  ["s4", "FIP", "Radio France", "#d94f8a"],
  ["s5", "Jazz24", "Essential jazz", "#2f9c78"],
  ["s6", "SomaFM Groove Salad", "Ambient and downtempo", "#6a5acd"],
  ["s7", "Radio Swiss Jazz", "Jazz around the clock", "#2a8fa8"],
  ["s8", "NTS 1", "Radio from the underground", "#444444"],
].map(([tuneInId, name, tagline, color]) => ({
  tuneInId,
  name,
  tagline,
  color,
}));
const logo = (s) => `/api/demo/logo/${s.tuneInId}.svg`;
const initials = (s) =>
  s.name
    .replace(/[^A-Za-z0-9 ]/g, "")
    .split(" ")
    .slice(0, 2)
    .map((w) => w[0])
    .join("");
const logoSvg = (s) =>
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" fill="${s.color}"/><text x="32" y="41" font-family="sans-serif" font-weight="700" font-size="26" text-anchor="middle" fill="#fff">${initials(s)}</text></svg>`;

// Invented library, drawn by artSvgFor: no real covers or account data in the README.
const hue = (seed) =>
  [...seed].reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 360, 7);
const artSvgFor = (seed) => {
  const h = hue(seed);
  const c = (dh, s, l) => `hsl(${(h + dh) % 360},${s}%,${l}%)`;
  const svg = (body) =>
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 400">${body}</svg>`;
  const sky = (top, bottom) =>
    `<defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${top}"/><stop offset="1" stop-color="${bottom}"/></linearGradient></defs><rect width="400" height="400" fill="url(#g)"/>`;
  // One picture per motif, picked by the seed: a sunrise for the track that plays, then
  // waves, mountains, a record, rings and stripes. Drawn here, so no real cover is shown.
  const rays = (cx, cy, n, r1, r2, colour) =>
    Array.from({ length: n }, (_, i) => {
      const a = (i / n) * Math.PI * 2;
      return `<line x1="${cx + Math.cos(a) * r1}" y1="${cy + Math.sin(a) * r1}" x2="${cx + Math.cos(a) * r2}" y2="${cy + Math.sin(a) * r2}" stroke="${colour}" stroke-width="10" stroke-linecap="round"/>`;
    }).join("");
  const motifs = [
    () =>
      sky("#ffcf6b", "#f2734f") +
      `<circle cx="200" cy="250" r="92" fill="#fff4c9"/>${rays(200, 250, 14, 112, 150, "#fff4c9")}` +
      `<path d="M0 290 Q100 250 200 290 T400 285 V400 H0Z" fill="#e0533f"/><path d="M0 330 Q120 300 230 335 T400 330 V400 H0Z" fill="#b8393a"/>`,
    () =>
      sky(c(190, 60, 62), c(220, 55, 30)) +
      [0, 1, 2, 3]
        .map(
          (i) =>
            `<path d="M0 ${200 + i * 45} Q50 ${180 + i * 45} 100 ${200 + i * 45} T200 ${200 + i * 45} T300 ${200 + i * 45} T400 ${200 + i * 45} V400 H0Z" fill="#fff" fill-opacity="${0.12 + i * 0.08}"/>`,
        )
        .join("") +
      `<circle cx="300" cy="110" r="40" fill="#fff" fill-opacity=".85"/>`,
    () =>
      sky(c(20, 55, 70), c(260, 40, 35)) +
      `<path d="M0 300 L110 150 L180 240 L260 120 L400 300 V400 H0Z" fill="${c(250, 35, 28)}"/><path d="M260 120 L290 160 L270 165 L250 150Z" fill="#fff" fill-opacity=".8"/><path d="M0 340 L150 250 L260 330 L400 260 V400 H0Z" fill="${c(240, 40, 18)}"/>`,
    () =>
      `<rect width="400" height="400" fill="${c(0, 55, 55)}"/><circle cx="200" cy="200" r="150" fill="#141414"/>` +
      [125, 110, 95, 80]
        .map(
          (r) =>
            `<circle cx="200" cy="200" r="${r}" fill="none" stroke="#2a2a2a" stroke-width="2"/>`,
        )
        .join("") +
      `<circle cx="200" cy="200" r="48" fill="${c(30, 70, 62)}"/><circle cx="200" cy="200" r="6" fill="#141414"/>`,
    () =>
      `<rect width="400" height="400" fill="${c(300, 45, 22)}"/>` +
      [180, 140, 100, 60]
        .map(
          (r, i) =>
            `<circle cx="${130 + i * 20}" cy="${270 - i * 20}" r="${r}" fill="${c(i * 40, 70, 55 + i * 6)}"/>`,
        )
        .join(""),
    () =>
      `<rect width="400" height="400" fill="${c(0, 30, 92)}"/>` +
      [0, 1, 2, 3, 4, 5, 6]
        .map(
          (i) =>
            `<rect x="${-40 + i * 70}" y="-40" width="34" height="480" fill="${c(i * 25, 65, 50)}" transform="rotate(20 200 200)"/>`,
        )
        .join(""),
  ];
  const pick = seed === "Sun" ? 0 : 1 + (h % (motifs.length - 1));
  return svg(motifs[pick]());
};
const coverOf = (seed) => `/api/demo/cover/${encodeURIComponent(seed)}.svg`;
const artist = (name) => ({
  uri: `spotify:artist:${name.toLowerCase().replace(/\W/g, "")}`,
  name,
  artwork: coverOf(name),
});
const ARTISTS = [
  "Ada Lindqvist",
  "The Harbour Lights",
  "Noor & Mats",
  "Blue Meridian",
  "Kaito Sand",
  "Velvet Ferry",
].map(artist);
const TRACKS = [
  ["Slow Tide", 0],
  ["Paper Lanterns", 1],
  ["Second Summer", 2],
  ["Northern Line", 3],
  ["Quiet Engines", 4],
  ["Long Way Round", 5],
  ["Glasshouse", 0],
  ["After the Rain", 3],
].map(([name, a]) => ({
  uri: `spotify:track:${name.toLowerCase().replace(/\W/g, "")}`,
  name,
  artists: [{ uri: ARTISTS[a].uri, name: ARTISTS[a].name }],
  album: { uri: `spotify:album:${a}`, name: `${ARTISTS[a].name} Sessions` },
  artwork: coverOf(name),
  durationMs: 184000 + name.length * 9000,
}));
const PLAYLISTS = [
  "Sunday Morning",
  "Late Night Jazz",
  "Kitchen Disco",
  "Deep Focus",
  "Road Trip",
  "Rainy Day",
].map((name) => ({
  uri: `spotify:playlist:${name.toLowerCase().replace(/\W/g, "")}`,
  name,
  owner: "SoundTap",
  artwork: coverOf(name),
  total: TRACKS.length,
}));
const spotifyRecent = [
  ...PLAYLISTS.slice(0, 3).map((p) => ({
    kind: "playlist",
    uri: p.uri,
    name: p.name,
    artwork: p.artwork,
  })),
  ...ARTISTS.slice(0, 2).map((a) => ({
    kind: "artist",
    uri: a.uri,
    name: a.name,
    artwork: a.artwork,
  })),
];

function spotify(path) {
  if (path === "/api/spotify/status") {
    return {
      available: true,
      authenticated: true,
      connected: true,
      reauthRequired: false,
      state: "connected",
    };
  }
  if (path === "/api/spotify/me/playlists")
    return { items: PLAYLISTS, total: PLAYLISTS.length };
  if (path === "/api/spotify/me/tracks")
    return { items: TRACKS, total: TRACKS.length };
  if (path === "/api/spotify/me/recent") return spotifyRecent;
  if (path === "/api/spotify/me/following") return ARTISTS;
  const list = path.match(/^\/api\/spotify\/playlists\/([^/]+)$/);
  if (list) {
    const p =
      PLAYLISTS.find((x) => x.uri.endsWith(`:${list[1]}`)) ?? PLAYLISTS[0];
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
  {
    id: "woonkamer",
    name: "Woonkamer",
    ip: "192.168.1.20",
    model: "ST10",
    online: true,
    self: true,
  },
  {
    id: "keuken",
    name: "Keuken",
    ip: "192.168.1.21",
    model: "ST20",
    online: true,
    self: false,
  },
  {
    id: "slaapkamer",
    name: "Slaapkamer",
    ip: "192.168.1.22",
    model: "WLA",
    online: true,
    self: false,
  },
  {
    id: "tv",
    name: "TV",
    ip: "192.168.1.23",
    model: "ST300",
    online: true,
    self: false,
  },
];
const VOLUMES = { woonkamer: 28, keuken: 18, slaapkamer: 0, tv: 22 };
// What /api/wifi/scan finds from the setup access point.
const SURVEY = [
  { ssid: "HomeNet", signal: "excellent", secure: true },
  { ssid: "Neighbours", signal: "fair", secure: true },
  { ssid: "Café Guest", signal: "poor", secure: false },
];
// What the page changes; /api/demo/reset puts it back.
let devices;
let volumes;
let muted; // speaker id -> true while muted; its volume stays the level it returns to
let joined;
let away;
let leaders; // follower id -> the id of the speaker it plays along with
let offer; // whether the speakers offer a newer app (/api/demo/offer-update)
let updating; // speaker id -> true once asked to update
function reset() {
  devices = DEVICES.map((d) => ({ ...d }));
  volumes = { ...VOLUMES };
  muted = {};
  joined = null;
  away = false;
  leaders = {};
  offer = false;
  updating = {};
}
reset();
const now = {
  woonkamer: {
    source: "TUNEIN",
    station: "Radio Paradise",
    stationId: "s1",
    track: "Up Till Dawn (On The Move)",
    artist: "Lucas & Steve",
    playStatus: "PLAY_STATE",
    art: coverOf("Sun"),
  },
  keuken: {
    source: "TUNEIN",
    station: "Jazz24",
    stationId: "s5",
    track: "Take Five",
    artist: "The Dave Brubeck Quartet",
    playStatus: "PLAY_STATE",
    art: "",
  },
  slaapkamer: { source: "STANDBY" },
  tv: { source: "STANDBY" },
};
const presets = STATIONS.slice(0, 6).map((s, i) => ({
  slot: i + 1,
  source: "TUNEIN",
  name: s.name,
  stationId: s.tuneInId,
  location: "",
  logo: logo(s),
}));
const station = (s) => ({
  type: "audio",
  item: "station",
  guide_id: s.tuneInId,
  text: s.name,
  subtext: s.tagline,
  image: logo(s),
  locale: "en-US",
});
const settings = {
  model: "ST10",
  language: "en",
  host: "woonkamer.local",
  bass: { actual: 0, min: -9, max: 0, default: 0 },
  integrations: { airplay: true, spotify: true },
  // The rest of a complete /api/settings answer (app/frontend/src/test/settings.ts).
  autoOff: 0,
  auxName: "",
  mqtt: {
    enabled: true,
    host: "homeassistant.local",
    port: 1883,
    username: "",
    hasPassword: false,
    baseTopic: "soundtap",
    discoveryPrefix: "homeassistant",
    tls: false,
  },
  hasDisplay: false,
  display: { clockOff: false, hour12: false, timezone: "" },
  alarm: { enabled: false, time: "07:00", slot: 1 },
  multiroom: { keepGroups: false, syncVolume: false },
};
// What only the SoundTouch 300 has: its bass module and rears, its tone and its TV settings.
const SOUNDBAR = {
  bassPaired: true,
  bassConnected: true,
  rearsPaired: true,
  rearsConnected: true,
};
const level = (value) => ({ value, min: -10, max: 10, step: 1 });
const TONE = {
  bass: level(2),
  treble: level(0),
  centre: level(1),
  rear: level(-2),
};
const SOUNDBAR_CHOICES = {
  "audio-mode": "dialog",
  "cec-mode": "on",
  "hdmi-input": "1",
};
// The speakers playing along with `dev`, as its snapserver lists them.
const followers = (dev) =>
  Object.entries(leaders)
    .filter(([, lead]) => lead === dev)
    .map(([id]) => {
      const d = devices.find((x) => x.id === id);
      const client = {
        id,
        name: d.name,
        host: `${id}.local`,
        ip: d.ip,
        percent: volumes[id] ?? 0,
      };
      return {
        ...client,
        muted: false,
        groupId: dev,
        deviceId: id,
        current: false,
        member: true,
        connected: true,
      };
    });

// A GET of `path` on speaker `dev`: the answer, or null for a 404.
function api(path, dev, url) {
  if (path === "/api/devices") return { devices };
  if (path === "/api/now") return now[dev] ?? { source: "STANDBY" };
  if (path === "/api/volume")
    return { volume: volumes[dev] ?? 0, muted: muted[dev] === true };
  if (path === "/api/presets") return presets;
  if (path === "/api/sleep") return { minutes: 0, endsAt: "" };
  if (path === "/api/user-sources" || path === "/api/favorites") return [];
  if (path === "/api/recent") {
    return STATIONS.slice(2, 8).map((s, i) => ({
      source: "TUNEIN",
      name: s.name,
      location: `/v1/playback/station/${s.tuneInId}`,
      art: logo(s),
      stoppedAt: `2026-11-0${8 - i}T17:30:00Z`,
    }));
  }
  if (path === "/api/channel") return { channel: "stereo" };
  if (path === "/api/wifi/saved")
    return { networks: [{ id: "aG9tZQ==", ssid: "Home" }] };
  if (path.startsWith("/api/spotify/")) return spotify(path);
  if (path === "/api/settings") {
    const d = devices.find((x) => x.id === dev);
    return {
      ...settings,
      name: d.name,
      model: d.model,
      ...(d.model === "ST300" && { soundbar: SOUNDBAR }),
    };
  }
  if (path === "/api/version") {
    const v = { version: "0.1.0", os: "0.1.0", app: "0.1.0", updatable: false };
    if (!offer) return v;
    return {
      ...v,
      updatable: true,
      updateMode: "firmware",
      ...(updating[dev] && { updateProgress: 42 }),
    };
  }
  if (path === "/api/releases") {
    const stable = offer
      ? { tag: "app-v0.2.0", name: "", notes: "", publishedAt: "" }
      : null;
    return { current: "0.1.0", updatable: offer, stable, betas: [] };
  }
  if (path === "/api/multiroom") return followers(dev);
  if (path === "/api/soundbar/tone") return dev === "tv" ? TONE : null;
  if (path === "/api/soundbar/lip-sync")
    return dev === "tv" ? { ms: 40, max: 250 } : null;
  const choice = path.match(
    /^\/api\/soundbar\/(audio-mode|cec-mode|hdmi-input)$/,
  );
  if (choice)
    return dev === "tv" ? { mode: SOUNDBAR_CHOICES[choice[1]] } : null;
  if (path === "/api/bluetooth") {
    return {
      adapter: false,
      alias: "",
      powered: false,
      discoverable: false,
      devices: [],
    };
  }
  if (path === "/api/mqtt/status") return { connected: true, lastError: "" };
  if (path === "/api/upnp/servers") return [];
  if (path.startsWith("/api/tunein/")) {
    const q = (url.searchParams.get("query") ?? "").toLowerCase();
    const list = q
      ? STATIONS.filter((s) => (s.name + s.tagline).toLowerCase().includes(q))
      : STATIONS;
    return { body: list.map(station) };
  }
  return null;
}

// The live stream of each speaker, as soundtapd sends it: a state push now and on every
// change, and a ping every few seconds so the page can tell a stream that died.
const streams = new Set();
const stateOf = (dev) => ({
  now: now[dev] ?? { source: "STANDBY" },
  volume: volumes[dev] ?? 0,
  muted: muted[dev] === true,
  presets: "demo",
});
const event = (name, data) =>
  `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`;
function push(dev) {
  for (const s of streams)
    if (s.dev === dev) s.res.write(event("state", stateOf(dev)));
}
function stream(req, res, dev) {
  res.writeHead(200, {
    "content-type": "text/event-stream",
    "cache-control": "no-cache",
  });
  res.write("retry: 1000\n\n");
  res.write(event("state", stateOf(dev)));
  const s = {
    res,
    dev,
    ping: setInterval(() => res.write(event("ping", {})), 5000),
  };
  streams.add(s);
  req.on("close", () => {
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
    let text = "";
    req.on("data", (chunk) => (text += chunk));
    req.on("end", () => {
      try {
        resolve(JSON.parse(text || "{}"));
      } catch {
        resolve({});
      }
    });
  });

// The writes the page makes that a test looks for; every other one is accepted and forgotten.
function post(path, dev, sent) {
  if (path === "/api/wifi/scan") return { networks: SURVEY };
  if (path === "/api/wifi")
    joined = { ssid: sent.ssid, security: sent.security };
  if (path === "/api/multiroom/invite") {
    const d = devices.find((x) => x.ip === sent.ip);
    if (d) leaders[d.id] = dev;
  }
  if (path === "/api/multiroom/follow" && !sent.host) delete leaders[dev];
  if (path === "/api/volume" && typeof sent.volume === "number") {
    volumes[dev] = Math.max(0, Math.min(100, Math.round(sent.volume)));
    delete muted[dev]; // setting a level is asking to hear it
    push(dev);
  }
  if (path === "/api/mute" && typeof sent.muted === "boolean") {
    muted[dev] = sent.muted;
    push(dev);
    return { volume: volumes[dev] ?? 0, muted: sent.muted };
  }
  if (path === "/api/settings" && typeof sent.name === "string") {
    const d = devices.find((x) => x.id === dev);
    if (d) d.name = sent.name;
  }
  return {};
}

function demo(path, res) {
  const json = (v) => {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify(v));
  };
  if (path === "/api/demo/away") {
    away = true;
    dropStreams();
  } else if (path === "/api/demo/back") away = false;
  else if (path === "/api/demo/offer-update") offer = true;
  else if (path === "/api/demo/reset") {
    dropStreams();
    reset();
  } else if (path !== "/api/demo/state") return false;
  json({
    away,
    volumes,
    joined,
    names: Object.fromEntries(devices.map((d) => [d.id, d.name])),
    updating,
  });
  return true;
}

createServer(async (req, res) => {
  const url = new URL(req.url, "http://x");
  let path = url.pathname;
  const send = (code, type, body) => {
    res.writeHead(code, { "content-type": type });
    res.end(body);
  };
  if (path.startsWith("/api/") || path.includes("/api/")) {
    const m = path.match(/\/api\/demo\/logo\/(s\d+)\.svg$/);
    if (m)
      return send(
        200,
        "image/svg+xml",
        logoSvg(STATIONS.find((s) => s.tuneInId === m[1])),
      );
    const cover = path.match(/\/api\/demo\/cover\/(.+)\.svg$/);
    if (cover)
      return send(
        200,
        "image/svg+xml",
        artSvgFor(decodeURIComponent(cover[1])),
      );
    if (path.startsWith("/api/demo/") && demo(path, res)) return;
    if (away) return send(503, "text/plain", "away");
    const routed = path.match(/^\/api\/devices\/([^/]+)(\/api\/.*)$/);
    const dev = routed ? routed[1] : "woonkamer";
    if (routed) path = routed[2];
    if (path === "/api/events") return stream(req, res, dev);
    if (path.includes("/events")) return send(404, "text/plain", "no stream");
    // An offered update the speaker takes on: it answers before it downloads.
    if (path === "/api/update" && req.method === "POST" && offer) {
      updating[dev] = true;
      return send(
        202,
        "application/json",
        JSON.stringify({ status: "updating", to: "0.2.0" }),
      );
    }
    if (req.method !== "GET")
      return send(
        200,
        "application/json",
        JSON.stringify(post(path, dev, await body(req))),
      );
    const answer = api(path, dev, url);
    return answer === null
      ? send(404, "text/plain", "not found")
      : send(200, "application/json", JSON.stringify(answer));
  }
  try {
    const file = join(dist, normalize(path === "/" ? "/index.html" : path));
    send(
      200,
      TYPES[extname(file)] ?? "application/octet-stream",
      await readFile(file),
    );
  } catch {
    send(404, "text/plain", "not found");
  }
}).listen(Number(port), host);
