// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Composes the README art from the screenshots made by shoot.mjs, in the web app's own style:
// Montserrat, grey bands, big centred headlines and flat app screens with a soft shadow. Reads
// /shots and, from docs/assets (/out), the font, the logo, the feature icons and the model
// drawings; writes to /out the hero, the features, the screens and the speakers, each light and
// dark, and the social preview.
import { chromium } from "playwright";
import { readFileSync } from "node:fs";

const shots = process.argv[2] ?? "/shots";
const out = "/out";
const font = readFileSync(`${out}/fonts/montserrat-latin-wght.woff2`).toString(
  "base64",
);
const shot = (name) =>
  `data:image/png;base64,${readFileSync(`${shots}/${name}.png`).toString("base64")}`;
const svgData = (svg) =>
  `data:image/svg+xml;base64,${Buffer.from(svg).toString("base64")}`;

const THEMES = {
  light: {
    bg: "#f9f9f9",
    band: "#fff",
    tile: "#fff",
    ink: "#111",
    sub: "#5e5e5e",
    disc: "#ebebeb",
    shadow: "0 30px 60px -20px rgba(0,0,0,.28), 0 0 0 1px rgba(0,0,0,.06)",
    // The one dark tile of the features.
    accent: { tile: "#111", ink: "#f5f5f7", sub: "#a1a1a6" },
  },
  dark: {
    bg: "#000",
    band: "#0b0b0c",
    tile: "#161617",
    ink: "#f5f5f7",
    sub: "#a1a1a6",
    disc: "#444448",
    shadow: "0 30px 60px -20px rgba(0,0,0,.9), 0 0 0 1px rgba(255,255,255,.10)",
    accent: { tile: "#f5f5f7", ink: "#111", sub: "#5e5e5e" },
  },
};

// The mark of logo.svg without its tile, in the ink of the page.
const logo = (size, ink) =>
  `<svg width="${size}" height="${size}" viewBox="60 40 162 172" aria-hidden="true"><g fill="none" stroke="${ink}" stroke-width="16" stroke-linecap="round"><circle cx="96" cy="176" r="28"/><path d="M124 176V60"/></g><g fill="${ink}"><circle cx="152" cy="76" r="8"/><circle cx="168" cy="100" r="8"/><circle cx="176" cy="128" r="8"/><g fill-opacity=".6"><circle cx="172" cy="48" r="8"/><circle cx="196" cy="76" r="8"/><circle cx="210" cy="108" r="8"/><circle cx="214" cy="142" r="8"/></g></g></svg>`;

