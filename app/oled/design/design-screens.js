// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// The ST20 panel design, drawing code
// verbatim except the wordmark. golden.html renders these
// screens in Chromium; app/oled/screen must match them pixel for pixel within a level.
const W = 128, H = 100;
const G = (l) => { const v = Math.round((l / 15) * 255); return `rgb(${v},${v},${v})`; };
const FONT = "'Plus Jakarta Sans', sans-serif";
const PIX = "'Silkscreen', monospace";

function quantize(ctx) {
  const d = ctx.getImageData(0, 0, W, H), a = d.data;
  for (let i = 0; i < a.length; i += 4) {
    const l = 0.2126 * a[i] + 0.7152 * a[i + 1] + 0.0722 * a[i + 2];
    const q = Math.round((l / 255) * 15) * 17;
    a[i] = a[i + 1] = a[i + 2] = q; a[i + 3] = 255;
  }
  ctx.putImageData(d, 0, 0);
}
function text(ctx, s, x, y, { size = 8, weight = 600, level = 15, font = FONT, align = "left", max } = {}) {
  ctx.font = `${weight} ${size}px ${font}`;
  ctx.fillStyle = G(level); ctx.textAlign = align; ctx.textBaseline = "alphabetic";
  if (max) { while (ctx.measureText(s).width > max && s.length > 1) s = s.slice(0, -2) + "…"; }
  ctx.fillText(s, x, y);
}
function textMid(ctx, s, x, cy, opts = {}) {
  const { size = 8, weight = 600, font = FONT } = opts;
  ctx.font = `${weight} ${size}px ${font}`;
  const m = ctx.measureText(s);
  const y = cy + (m.actualBoundingBoxAscent - m.actualBoundingBoxDescent) / 2;
  text(ctx, s, x, Math.round(y), opts);
}
function rect(ctx, x, y, w, h, level) { ctx.fillStyle = G(level); ctx.fillRect(x, y, w, h); }
function rrect(ctx, x, y, w, h, r, level, stroke) {
  ctx.beginPath(); ctx.roundRect(x, y, w, h, r);
  if (stroke) { ctx.strokeStyle = G(level); ctx.lineWidth = 1; ctx.stroke(); } else { ctx.fillStyle = G(level); ctx.fill(); }
}
function iconSpeaker(ctx, x, y, level, waves = 2) {
  ctx.fillStyle = G(level); ctx.beginPath();
  ctx.moveTo(x, y + 3); ctx.lineTo(x + 2.5, y + 3); ctx.lineTo(x + 5.5, y); ctx.lineTo(x + 5.5, y + 9); ctx.lineTo(x + 2.5, y + 6); ctx.lineTo(x, y + 6); ctx.closePath(); ctx.fill();
  ctx.strokeStyle = G(level); ctx.lineWidth = 1;
  for (let i = 0; i < waves; i++) { ctx.beginPath(); ctx.arc(x + 6, y + 4.5, 2.5 + i * 2.2, -0.9, 0.9); ctx.stroke(); }
}
function iconBt(ctx, x, y, level) {
  ctx.strokeStyle = G(level); ctx.lineWidth = 1.1; ctx.lineJoin = "round"; ctx.beginPath();
  ctx.moveTo(x, y + 2.5); ctx.lineTo(x + 5, y + 7); ctx.lineTo(x + 2.5, y + 9.5); ctx.lineTo(x + 2.5, y); ctx.lineTo(x + 5, y + 2.5); ctx.lineTo(x, y + 7); ctx.stroke();
}
function statusBar(ctx, { room = "", src = "wifi", time = "08:42", group = 0 } = {}) {
  let x = 4;
  if (src === "spotify") { text(ctx, "Spotify", 4, 11, { size: 8, weight: 600, level: 8 }); x = 40; }
  else if (src === "bt") { iconBt(ctx, 5, 3, 8); x = 15; } else if (src === "aux") { text(ctx, "AUX", 4, 11, { size: 8, font: PIX, weight: 400, level: 8 }); x = 24; }
  if (room || group) text(ctx, room || ("+" + group), x, 11, { size: 8, weight: 600, level: 8, max: 80 });
  text(ctx, time, 124, 11, { size: 8, weight: 600, level: 8, align: "right" });
  rect(ctx, 4, 16, 120, 1, 2);
}

