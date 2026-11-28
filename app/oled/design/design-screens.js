// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// The ST20 front panel's design (128x100 OLED, 16 greys), drawn on a canvas: the reference
// app/oled/screen is held to (golden.sh renders golden-cases.js with it in Chromium).
//
// Rules: two text sizes, large titles (Montserrat 700, 20 down to 11 px) and small lines
// (600, 11 px, grey 12); tiny labels (LIVE, a preset's number, times, PRESET, CONFIRM) in
// Silkscreen at 8 px, full white; text only at grey 12-15; one thing per screen, a measured
// stack with 8 px gaps, centred; a title takes one line at the largest size that fits,
// then two lines, then an ellipsis; icons say what a word would.

const W = 128, H = 100;
const G = (l) => { const v = Math.round(l / 15 * 255); return `rgb(${v},${v},${v})`; };
const SANS = "'Montserrat','Manrope','Noto Sans Thai','Noto Sans JP','Noto Sans KR','Noto Sans SC','Noto Sans TC'";
const PIX = "'Silkscreen'";
const ease = (x) => (x < 0 ? 0 : x > 1 ? 1 : x < 0.5 ? 2 * x * x : 1 - Math.pow(-2 * x + 2, 2) / 2);
const clamp = (x, a, b) => Math.max(a, Math.min(b, x));

// quant rounds the canvas to the panel's 16 levels.
function quant(c) {
  const d = c.getImageData(0, 0, W, H), a = d.data;
  for (let i = 0; i < a.length; i += 4) {
    const l = 0.2126 * a[i] + 0.7152 * a[i + 1] + 0.0722 * a[i + 2];
    const q = Math.round(l / 255 * 15) * 17;
    a[i] = a[i + 1] = a[i + 2] = q; a[i + 3] = 255;
  }
  c.putImageData(d, 0, 0);
}
// clr starts a screen: black, and every canvas state a screen relies on reset.
function clr(c) {
  c.setTransform(1, 0, 0, 1, 0, 0); c.globalAlpha = 1; c.lineCap = 'butt'; c.lineJoin = 'miter'; c.lineWidth = 1;
  c.fillStyle = '#000'; c.fillRect(0, 0, W, H);
}
function rect(c, x, y, w, h, l) { c.fillStyle = G(l); c.fillRect(x, y, w, h); }
function setF(c, size, weight, font) { c.font = `${weight} ${size}px ${font}`; }
function txt(c, s, x, y, o = {}) {
  const { size = 11, weight = 600, font = SANS, level = 15, align = 'left' } = o;
  setF(c, size, weight, font); c.fillStyle = G(level); c.textAlign = align; c.textBaseline = 'alphabetic'; c.fillText(s, x, y);
}
function pix(c, s, x, y, level = 15, align = 'left') { txt(c, s.toUpperCase(), x, y, { size: 8, weight: 400, font: PIX, level, align }); }
function ell(c, s, max) {
  if (c.measureText(s).width <= max) return s;
  while (s.length > 1 && c.measureText(s + '…').width > max) s = s.slice(0, -1);
  return s + '…';
}
function dots(c, n, on, y) { const w = n * 6 - 2; const x = Math.round(W / 2 - w / 2); for (let i = 0; i < n; i++) rect(c, x + i * 6, y, 4, 2, i === on ? 15 : 5); }