// A line icon of docs/assets/icons in one colour.
const icon = (name, color, size = 40) => {
  const body = readFileSync(`${out}/icons/${name}.svg`, "utf8").match(
    /<g>[\s\S]*<\/g>/,
  )[0];
  return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="${color}" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${body}</svg>`;
};

// A model drawing of docs/assets/models on a disc of the given colour.
const model = (name, size, disc) => {
  const svg = readFileSync(`${out}/models/${name}.svg`, "utf8")
    .replace(/<style>[\s\S]*?<\/style>/, "")
    .replace('class="disc"', `fill="${disc}"`);
  return `<img src="${svgData(svg)}" width="${size}" height="${size}" alt="" style="display:block">`;
};

// An app screen as it is, upright and without a phone around it.
const screen = (name, scale, t, radius = 26) =>
  `<div class="screen" style="width:${Math.round(390 * scale)}px;height:${Math.round(844 * scale)}px;border-radius:${radius}px;box-shadow:${t.shadow}"><img src="${shot(name)}"></div>`;

const base = (t) => `
@font-face{font-family:Montserrat;src:url(data:font/woff2;base64,${font});font-weight:100 900}
*{box-sizing:border-box;margin:0}
body{font-family:Montserrat,sans-serif;color:${t.ink};background:${t.bg};overflow:hidden;-webkit-font-smoothing:antialiased}
.screen{flex:none;overflow:hidden;background:${t.tile}}
.screen img{display:block;width:100%;height:100%}
h1,h2{font-weight:800;letter-spacing:-.035em;text-align:center}
h2{font-size:56px;letter-spacing:-.03em}
.sub{color:${t.sub};font-weight:500}
`;

const hero = (t, mode) => `
<style>${base(t)}</style>
<div style="position:relative;width:1600px;height:900px;overflow:hidden">
  <div style="display:flex;flex-direction:column;align-items:center;padding-top:84px;text-align:center">
    <p style="display:flex;align-items:center;gap:10px;font-size:22px;font-weight:700">${logo(34, t.ink)}SoundTap</p>
    <h1 style="margin-top:22px;font-size:80px;line-height:1.02">SoundTouch, the way<br>it should have been.</h1>
    <p class="sub" style="margin-top:22px;max-width:1000px;font-size:24px;line-height:1.4">New software for the Bose speaker you already own. AirPlay&nbsp;2, Apple Home and multiroom with anything. No cloud, no account, no extra box.</p>
  </div>
  <div style="position:absolute;left:140px;top:590px">${model("st10", 210, t.disc)}</div>
  <div style="position:absolute;right:130px;top:600px">${model("st300", 200, t.disc)}</div>
  <div style="position:absolute;left:0;right:0;top:440px;display:flex;justify-content:center;align-items:flex-start;gap:36px">
    <div style="margin-top:70px">${screen(`now-${mode}`, 0.62, t)}</div>
    ${screen(`home-${mode}`, 0.74, t)}
    <div style="margin-top:70px">${screen(`group-${mode}`, 0.62, t)}</div>
  </div>
</div>`;

const tile = (
  t,
  { span, title, text, icon: name, accent = false, big = false, art = "" },
) => {
  const c = accent ? t.accent : t;
  return `<div style="grid-column:span ${span};position:relative;min-height:300px;padding:40px;border-radius:14px;background:${c.tile};color:${c.ink};overflow:hidden">
  <div style="position:relative;z-index:1">${icon(name, c.ink)}
    <h3 style="margin-top:22px;font-size:${big ? 40 : 30}px;font-weight:800;line-height:1.08;letter-spacing:-.025em">${title}</h3>
    <p style="margin-top:12px;max-width:${big ? 560 : 520}px;font-size:18px;line-height:1.45;font-weight:500;color:${c.sub}">${text}</p>
  </div>${art}</div>`;
};

const features = (t) => {
  const tiles = [
    {
      span: 4,
      big: true,
      icon: "airplay",
      title: "AirPlay 2 on every speaker.",
      text: "Play from iPhone, iPad and Mac, straight to any SoundTouch.",
      art: `<div style="position:absolute;right:-30px;bottom:-40px">${model("st10", 300, t.disc)}</div>`,
    },
    {
      span: 2,
      icon: "home",
      title: "In Apple Home.",
      text: "Add your speaker to the Home app as an AirPlay speaker.",
    },
    {
      span: 2,
      icon: "multiroom",
      title: "Multiroom with anything.",
      text: "Every speaker in sync. A Raspberry Pi or any Snapcast player joins too.",
    },
    {
      span: 2,
      icon: "mqtt",
      title: "Home Assistant,<br>built in.",
      text: "MQTT out of the box: power, volume, presets and what’s playing.",
    },
    {
      span: 2,
      icon: "library",
      title: "Your own music.",
      text: "Browse your NAS or media server, or play from BubbleUPnP and VLC.",
    },
    {
      span: 3,
      accent: true,
      icon: "globe",
      title: "No cloud. No account. No box.",
      text: "The speaker runs on its own. Open soundtap.local on any phone and it works like an app.",
    },
    {
      span: 3,
      icon: "update",
      title: "Updates that keep coming.",
      text: "Signed, installed next to the running version and only kept once they work. Bose stopped. We didn’t.",
    },
  ];
  return `<style>${base(t)}</style>
<div style="width:1600px;height:1240px;padding:90px 110px">
  <h2>What SoundTouch never had.</h2>
  <div style="display:grid;grid-template-columns:repeat(6,minmax(0,1fr));gap:20px;margin-top:56px">${tiles.map((x) => tile(t, x)).join("")}</div>
</div>`;
};

const screens = (t, mode) => {
  const items = [
    ["home", "Just for You", "Your stations and music, one tap away."],
    ["now", "Now playing", "The song and its artwork, live."],
    ["group", "Every room", "Group speakers, each at its own volume."],
    [
      "integrations",
      "Your smart home",
      "AirPlay, Apple Home, Spotify and MQTT.",
    ],
    ["sound", "Your sound", "Bass, treble and more, per speaker."],
  ];
  return `<style>${base(t)}</style>
<div style="width:1600px;height:930px;padding:80px 70px 0;background:${t.band}">
  <h2>One app. Every speaker.</h2>
  <p class="sub" style="margin-top:14px;text-align:center;font-size:22px">Every speaker serves the same app. Add it to your home screen, in 25 languages, light or dark.</p>
  <div style="display:flex;justify-content:center;gap:44px;margin-top:56px">${items
    .map(
      ([
        name,
        title,
        text,
      ]) => `<div style="display:flex;flex-direction:column;align-items:center;gap:26px;width:260px">
      ${screen(`${name}-${mode}`, 0.62, t, 22)}
      <div style="text-align:center"><p style="font-size:22px;font-weight:800;letter-spacing:-.02em">${title}</p><p class="sub" style="margin-top:6px;font-size:16px;line-height:1.4">${text}</p></div>
    </div>`,
    )
    .join("")}</div>
</div>`;
};

const speakers = (t) => {
  const models = [
    ["st10", "SoundTouch 10"],
    ["st20", "SoundTouch 20"],
    ["st300", "SoundTouch 300"],
    ["wla", "Wireless Link adapter"],
  ];
  return `<style>${base(t)}</style>
<div style="width:1600px;height:580px;padding-top:84px;text-align:center">
  <h2>For the speaker you already own.</h2>
  <div style="display:flex;justify-content:center;gap:90px;margin-top:60px">${models
    .map(
      ([name, label]) =>
        `<div style="display:flex;flex-direction:column;align-items:center;gap:22px">${model(name, 220, t.disc)}<p style="font-size:22px;font-weight:700;letter-spacing:-.01em">${label}</p></div>`,
    )
    .join("")}</div>
</div>`;
};

const social = (t) => `<style>${base(t)}</style>
<div style="position:relative;width:1280px;height:640px;overflow:hidden">
  <div style="position:absolute;left:90px;top:120px;width:640px">
    <p style="display:flex;align-items:center;gap:10px;font-size:24px;font-weight:700">${logo(36, t.ink)}SoundTap</p>
    <h1 style="margin-top:26px;font-size:58px;line-height:1.04;text-align:left">SoundTouch, the way<br>it should have been.</h1>
    <p class="sub" style="margin-top:24px;font-size:24px;line-height:1.4">AirPlay&nbsp;2, Apple Home and multiroom for the Bose speaker you already own.</p>
  </div>
  <div style="position:absolute;left:780px;top:70px;display:flex;gap:28px;align-items:flex-start">
    ${screen("now-light", 0.54, t, 22)}
    <div style="margin-top:60px">${screen("home-light", 0.54, t, 22)}</div>
  </div>
</div>`;

// name: [width, height, device scale, html]
const pages = { "social-preview": [1280, 640, 1, social(THEMES.light)] };
for (const mode of ["light", "dark"]) {
  const t = THEMES[mode];
  pages[`hero-${mode}`] = [1600, 900, 2, hero(t, mode)];
  pages[`features-${mode}`] = [1600, 1240, 2, features(t)];
  pages[mode === "light" ? "screens" : "screens-dark"] = [
    1600,
    930,
    2,
    screens(t, mode),
  ];
  pages[`speakers-${mode}`] = [1600, 580, 2, speakers(t)];
  // The landing's phone: each of the five screens on its own, at a phone's size, for the
  // switcher in docs/overrides/home.html.
  for (const name of ["home", "now", "group", "integrations", "sound"])
    pages[`screens/${name}-${mode}`] = [
      390,
      844,
      2,
      `<img src="${shot(`${name}-${mode}`)}" style="display:block;width:390px;height:844px">`,
    ];
}

const browser = await chromium.launch();
for (const [name, [w, h, scale, html]] of Object.entries(pages)) {
  const page = await browser.newPage({
    viewport: { width: w, height: h },
    deviceScaleFactor: scale,
  });
  await page.setContent(
    `<!doctype html><html lang="en"><body style="margin:0;width:${w}px;height:${h}px">${html}</body></html>`,
  );
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(300);
  await page.screenshot({ path: `${out}/${name}.png` });
  await page.close();
}
await browser.close();
