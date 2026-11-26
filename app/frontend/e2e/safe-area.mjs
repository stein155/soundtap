// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// A phone's notch, status bar and home indicator for a browser that has none: Chromium and
// WebKit in Playwright report env(safe-area-inset-*) as 0, so every one in the page's CSS
// (index.html, the stylesheet, styled-components' rules) is rewritten to a fixed value
// before it applies. Then audit() reads what the page drew into those bands.
/* global document, window, getComputedStyle, innerWidth, innerHeight, CSSStyleSheet, MutationObserver */

const ENV =
  'env\\(\\s*safe-area-inset-(top|bottom|left|right)\\s*(?:,[^()]*(?:\\([^()]*\\)[^()]*)*)?\\)';

// In the page, before its scripts: rules inserted later get the insets too.
function patchStyles({ ins, src }) {
  const re = new RegExp(src, 'gu');
  const rw = (css) =>
    typeof css === 'string' ? css.replace(re, (_, side) => `${ins[side] ?? 0}px`) : css;
  const original = CSSStyleSheet.prototype.insertRule;
  CSSStyleSheet.prototype.insertRule = function insertRule(rule, i) {
    return original.call(this, rw(rule), i);
  };
  // Text in <style> elements: styled-components' global styles and its development mode.
  const fix = (node) => {
    if (node.nodeType === 3 && node.parentNode?.nodeName === 'STYLE') {
      const v = rw(node.data);
      if (v !== node.data) node.data = v;
    } else if (node.nodeName === 'STYLE') node.childNodes.forEach(fix);
  };
  new MutationObserver((list) => {
    for (const m of list) {
      if (m.type === 'characterData') fix(m.target);
      m.addedNodes.forEach(fix);
      if (m.target.nodeName === 'STYLE') m.target.childNodes.forEach(fix);
    }
  }).observe(document, { subtree: true, childList: true, characterData: true });
  window.fakeSafeAreaInsets = ins;
}

export async function fakeInsets(page, ins) {
  const re = new RegExp(ENV, 'gu');
  await page.route('**/*', async (route) => {
    const req = route.request();
    if (!['document', 'stylesheet'].includes(req.resourceType())) return route.fallback();
    const res = await route.fetch();
    const body = (await res.text()).replace(re, (_, side) => `${ins[side] ?? 0}px`);
    await route.fulfill({ response: res, body });
  });
  await page.addInitScript(patchStyles, { ins, src: ENV });
}

// The rules the rewrite missed: none may still ask env() for an inset.
export function leftoverInsets(page) {
  return page.evaluate(() => {
    const out = [];
    const walk = (rules) => {
      for (const r of rules) {
        if (r.cssRules) walk(r.cssRules);
        else if (r.cssText.includes('safe-area-inset')) out.push(r.cssText.slice(0, 120));
      }
    };
    for (const sheet of document.styleSheets) walk(sheet.cssRules);
    return out;
  });
}

// What is checked: controls, headings and the text of every leaf element.
const CONTROLS =
  'button,a[href],input:not([type=hidden]),select,textarea,[role=button],[role=link],[role=slider],[role=switch],[role=radio],[role=checkbox],[role=option],[role=tab]';

const shown = (el) =>
  el.checkVisibility({ opacityProperty: true, visibilityProperty: true }) &&
  !el.closest('[aria-hidden="true"],[inert]');

// In the page. band 'top' checks the top inset as the screen opens; 'bottom' first scrolls
// every scroller to its end, then checks the bottom inset. The docked speaker bar reaches
// under the home indicator by design and is left to bars().
function audit(band) {
  const ins = window.fakeSafeAreaInsets;
  const H = innerHeight;
  document.documentElement.style.scrollBehavior = 'auto';
  if (band === 'bottom') {
    for (const el of document.querySelectorAll('*')) {
      if (/auto|scroll/u.test(getComputedStyle(el).overflowY)) {
        el.style.scrollBehavior = 'auto';
        el.scrollTop = el.scrollHeight;
      }
    }
  }
  const [from, to] = band === 'top' ? [-1, ins.top] : [H - ins.bottom, H + 1];
  const found = new Set();
  if (!ins[band]) return [];
  for (const [el, kind] of targets()) {
    if (!shown(el) || (band === 'bottom' && el.closest('.spkbar-wrap'))) continue;
    const box = clip(el, kind === 'control' ? el.getBoundingClientRect() : textBox(el));
    if (box && onTopWithin(el, box, from, to)) found.add(`${kind} ${describe(el)}`);
  }
  return [...found];
}