// ---- pixel icons ----
function icon(c, kind, cx, cy, level = 15) {
  c.save(); c.strokeStyle = G(level); c.fillStyle = G(level); c.lineWidth = 2; c.lineCap = 'round'; c.lineJoin = 'round';
  const L = (pts) => { c.beginPath(); pts.forEach(([x, y], i) => (i ? c.lineTo(cx + x, cy + y) : c.moveTo(cx + x, cy + y))); c.stroke(); };
  switch (kind) {
    case 'wifi': for (const r of [13, 8]) { c.beginPath(); c.arc(cx, cy + 6, r, Math.PI * 1.25, Math.PI * 1.75); c.stroke(); } c.beginPath(); c.arc(cx, cy + 5, 2, 0, 7); c.fill(); break;
    case 'wifi-off': icon(c, 'wifi', cx, cy, 6); L([[-10, -8], [10, 10]]); break;
    case 'cable': c.strokeRect(cx - 6.5, cy - 5.5, 13, 9); L([[-3, 4], [-3, 8]]); L([[3, 4], [3, 8]]); L([[0, -6], [0, -11]]); break;
    case 'bt-off': icon(c, 'bt', cx, cy, 6); L([[-9, -9], [9, 9]]); break;
    case 'bt': L([[-5, -5], [5, 5], [0, 10], [0, -10], [5, -5], [-5, 5]]); break;
    case 'check': L([[-8, 0], [-3, 6], [8, -6]]); break;
    case 'cross': L([[-6, -6], [6, 6]]); L([[6, -6], [-6, 6]]); break;
    case 'warn': L([[0, -10], [10, 8], [-10, 8], [0, -10]]); L([[0, -3], [0, 2]]); c.fillRect(cx - 1, cy + 4, 2, 2); break;
    case 'pause': c.fillRect(cx - 4, cy - 4, 3, 9); c.fillRect(cx + 1, cy - 4, 3, 9); break;
    case 'stop': c.fillRect(cx - 4, cy - 4, 8, 8); break;
    case 'mute': L([[-9, -3], [-5, -3], [0, -8], [0, 8], [-5, 3], [-9, 3], [-9, -3]]); L([[4, -4], [10, 4]]); L([[10, -4], [4, 4]]); break;
    case 'lock': c.strokeRect(cx - 6.5, cy - 1.5, 13, 10); c.beginPath(); c.arc(cx, cy - 2, 4.5, Math.PI, 0); c.stroke(); break;
    case 'aux': c.beginPath(); c.arc(cx, cy, 9, 0, 7); c.stroke(); c.beginPath(); c.arc(cx, cy, 3, 0, 7); c.fill(); break;
    case 'skip': L([[-7, -6], [1, 0], [-7, 6], [-7, -6]]); L([[5, -6], [5, 6]]); break;
    case 'group': c.strokeRect(cx - 10.5, cy - 4.5, 8, 12); c.strokeRect(cx + 2.5, cy - 7.5, 8, 15); break;
  }
  c.restore();
}

// ---- measured layout: every block knows its height, the stack is centred ----
function wrap(c, s, w) {
  const words = s.split(' '); const lines = []; let cur = '';
  for (const wd of words) { const t = cur ? cur + ' ' + wd : wd; if (c.measureText(t).width <= w) cur = t; else { if (cur) lines.push(cur); cur = wd; } }
  if (cur) lines.push(cur);
  return lines;
}
// A title that fits none of its sizes (a long compound no wrap can break) tries 13, 12 and
// 11 px before an ellipsis; the design's own texts never need them.
function fitText(c, s, w, sizes, weight, maxLines) {
  for (let n = 1; n <= maxLines; n++) for (const sz of sizes) { setF(c, sz, weight, SANS); const l = wrap(c, s, w); if (l.length <= n && l.every((x) => c.measureText(x).width <= w)) return { sz, lines: l }; }
  const sz = sizes[sizes.length - 1];
  if (weight === 700) for (const r of [13, 12, 11]) { if (r >= sz) continue; setF(c, r, weight, SANS); const l = wrap(c, s, w); if (l.length <= maxLines && l.every((x) => c.measureText(x).width <= w)) return { sz: r, lines: l }; }
  setF(c, sz, weight, SANS); let l = wrap(c, s, w);
  if (l.length > maxLines) { l = l.slice(0, maxLines); l[maxLines - 1] = ell(c, l[maxLines - 1] + ' …', w); }
  return { sz, lines: l.map((x) => ell(c, x, w)) };
}
function flow(c, items, top = 0, bottom = 100) {
  const gap = 8;
  const m = items.map((it) => {
    if (it.t === 'text') { const f = fitText(c, it.s, it.w ?? 120, it.sizes ?? [18, 16, 14], it.weight ?? 700, it.max ?? 2); const lh = Math.round(f.sz * 1.15); return { ...it, f, lh, h: f.lines.length * lh }; }
    if (it.t === 'icon') return { ...it, h: 22 };
    if (it.t === 'dots') return { ...it, h: 2 };
    return { ...it, h: it.h ?? 0 };
  });
  const total = m.reduce((a, b) => a + b.h, 0) + gap * (m.length - 1);
  let y = Math.round(top + (bottom - top - total) / 2);
  for (const it of m) {
    if (it.t === 'text') it.f.lines.forEach((l, i) => txt(c, l, 64 + (it.dx ?? 0), Math.round(y + it.lh * (i + 0.8)), { size: it.f.sz, weight: it.weight ?? 700, level: it.level ?? 15, align: 'center' }));
    else if (it.t === 'icon') icon(c, it.kind, 64, y + 11, it.level ?? 15);
    else if (it.t === 'dots') dots(c, it.n, it.on, y);
    else if (it.draw) it.draw(y);
    y += it.h + gap;
  }
}
const T = (s, o = {}) => ({ t: 'text', s, ...o });
const S = (s, o = {}) => ({ t: 'text', s, sizes: [11], weight: 600, level: 12, ...o });

