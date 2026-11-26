# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Run the actual installer script with a fake DOM, clock and speaker; no LAN access."""
import shutil
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]


@unittest.skipUnless(shutil.which("node"), "browser installer execution requires Node.js")
class BrowserInstallerTest(unittest.TestCase):
    """The browser installer page, run in Node against a fake speaker."""

    def test_installer(self) -> None:
        """The installer page walks a fake speaker through every install step it supports."""
        result = subprocess.run(["node", "--input-type=commonjs", "-"], cwd=ROOT,
                                input=r"""
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const source = fs.readFileSync('docs/installer/index.html', 'utf8').match(/<script>([\s\S]*?)<\/script>/)[1];
// The page reads versions.json beside it; the speaker's requests go to the fetcher under test.
const table = JSON.parse(fs.readFileSync('docs/installer/versions.json', 'utf8'));
const english = JSON.parse(fs.readFileSync('docs/installer/i18n/en.json', 'utf8'));
async function setup(fetcher) {
  let now = 0;
  const elements = {};
  const document = {
    getElementById(id) {
      return elements[id] ||= { value: '', checked: false, textContent: '',
        addEventListener(name, fn) { this[name] = fn; }, append() {} };
    },
    createElement() { return {}; },
  };
  const fetch = (url, init) => url === 'versions.json'
    ? Promise.resolve({ok: true, json: async () => table})
    : url === 'i18n/en.json' ? Promise.resolve({ok: true, json: async () => english}) : fetcher(url, init);
  const ctx = vm.createContext({document, fetch, AbortSignal,
    Date: {now: () => now}, setTimeout(fn, ms) { now += ms; fn(); }});
  vm.runInContext(source, ctx);
  await vm.runInContext('tableLoaded', ctx);
  elements.ip.value = '192.168.1.23';
  elements.model.value = 's0';
  elements.agree.checked = true;
  return {elements, run: code => vm.runInContext(code, ctx)};
}
const version = {api: 1, os: '0.1.0', app: '0.1.0', version: 'app-v0.1.0', updatable: true};
const response = (body, ok = true) => ({ok, json: async () => body});
(async () => {
  // Unsupported or unknown model, missing risk confirmation and public IP never dispatch.
  for (const change of [e => e.model.value = 'u0', e => e.model.value = 'other',
                        e => e.model.value = '', e => e.agree.checked = false,
                        e => e.ip.value = '8.8.8.8']) {
    let calls = 0;
    const h = await setup(async () => { calls++; }); change(h.elements);
    await h.elements.form.submit({preventDefault() {}});
    assert.equal(calls, 0);
  }
  // A preview installs only with its own box ticked, and the tick is for one model: picking
  // another clears it, and a model changed without an event never inherits it.
  if (table.preview.length >= 2) {
    let calls = 0;
    const p = await setup(async () => { calls++; return response(version); });
    p.elements.model.value = 'p0';
    p.elements.model.input();
    assert.equal(p.elements.go.disabled, true);
    p.elements.previewAgree.checked = true; p.elements.previewAgree.change();
    assert.equal(p.elements.go.disabled, false);
    p.elements.model.value = 'p1';
    p.elements.model.input();
    assert.equal(p.elements.previewAgree.checked, false);
    assert.equal(p.elements.go.disabled, true);
    p.elements.previewAgree.checked = true; p.elements.previewAgree.change();
    p.elements.model.value = 'p0';
    await p.elements.form.submit({preventDefault() {}});
    assert.equal(calls, 0);
  }
  // HTTP success, unrelated JSON, HTML, empty versions and errors cannot mean Done.
  for (const body of [{}, {version: '1'}, {...version, os: ''}, {...version, api: '1'}, null]) {
    const h = await setup(async () => response(body));
    await h.run("follow('192.168.1.23')");
    assert.match(h.elements.line.textContent, /did not start or has not finished/);
  }
  for (const fetcher of [async () => response(version, false),
    async () => { throw new Error('CORS or offline'); },
    async () => ({ok: true, json: async () => { throw new Error('HTML'); }})]) {
    const h = await setup(fetcher);
    await h.run("follow('192.168.1.23')");
    assert.doesNotMatch(h.elements.line.textContent, /^Done/);
  }
  // A missed stock poll followed by its return cannot establish a successful reboot.
  let polls = 0;
  const recovering = await setup(async url => {
    if (url.endsWith('/api/version')) return response({});
    if (++polls === 1) throw new Error('temporary outage');
    return response({});
  });
  await recovering.run("follow('192.168.1.23')");
  assert.match(recovering.elements.line.textContent, /did not start or has not finished/);
  // A timed-out POST may have reached the speaker: keep polling, never resend.
  let posts = 0;
  const uncertain = await setup(async (url, init) => {
    if (init.method === 'POST') { posts++; throw new Error('timeout'); }
    return response(version);
  });
  await uncertain.elements.form.submit({preventDefault() {}});
  assert.match(uncertain.elements.line.textContent, /^Done/);
  uncertain.elements.ip.input();
  assert.equal(uncertain.elements.go.disabled, true);
  assert.equal(posts, 1);
  // ADR 0133: a confirmed preview goes to the preview feed, the one that names the preview
  // package; a supported model never does.
  const FEED = 'https://raw.githubusercontent.com/stein155/soundtap/main/updates/soundtouch';
  const posted = (calls) => calls.filter(([, init]) => init && init.method === 'POST').map(([, init]) => init.body);
  if (table.preview.length) {
    const sent = [];
    const p = await setup(async (url, init) => { sent.push([url, init]); return response(version); });
    p.elements.model.value = 'p0';
    p.elements.model.input();
    p.elements.previewAgree.checked = true; p.elements.previewAgree.change();
    await p.elements.form.submit({preventDefault() {}});
    assert.deepEqual(posted(sent), ['<swUpdateStart indexUrl="' + FEED + '-preview"/>']);
  }
  let calls = [];
  const h = await setup(async (url, init) => {
    calls.push([url, init]);
    return response(version);
  });
  await h.elements.form.submit({preventDefault() {}});
  assert.match(h.elements.line.textContent, /^Done/);
  assert.deepEqual(posted(calls), ['<swUpdateStart indexUrl="' + FEED + '"/>']);
  const check = calls.find(([url]) => url.endsWith('/api/version'))[1];
  assert.equal(check.mode, 'cors'); assert.equal(check.credentials, 'omit');
  assert.equal(check.redirect, 'error'); assert.ok(check.signal);
  assert.equal(calls.filter(([, init]) => init.method === 'POST').length, 1);
  // Editing inputs or submitting again cannot dispatch another install while locked.
  h.elements.ip.input();
  assert.equal(h.elements.go.disabled, true);
  await h.elements.form.submit({preventDefault() {}});
  assert.equal(calls.filter(([, init]) => init.method === 'POST').length, 1);
  console.log('installer behavior passed');
})().catch(err => { console.error(err); process.exitCode = 1; });
""", text=True, capture_output=True, check=False)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_language_newest_pick_wins(self) -> None:
        """A slower load of an earlier language pick does not flip the page back from a later one."""
        result = subprocess.run(["node", "--input-type=commonjs", "-"], cwd=ROOT,
                                input=r"""
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const [installer, page] = [...fs.readFileSync('docs/installer/index.html', 'utf8')
  .matchAll(/<script>([\s\S]*?)<\/script>/g)].map((m) => m[1]);
const strings = (lang) => JSON.parse(fs.readFileSync('docs/installer/i18n/' + lang + '.json', 'utf8'));
// Anything the page touches and this test does not look at: callable, constructible, empty
// when iterated, '' as a string, and keeping what is written to it.
const stub = () => new Proxy(function () {}, {
  get(target, key) {
    if (key === Symbol.iterator) return function* () {};
    if (key === Symbol.toPrimitive) return () => '';
    if (key === 'then') return undefined;
    if (!(key in target)) target[key] = stub();
    return target[key];
  },
  set(target, key, value) {
    Object.defineProperty(target, key, {value, writable: true, configurable: true});
    return true;
  },
  apply: () => stub(),
  construct: () => stub(),
});
const elements = {};
const listeners = {};
const document = stub();
document.getElementById = (id) => {
  if (!elements[id]) {
    elements[id] = stub();
    elements[id].addEventListener = (name, fn) => { listeners[id + ':' + name] = fn; };
  }
  return elements[id];
};
const held = {};
const fetch = (url) => {
  if (url === 'versions.json') {
    return Promise.resolve({ok: true, json: async () => ({supported: [], unsupported: [], preview: []})});
  }
  const lang = /^i18n\/(.+)\.json$/.exec(url);
  if (!lang) return new Promise(() => {});
  if (lang[1] === 'en') return Promise.resolve({ok: true, json: async () => strings('en')});
  return new Promise((resolve) => { held[lang[1]] = () => resolve({ok: true, json: async () => strings(lang[1])}); });
};
const ctx = vm.createContext({document, fetch, AbortSignal, setTimeout, window: stub(), history: stub(),
  location: stub(), localStorage: stub(), MutationObserver: stub(), requestAnimationFrame: stub(),
  navigator: {languages: ['en'], language: 'en', userAgent: '', maxTouchPoints: 0}});
vm.runInContext(installer, ctx);
vm.runInContext(page, ctx);
const settle = () => new Promise((r) => setImmediate(r));
(async () => {
  await settle();
  const pick = (lang) => { elements.lang.value = lang; listeners['lang:change'](); };
  pick('de');
  pick('nl');
  held.nl();
  await settle();
  held.de();
  await settle();
  assert.equal(document.documentElement.lang, 'nl');
  assert.equal(elements.lang.value, 'nl');
  assert.equal(document.title, strings('nl')['page.title']);
  console.log('language picks passed');
})().catch(err => { console.error(err); process.exitCode = 1; });
""", text=True, capture_output=True, check=False)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