function targets() {
  const out = [];
  for (const el of document.querySelectorAll(CONTROLS)) out.push([el, 'control']);
  for (const el of document.querySelectorAll('body *')) {
    if (el.children.length || !el.textContent.trim() || el.closest('script,style')) continue;
    out.push([el, 'text']);
  }
  return out;
}

function textBox(el) {
  const range = document.createRange();
  range.selectNodeContents(el);
  return range.getBoundingClientRect();
}

// The part of a box its scrolling and clipping ancestors leave on screen.
function clip(el, r) {
  let [x1, y1, x2, y2] = [r.left, r.top, r.right, r.bottom];
  for (let a = el.parentElement; a && a !== document.documentElement; a = a.parentElement) {
    const cs = getComputedStyle(a);
    const ar = a.getBoundingClientRect();
    if (cs.overflowX !== 'visible') [x1, x2] = [Math.max(x1, ar.left), Math.min(x2, ar.right)];
    if (cs.overflowY !== 'visible') [y1, y2] = [Math.max(y1, ar.top), Math.min(y2, ar.bottom)];
    if (cs.position === 'fixed') break;
  }
  return x2 - x1 > 0.5 && y2 - y1 > 0.5 ? { x1, y1, x2, y2 } : null;
}

// Whether the element itself, not something drawn over it, shows somewhere in [from, to].
function onTopWithin(el, b, from, to) {
  const lo = Math.max(b.y1, from);
  const hi = Math.min(b.y2, to);
  for (let y = lo + 0.25; y < hi; y += 3) {
    for (const f of [0.1, 0.3, 0.5, 0.7, 0.9]) {
      const hit = document.elementFromPoint(b.x1 + (b.x2 - b.x1) * f, y);
      if (hit && (hit === el || el.contains(hit) || hit.contains(el))) return true;
    }
  }
  return false;
}

function describe(el) {
  const label = el.getAttribute('aria-label') || el.textContent || '';
  return `${el.tagName.toLowerCase()} "${label.trim().replace(/\s+/gu, ' ').slice(0, 40)}"`;
}

// Whatever reaches past the sides of the screen, the page's own scroll width included.
function sides() {
  const W = innerWidth;
  const out = [];
  if (document.documentElement.scrollWidth > W + 1) out.push('the page scrolls sideways');
  for (const el of document.querySelectorAll('body *')) {
    if (!shown(el)) continue;
    const b = clip(el, el.getBoundingClientRect());
    if (b && (b.x2 > W + 1 || b.x1 < -1) && b.y2 > 0 && b.y1 < innerHeight)
      out.push(`${describe(el)} ${Math.round(b.x1)}..${Math.round(b.x2)}`);
  }
  return out.slice(0, 5);
}

// The docked speaker bar: under its 64 px of content, max(0, inset - 16px) and no more.
function bars() {
  return [...document.querySelectorAll('.spkbar')]
    .filter((el) => shown(el) && el.getBoundingClientRect().top < innerHeight)
    .map((el) => {
      const r = el.getBoundingClientRect();
      return {
        pad: parseFloat(getComputedStyle(el).paddingBottom),
        // + 0: a bar a fraction of a pixel low rounds to -0, which toEqual tells from 0.
        gap: Math.round(innerHeight - r.bottom) + 0,
        content: Math.round(r.height - parseFloat(getComputedStyle(el).paddingBottom)),
      };
    });
}

const IN_PAGE = [audit, targets, textBox, clip, onTopWithin, describe, sides, bars].map(String);

// Loads the audit into the page once per document.
export async function installAudit(page) {
  await page.addInitScript({
    content: `const CONTROLS = ${JSON.stringify(CONTROLS)};
const shown = ${shown.toString()};
${IN_PAGE.join('\n')}
window.safeAreaAudit = { audit, sides, bars };`,
  });
}

export const inBand = (page, band) => page.evaluate((b) => window.safeAreaAudit.audit(b), band);
export const pastSides = (page) => page.evaluate(() => window.safeAreaAudit.sides());
export const speakerBars = (page) => page.evaluate(() => window.safeAreaAudit.bars());