// ---- screens ----
function notice(c, ic, title, line, opt = {}) {
  clr(c); const it = [];
  if (ic) it.push({ t: 'icon', kind: ic, level: opt.iconLevel ?? 15 });
  it.push(T(title, { sizes: opt.sizes ?? [18, 16, 14] }));
  if (line) it.push(S(line));
  if (opt.dots) it.push({ t: 'dots', n: 3, on: opt.dots - 1 });
  flow(c, it, 2, 98); quant(c);
}
function ring(c, p, cy = 38, r = 21) {
  c.lineCap = 'round'; c.lineWidth = 3; c.strokeStyle = G(4); c.beginPath(); c.arc(64, cy, r, 0, 7); c.stroke();
  if (p > 0) { c.strokeStyle = G(15); c.beginPath(); c.arc(64, cy, r, -Math.PI / 2, -Math.PI / 2 + Math.min(1, p) * 2 * Math.PI); c.stroke(); }
}
function countdown(c, n, p, label) {
  clr(c); ring(c, p); txt(c, String(n), 64, 47, { size: 24, weight: 800, align: 'center' });
  flow(c, [T(label, { sizes: [14, 13, 12, 11], max: 1 })], 64, 98); quant(c);
}
function holding(c, n, p, label) {
  clr(c); ring(c, p); txt(c, String(n), 64, 47, { size: 24, weight: 800, align: 'center' });
  flow(c, [T(label, { sizes: [14, 13, 12] })], 62, 100); quant(c);
}
function saved(c, label) { clr(c); ring(c, 1); icon(c, 'check', 64, 38); flow(c, [T(label, { sizes: [14] })], 62, 100); quant(c); }
// corner is what a playing screen keeps in its top corners: pause or stop, a preset's
// number, a badge, a stereo pair's side.
function corner(c, o) {
  // 4 px in from the edges, so the burn-in orbit's pixel never takes a mark to the edge
  if (o.state === 'pause') icon(c, 'pause', 10, 9);
  else if (o.state === 'stop') icon(c, 'stop', 10, 9);
  else if (o.preset) { pix(c, String(o.preset), 7, 12); c.strokeStyle = G(15); c.lineWidth = 1; c.strokeRect(4.5, 4.5, 10, 10); }
  if (o.badge) pix(c, o.badge, 121, 12, o.badgeLevel ?? 15, 'right');
  if (o.mark) { pix(c, o.mark, 118, 12, 15, 'center'); c.strokeStyle = G(15); c.lineWidth = 1; c.strokeRect(112.5, 4.5, 11, 10); }
}
function station(c, o = {}) {
  clr(c); corner(c, o);
  const it = [T(o.name, { sizes: [20, 18, 16, 14] })];
  if (o.warn) it.push({ t: 'icon', kind: 'warn' });
  if (o.line) it.push(S(o.line));
  flow(c, it, 12, o.dots !== undefined ? 92 : 98);
  if (o.dots !== undefined) dots(c, 2, o.dots, 95);
  quant(c);
}
function radioNow(c, title, artist) { clr(c); flow(c, [T(title, { sizes: [20, 18, 16] }), S(artist)], 4, 92); dots(c, 2, 1, 95); quant(c); }
const clockText = (x) => `${Math.floor(x / 60)}:${String(Math.floor(x % 60)).padStart(2, '0')}`;
function song(c, o = {}) {
  clr(c); corner(c, o);
  const bar = o.len > 0;
  flow(c, [T(o.title, { sizes: [20, 18, 16, 14] }), S(o.artist, { max: 1 })], o.mark || o.state ? 12 : 2, bar ? 72 : 98);
  if (bar) {
    const p = Math.min(1, o.at / o.len);
    rect(c, 4, 76, 120, 3, 4); rect(c, 4, 76, Math.round(120 * p), 3, 15);
    pix(c, clockText(o.at), 4, 92); pix(c, clockText(o.len), 124, 92, 15, 'right');
  }
  quant(c);
}
function vol(c, v) {
  clr(c); txt(c, String(v), 64, 56, { size: 44, weight: 800, align: 'center' });
  const n = Math.round(v / 5); for (let i = 0; i < 20; i++) rect(c, 4 + i * 6, 72, 4, 12, i < n ? 15 : 3); quant(c);
}
// fit and block are the one-shot title of the preset and dimmed screens: the largest size
// whose wrap takes at most two lines, centred on cy.
function fit(c, s, w, sizes, weight) {
  for (const sz of sizes) { setF(c, sz, weight, SANS); const lines = wrap(c, s, w); if (lines.length <= 2 && lines.every((l) => c.measureText(l).width <= w)) return { sz, lines }; }
  const sz = sizes[sizes.length - 1]; setF(c, sz, weight, SANS); return { sz, lines: [ell(c, s, w)] };
}
function block(c, s, cx, cy, w, o = {}) {
  const { sizes = [18, 16], level = 15, weight = 700 } = o; const f = fit(c, s, w, sizes, weight);
  const lh = Math.round(f.sz * 1.12); const top = cy - (f.lines.length * lh) / 2;
  f.lines.forEach((l, i) => txt(c, l, cx, Math.round(top + lh * (i + 0.78)), { size: f.sz, weight, level, align: 'center' }));
}
function preset(c, word, n, name) { clr(c); pix(c, word, 64, 12, 15, 'center'); txt(c, String(n), 64, 58, { size: 46, weight: 800, align: 'center' }); block(c, name, 64, 82, 120, { sizes: [16] }); quant(c); }
function presetEmpty(c, n, word) { clr(c); txt(c, String(n), 64, 52, { size: 40, weight: 800, align: 'center', level: 6 }); block(c, word, 64, 80, 120, { sizes: [16] }); quant(c); }
function ambient(c, title, dx, dy) { clr(c); block(c, title, 64 + dx, 52 + dy, 112, { sizes: [16, 14], level: 8 }); quant(c); }
function clock(c, s, ampm, dx, dy) {
  clr(c); const y = (ampm ? 54 : 60) + dy;
  txt(c, s, 64 + dx, y, { size: 30, weight: 600, level: 7, align: 'center' });
  if (ampm) txt(c, ampm, 64 + dx, y + 14, { size: 11, weight: 600, level: 7, align: 'center' });
  quant(c);
}
function progress(c, title, pct, line) {
  clr(c);
  flow(c, [T(title, { sizes: [14, 13, 12], max: 1 }), T(pct + '%', { sizes: [22], weight: 800, max: 1 }),
    { h: 4, draw: (y) => { rect(c, 14, y, 100, 4, 4); rect(c, 14, y, pct, 4, 15); } }, S(line, { max: 1 })], 0, 100);
  quant(c);
}
function setupPage(c, pg, step, value, sub) {
  clr(c);
  txt(c, step, 64, 14, { size: 11, weight: 700, align: 'center' });
  if (pg === 0) block(c, value, 64, 46, 120, { sizes: [16, 14] });
  else { txt(c, value, 64, 52, { size: 17, weight: 800, align: 'center' }); txt(c, sub, 64, 72, { size: 11, weight: 600, level: 12, align: 'center' }); }
  dots(c, 2, pg, 90); quant(c);
}
function confirm(c, label, title, sub, on) {
  clr(c); pix(c, label, 64, 12, 15, 'center');
  flow(c, [T(title, { sizes: [20, 18, 16, 14] }), S(sub), { t: 'dots', n: 3, on }], 16, 100); quant(c);
}

