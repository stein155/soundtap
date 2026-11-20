// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// Composes the README art (hero, screens, social preview) from the screenshots made by
// shoot.mjs. Reads /shots, /dist (for the Montserrat font) and /out/logo.svg; writes PNGs to /out.
import { chromium } from 'playwright';
import { readFileSync, readdirSync } from 'node:fs';

const shots = process.argv[2] ?? '/shots';
const out = '/out';
const font = readdirSync('/dist/assets').find((f) => f.startsWith('montserrat-latin-wght'));
const fontB64 = readFileSync(`/dist/assets/${font}`).toString('base64');
const logo = `data:image/svg+xml;base64,${readFileSync(`${out}/logo.svg`).toString('base64')}`;
const img = (name) => `data:image/png;base64,${readFileSync(`${shots}/${name}.png`).toString('base64')}`;

const THEMES = {
  light: { bg: 'linear-gradient(135deg,#fafafa,#e9e9e9)', ink: '#111', sub: '#4a4a4a', chip: '#fff', chipInk: '#111', frame: '#111', shadow: '0 30px 60px -20px rgba(0,0,0,.35)' },
  dark: { bg: 'linear-gradient(135deg,#1a1a1a,#0a0a0a)', ink: '#fff', sub: '#b4b4b4', chip: '#262628', chipInk: '#fff', frame: '#3a3a3c', shadow: '0 30px 60px -20px rgba(0,0,0,.8)' },
};

const css = (t) => `
@font-face{font-family:M;src:url(data:font/woff2;base64,${fontB64});font-weight:100 900}
*{box-sizing:border-box;margin:0}
body{font-family:M,sans-serif;color:${t.ink};background:${t.bg};position:relative;overflow:hidden}
.phone{position:absolute;border:solid ${t.frame};border-radius:12% / 5.5%;overflow:hidden;box-shadow:${t.shadow};background:#000}
.phone img{display:block;width:100%;height:100%}
.brand{display:flex;align-items:center;gap:22px}
.brand img{width:92px;height:92px;border-radius:22px}
.brand b{font-size:84px;font-weight:700;letter-spacing:-.03em}
h1{font-weight:700;letter-spacing:-.02em;line-height:1.1}
p{color:${t.sub};line-height:1.5}
.chips{display:flex;gap:10px;flex-wrap:wrap}
.chips span{background:${t.chip};color:${t.chipInk};padding:12px 20px;border-radius:999px;font-size:22px;font-weight:600;box-shadow:0 1px 3px rgba(0,0,0,.15)}
.cap{position:absolute;text-align:center;width:340px}
.cap b{display:block;font-size:28px;font-weight:700;letter-spacing:-.01em}
.cap span{font-size:20px;color:${t.sub}}
`;

const phone = (src, x, y, w, rot = 0) =>
  `<div class="phone" style="left:${x}px;top:${y}px;width:${w}px;height:${w * 2.164}px;border-width:${w * 0.045}px;transform:rotate(${rot}deg)"><img src="${src}"></div>`;

const pages = {};
for (const mode of ['light', 'dark']) {
  const t = THEMES[mode];
  pages[`hero-${mode}`] = {
    w: 1600, h: 820,
    html: `<style>${css(t)}</style>
<div class="brand" style="position:absolute;left:110px;top:120px"><img src="${logo}"><b>SoundTap</b></div>
<h1 style="position:absolute;left:110px;top:270px;font-size:56px;width:640px">A second life for your Bose SoundTouch.</h1>
<p style="position:absolute;left:110px;top:445px;font-size:26px;width:620px">Open firmware, rebuilt from scratch: our own bootloader, mainline Linux and a player that needs no cloud, no account and no app.</p>
<div class="chips" style="position:absolute;left:110px;top:640px;width:720px"><span>Radio</span><span>Spotify Connect</span><span>AirPlay 2</span><span>Multiroom</span></div>
${phone(img(`home-${mode}`), 815, 150, 270, -5)}
${phone(img(`spotify-${mode}`), 1305, 150, 270, 5)}
${phone(img(`now-${mode}`), 1040, 50, 310)}`,
  };
}

const caps = [
  ['home-light', 'Just for You', 'your stations and music'],
  ['presets-light', 'Presets', 'one tap, six favourites'],
  ['now-light', 'Now playing', 'live song and artwork'],
  ['spotify-dark', 'Spotify', 'playlists, artists, songs'],
  ['settings-dark', 'Light or dark', 'in 25 languages'],
];
pages.screens = {
  w: 1600, h: 760,
  html: `<style>${css(THEMES.light)}</style>${caps
    .map(([s, a, b], i) => {
      const x = 70 + i * 300;
      return `${phone(img(s), x, 50, 260)}<div class="cap" style="left:${x - 20}px;width:300px;top:640px"><b>${a}</b><span>${b}</span></div>`;
    })
    .join('')}`,
};

pages['social-preview'] = {
  w: 1280, h: 640,
  html: `<style>${css(THEMES.light)}</style>
<div class="brand" style="position:absolute;left:90px;top:110px"><img src="${logo}"><b>SoundTap</b></div>
<h1 style="position:absolute;left:90px;top:250px;font-size:50px;width:600px">A second life for your Bose SoundTouch.</h1>
<p style="position:absolute;left:90px;top:420px;font-size:24px;width:560px">Open firmware: no cloud, no account, no app.</p>
${phone(img('now-light'), 760, 80, 250, -4)}
${phone(img('spotify-dark'), 1000, 120, 250, 4)}`,
};

const browser = await chromium.launch();
for (const [name, { w, h, html }] of Object.entries(pages)) {
  const page = await browser.newPage({ viewport: { width: w, height: h } });
  await page.setContent(`<body style="width:${w}px;height:${h}px">${html}</body>`);
  await page.waitForTimeout(500);
  await page.screenshot({ path: `${out}/${name}.png` });
}
await browser.close();