const S = {
  boot(ctx, t, st = {}) {
    const p = st.bootAt ? (t - st.bootAt) / 2600 : (t % 3200) / 2600;
    text(ctx, "SoundTap", 64, 46, { size: 15, weight: 800, align: "center" });
    text(ctx, "Opstarten…", 64, 59, { size: 8, weight: 500, level: 8, align: "center" });
    rrect(ctx, 24, 72, 80, 4, 2, 3);
    rrect(ctx, 24, 72, Math.max(4, Math.min(1, p) * 80), 4, 2, 15);
  },
  radio(ctx, t, st = {}) {
    statusBar(ctx, st);
    text(ctx, st.station || "NPO Radio 2", 6, 42, { size: 15, weight: 800, max: 116 });
    text(ctx, st.tag || "Er is muziek voor iedereen", 6, 55, { size: 8, weight: 500, level: 8, max: 116 });
    rrect(ctx, 6, 75, 14, 14, 7, 3);
    textMid(ctx, String(st.preset || 1), 13, 82, { size: 8.5, weight: 800, level: 14, align: "center" });
    textMid(ctx, "LIVE", 122, 82, { size: 7, font: PIX, weight: 400, level: 10, align: "right" });
  },
  spotify(ctx, t) {
    statusBar(ctx, { group: 1, src: "spotify" });
    text(ctx, "Sun Goes Down", 6, 40, { size: 13, weight: 800, max: 116 });
    text(ctx, "Robin Schulz · Discover Weekly", 6, 52, { size: 8, weight: 500, level: 8, max: 116 });
    const p = ((t / 1000) % 214) / 214;
    rrect(ctx, 6, 70, 116, 3, 1.5, 3); rrect(ctx, 6, 70, Math.max(3, p * 116), 3, 1.5, 13);
    const s = Math.floor(p * 214);
    text(ctx, Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0"), 6, 84, { size: 7.5, weight: 600, level: 8 });
    text(ctx, "3:34", 122, 84, { size: 7.5, weight: 600, level: 8, align: "right" });
  },
  volume(ctx, t, st = {}) {
    const v = st.vol ?? 32;
    const waves = v === 0 ? 0 : v < 40 ? 1 : 2;
    const iw = 11;
    ctx.save(); ctx.translate(64 - iw, 12); ctx.scale(2, 2); iconSpeaker(ctx, 0, 0, 10, waves); ctx.restore();
    text(ctx, v === 0 ? "—" : String(v), 64, 62, { size: 34, weight: 800, align: "center" });
    const segs = 20;
    for (let i = 0; i < segs; i++) {
      const on = i < Math.round((v / 100) * segs);
      rrect(ctx, 6 + i * 6, 76, 4, 10, 1, on ? 15 : 3);
    }
  },
  mute(ctx) {
    iconSpeaker(ctx, 57, 26, 15, 0);
    ctx.strokeStyle = G(15); ctx.lineWidth = 1.4; ctx.beginPath(); ctx.moveTo(65, 28); ctx.lineTo(71, 34); ctx.moveTo(71, 28); ctx.lineTo(65, 34); ctx.stroke();
    text(ctx, "Gedempt", 64, 60, { size: 13, weight: 800, align: "center" });
  },
  preset(ctx, t, st = {}) {
    text(ctx, "PRESET", 64, 26, { size: 8, font: PIX, weight: 400, level: 7, align: "center" });
    text(ctx, String(st.preset || 3), 64, 64, { size: 40, weight: 800, align: "center" });
    text(ctx, st.station || "Qmusic", 64, 84, { size: 10, weight: 700, level: 12, align: "center", max: 116 });
  },
  bluetooth(ctx, t) {
    statusBar(ctx, { src: "bt", room: "Keuken" });
    ctx.save(); ctx.translate(58, 26); ctx.scale(1.4, 1.4); iconBt(ctx, 0, 0, 15); ctx.restore();
    text(ctx, "Bluetooth", 64, 58, { size: 13, weight: 800, align: "center" });
    text(ctx, "iPhone van Sanne", 64, 71, { size: 8, weight: 500, level: 8, align: "center" });
  },
  holding(ctx, t, st = {}) {
    const p = Math.max(0, Math.min(1, st.holdP ?? 0.6));
    text(ctx, "Opslaan als preset", 64, 22, { size: 9, weight: 700, level: 10, align: "center" });
    text(ctx, String(st.holdN || 3), 64, 66, { size: 40, weight: 800, align: "center" });
    rrect(ctx, 10, 80, 108, 6, 3, 3);
    rrect(ctx, 10, 80, Math.max(6, p * 108), 6, 3, 15);
  },
  saved(ctx) {
    ctx.strokeStyle = G(15); ctx.lineWidth = 3; ctx.lineCap = "round"; ctx.lineJoin = "round";
    ctx.beginPath(); ctx.moveTo(51, 40); ctx.lineTo(60, 49); ctx.lineTo(77, 31); ctx.stroke();
    text(ctx, "Opgeslagen als preset", 64, 72, { size: 9, weight: 700, align: "center" });
  },
  buffering(ctx, t, st = {}) {
    statusBar(ctx, st);
    text(ctx, st.station || "NPO Radio 2", 6, 42, { size: 15, weight: 800, max: 116 });
    text(ctx, st.tag || "Er is muziek voor iedereen", 6, 55, { size: 8, weight: 500, level: 8, max: 116 });
    textMid(ctx, "BUFFEREN", 122, 82, { size: 7, font: PIX, weight: 400, level: 7, align: "right" });
    rrect(ctx, 6, 75, 14, 14, 7, 3);
    textMid(ctx, String(st.preset || 1), 13, 82, { size: 8.5, weight: 800, level: 14, align: "center" });
  },
  aux(ctx) {
    statusBar(ctx, { src: "aux" });
    text(ctx, "AUX", 64, 56, { size: 26, weight: 800, align: "center" });
    text(ctx, "Externe ingang", 64, 72, { size: 8, weight: 500, level: 8, align: "center" });
  },
  pairing(ctx, t) {
    ctx.strokeStyle = G(6); ctx.lineWidth = 1; ctx.beginPath(); ctx.arc(64, 36, 12, 0, Math.PI * 2); ctx.stroke();
    iconBt(ctx, 61, 31, 15);
    text(ctx, "Koppelen…", 64, 70, { size: 12, weight: 800, align: "center" });
    text(ctx, "Zoek naar Keuken", 64, 83, { size: 7.5, weight: 500, level: 8, align: "center" });
  },
  standby(ctx, t, st = {}) {
    const drift = Math.floor(t / 60000) % 4;
    const dx = (drift % 2) * 2 - 1, dy = Math.floor(drift / 2) * 2;
    text(ctx, "08:42", 64 + dx, 54 + dy, { size: 32, weight: 700, level: 10, align: "center" });
    if (st.temp) {
      ctx.font = "700 32px " + FONT;
      const w = ctx.measureText("08:42").width;
      const l = Math.round(64 - w / 2) + dx, r = Math.round(64 + w / 2) + dx;
      text(ctx, "do 25 sep", l, 76 + dy, { size: 9, weight: 600, level: 8 });
      text(ctx, "14°", r, 76 + dy, { size: 9, weight: 700, level: 9, align: "right" });
    } else {
      text(ctx, "donderdag 25 sep", 64 + dx, 76 + dy, { size: 9, weight: 600, level: 8, align: "center" });
    }
  },
  offline(ctx, t) {
    statusBar(ctx, { src: "wifi" });
    text(ctx, "Geen verbinding", 64, 48, { size: 11, weight: 800, align: "center" });
    text(ctx, "Opnieuw proberen", 64, 62, { size: 7.5, weight: 500, level: 8, align: "center" });
    for (let i = 0; i < 3; i++) rect(ctx, 57 + i * 6, 76, 3, 3, Math.floor(t / 300) % 3 === i ? 15 : 4);
  },
};

function draw(canvas, name, t, st) {
  const ctx = canvas.getContext("2d");
  ctx.imageSmoothingEnabled = false;
  rect(ctx, 0, 0, W, H, 0);
  S[name](ctx, t, st);
  quantize(ctx);
}