// ---- the SoundTap mark (docs/assets/logo.svg: open head, stem, grille holes), 256 units ----
const HOLES = [[152, 76, 15], [168, 100, 15], [176, 128, 15], [172, 48, 9], [196, 76, 9], [210, 108, 9], [214, 142, 9]];
// boot is the power-on screen u seconds after the bar filled: u < 0 is the bar alone at
// progress p, outline grey 6. Then the bar slides into the note's stem, the stem grows, the
// head draws round, the holes appear near ones first, "SoundTap" comes under it; the panel
// fades out from u 3.6 to 4.0 (a contrast fade on the speaker, not drawn here).
function boot(c, p, u) {
  clr(c); const bx = 32, by = 48, bw = 64, bh = 5;
  if (u < 0) {
    c.strokeStyle = G(6); c.lineWidth = 1; c.beginPath(); c.roundRect(bx + 0.5, by + 0.5, bw - 1, bh - 1, 2); c.stroke();
    if (p > 0) { c.fillStyle = G(15); c.beginPath(); c.roundRect(bx + 1, by + 1, Math.max(3, (bw - 2) * p), bh - 2, 1.5); c.fill(); }
    quant(c); return;
  }
  const s = 0.25, ox = 32, oy = 4; const X = (x) => ox + x * s, Y = (y) => oy + y * s;
  const sx = X(124), sTop = Y(60), sBot = Y(176), lw = 4;
  c.fillStyle = G(15); c.strokeStyle = G(15); c.lineCap = 'round';
  if (u < 0.35) {
    const k = ease(u / 0.35); const w = bw + (lw - bw) * k; const x = bx + (sx - lw / 2 - bx) * k;
    c.beginPath(); c.roundRect(x, by + (sBot - 2 - by) * k, w, bh - 1 * k, 2); c.fill(); quant(c); return;
  }
  const g = clamp((u - 0.35) / 0.4, 0, 1); const top = sBot - (sBot - sTop) * ease(g);
  c.lineWidth = lw; c.beginPath(); c.moveTo(sx, sBot); c.lineTo(sx, top); c.stroke();
  const h = clamp((u - 0.75) / 0.35, 0, 1);
  if (h > 0) { c.beginPath(); c.arc(X(96), Y(176), 28 * s, 0, h * 2 * Math.PI); c.stroke(); }
  HOLES.forEach(([x, y, l], i) => { const a = clamp((u - 1.1 - i * 0.12) / 0.12, 0, 1); if (a > 0) { c.fillStyle = G(Math.round(l * a)); c.beginPath(); c.arc(X(x), Y(y), 2.2, 0, 7); c.fill(); } });
  const n = clamp((u - 2.0) / 0.4, 0, 1);
  if (n > 0) txt(c, 'SoundTap', 64, 90, { size: 14, weight: 700, align: 'center', level: Math.round(15 * n) });
  quant(c);
}
// HELLO is the network card screen's words: the speaker's language is often not known yet
// (the first install), so it greets and says it is almost done in every language in turn.
const HELLO = [['Hallo!', 'Bijna klaar'], ['Hello!', 'Almost done'], ['Bonjour !', 'Presque fini'], ['¡Hola!', 'Casi listo'], ['Ciao!', 'Quasi fatto'], ['Hej!', 'Nästan klart'], ['Hei!', 'Melkein valmis'], ['Olá!', 'Quase pronto'], ['Ahoj!', 'Skoro hotovo'], ['Szia!', 'Mindjárt kész'], ['Cześć!', 'Prawie gotowe'], ['Salut!', 'Aproape gata'], ['Привет!', 'Почти готово'], ['Živjo!', 'Skoraj končano'], ['Merhaba!', 'Neredeyse bitti'], ['Γειά σου!', 'Σχεδόν έτοιμο'], ['こんにちは！', 'もうすぐ完了'], ['안녕하세요!', '거의 다 됐어요'], ['สวัสดี!', 'ใกล้เสร็จแล้ว'], ['你好！', '快好了'], ['您好！', '快好了']];
// netcard is the BCO app's write: the small mark hops 2 px on a 0.8 s beat, its holes ripple
// outward (near row, then far row, 1.6 s), a greeting and "almost done" a language every
// 2.4 s, fading in and out over the first and last 10 % of its slot, and a thin bar at the bottom.
function netcard(c, t, pct) {
  clr(c);
  const beat = (t % 0.8) / 0.8; const hop = Math.round(Math.max(0, Math.sin(beat * Math.PI * 2)) * 2);
  const s = 0.16, ox = 64 - 140 * s, oy = 2 - hop; const X = (x) => ox + x * s, Y = (y) => oy + y * s;
  c.strokeStyle = G(15); c.lineCap = 'round'; c.lineWidth = 3;
  c.beginPath(); c.moveTo(X(124), Y(176)); c.lineTo(X(124), Y(60)); c.stroke();
  c.beginPath(); c.arc(X(96), Y(176), 28 * s, 0, 7); c.stroke();
  const ph = (t % 1.6) / 1.6;
  HOLES.forEach(([x, y, base], i) => {
    const ringN = i < 3 ? 0 : 1; const d = ph - ringN * 0.25 - (i % 3) * 0.05;
    const glow = Math.max(0, 1 - Math.abs(d - 0.2) * 4);
    c.fillStyle = G(Math.round(4 + (base - 4) * glow)); c.beginPath(); c.arc(X(x), Y(y), 1.8, 0, 7); c.fill();
  });
  const SLOT = 2.4; const [w, sub2] = HELLO[Math.floor(t / SLOT) % HELLO.length]; const k = (t % SLOT) / SLOT;
  const a = k < 0.1 ? k / 0.1 : k > 0.9 ? (1 - k) / 0.1 : 1;
  flow(c, [T(w, { sizes: [20, 18], max: 1, level: Math.round(15 * a) }), S(sub2, { max: 1, level: Math.round(12 * a) })], 36, 95);
  rect(c, 14, 96, 100, 2, 4); rect(c, 14, 96, pct, 2, 15);
  quant(c);
}
