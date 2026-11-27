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
        addEventListener(name, fn) { this[name] = fn; },
        append(...xs) { for (const x of xs) this.textContent += typeof x === 'string' ? x : (x.textContent || ''); } };
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
// A page with supported model i chosen, for what it derives from the model alone.
let h0Pages = {};
const h0 = (i) => ({run: async (code) => {
  h0Pages[i] ||= await setup(async () => response({}));
  h0Pages[i].elements.model.value = 's' + i;
  return h0Pages[i].run(code);
}});
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
  // A SoundTouch 20 rewrites its network chip and restarts again: it gets twenty minutes, and
  // while it restarts the owner is told it may come back at another address.
  const st20 = table.supported.findIndex((rule) => rule.type === 'SoundTouch 20');
  assert.ok(st20 >= 0);
  for (const [pick, minutes] of [['s' + st20, 20], ['s0', 10]]) {
    const h = await setup(async () => response({}));
    h.elements.model.value = pick;
    await h.run("follow('192.168.1.23')");
    assert.match(h.elements.detail.textContent, new RegExp('after ' + minutes + ' minutes'));
  }
  const moving = await setup(async () => { throw new Error('restarting'); });
  moving.elements.model.value = 's' + st20;
  await moving.run("follow('192.168.1.23')");
  assert.match(moving.elements.detail.textContent, /another address/);
  // A speaker that comes back at another address is found by a sweep of the /24: SoundTap
  // speakers already there during the install (.50) are the baseline, the new one (.77) is it.
  let stockPolls = 0, asked77 = 0;
  const moved = await setup(async (url) => {
    if (url === 'http://192.168.1.50/api/version') return response(version);
    if (url === 'http://192.168.1.77/api/version') {
      if (++asked77 > 1) return response({...version, app: '9.9.9'});
      throw new Error('nothing there yet');
    }
    if (url === 'http://192.168.1.23:8090/info' && ++stockPolls < 8) return response({});
    throw new Error('nothing');
  });
  moved.elements.model.value = 's' + st20;
  await moved.run("follow('192.168.1.23')");
  assert.match(moved.elements.line.textContent, /^Done/);
  assert.match(moved.elements.detail.textContent, /new address: 192\.168\.1\.77\. SoundTap Core 0\.1\.0 · app 9\.9\.9\./);
  assert.doesNotMatch(moved.elements.detail.textContent, /192\.168\.1\.50/);
  // The upload page on the setup network: 192.168.1.1 on a SoundTouch 20, 192.0.2.1 on the others.
  assert.equal(await h0(st20).run('uploadAddress()'), 'http://192.168.1.1:17008/update.html');
  for (const i of table.supported.keys()) {
    if (i !== st20) assert.equal(await h0(i).run('uploadAddress()'), 'http://192.0.2.1:17008/update.html');
  }
  // Safari, every iPhone and iPad browser, and Firefox cannot reach the speaker from this page:
  // the page sends them to the route without internet. Chrome and Edge can.
  const page = await setup(async () => response({}));
  const browsers = [
    ['Mozilla/5.0 (iPad; CPU OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1', 5, true],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15', 5, true],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15', 0, true],
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/131.0 Mobile/15E148 Safari/604.1', 5, true],
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0', 0, true],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36', 0, false],
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0', 0, false],
    ['Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36', 5, false],
  ];
  for (const [ua, touch, blocked] of browsers) {
    assert.equal(page.run('lanBlocked(' + JSON.stringify(ua) + ', ' + touch + ')'), blocked, ua);
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
