# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""docs/installer/versions.json is the one table of models and Bose software every installer
applies: install.sh, install.ps1 and the browser installer run here against the cases the
table itself yields (each model at, below and above its minimum, the unsupported models, an
unknown one) and must give the same verdict as the reference below, with the same words."""
import json
import os
import re
import shutil
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
INSTALLER = ROOT / "docs/installer"
TABLE_TEXT = (INSTALLER / "versions.json").read_text()
TABLE = json.loads(TABLE_TEXT)
VERSION = re.compile(r"^[0-9]+(\.[0-9]+)*$")


def numbers(version):
    return [int(n) for n in version.split(".")]


def matches(rule, type_, module):
    return ((not rule.get("type") or type_ == rule["type"])
            and (not rule.get("typeContains") or rule["typeContains"] in type_)
            and (not rule.get("moduleType") or module == rule["moduleType"]))


def reference(type_, module, software):
    """(status, rule): unsupported rules first, then the supported ones, else unknown."""
    for rule in TABLE["unsupported"]:
        if matches(rule, type_, module):
            return "no", rule
    for rule in TABLE["supported"]:
        if not matches(rule, type_, module):
            continue
        version = software.split(" ")[0]
        if not VERSION.match(version):
            return "unread", rule
        have, want = numbers(version), numbers(rule["minimum"])
        have += [0] * (len(want) - len(have))
        return ("old" if have[:len(want)] < want else "ok"), rule
    return "no", None


def device_type(rule):
    return rule.get("type") or "SoundTouch " + rule["typeContains"] + " Adapter"


def cases():
    """(type, moduleType, softwareVersion) per model and boundary, from the table alone."""
    out = []
    for rule in TABLE["supported"]:
        t, m, low = device_type(rule), rule.get("moduleType", ""), rule["minimum"]
        n = numbers(low)
        above = [*n[:-1], n[-1] + 10]  # 27.0.16 > 27.0.6, though "27.0.16" < "27.0.6" as text
        below = [*n[:-1], n[-1] - 1] if n[-1] else [*n[:-2], n[-2] - 1, 99]
        first_below = [n[0] - 1, *n[1:]]
        for version in (low, low + ".46330.5043500 epdbuild.trunk.hepdswbld04.2022-08-04T11:20:29",
                        ".".join(map(str, above)), ".".join(map(str, below)),
                        ".".join(map(str, first_below)), str(n[0]), "", "unknown"):
            out.append((t, m, version))
    for rule in TABLE["unsupported"]:
        for version in ("99.0.0", "1.0.0"):
            out.append((device_type(rule), rule.get("moduleType", ""), version))
    out.append(("Some Other Speaker", "", "99.0.0"))
    return out


CASES = cases()


class TableTest(unittest.TestCase):
    def test_rules_are_complete_and_one_per_line(self):
        """The file's layout is install.sh's input format (it reads one rule per line),
        so the layout itself is checked as text."""
        for rule in TABLE["supported"] + TABLE["unsupported"]:
            self.assertTrue(rule.get("model"), rule)
            self.assertTrue(rule.get("type") or rule.get("typeContains"), rule)
            line = json.dumps(rule, separators=(", ", ": "))
            self.assertIn(line, TABLE_TEXT, "install.sh reads one rule per line")
        for rule in TABLE["supported"]:
            self.assertRegex(rule["minimum"], VERSION)
        for rule in TABLE["unsupported"]:
            self.assertNotIn("minimum", rule, "install.sh tells the lists apart by minimum")

    def test_series_iii_is_unsupported_and_compares_numerically(self):
        self.assertEqual(reference("SoundTouch 20", "sm2", "27.0.6")[0], "no")
        self.assertEqual(reference("SoundTouch 20", "scm", "27.0.6")[0], "ok")
        self.assertEqual(reference("SoundTouch 10", "sm2", "22.0.16")[0], "ok")

    def test_install_guide_repeats_the_table(self):
        """The guide is prose for people; comparing its table with the file is the check."""
        guide = (ROOT / "docs/owners/install.md").read_text()
        rows = re.search(r"\| Model \| Oldest Bose software installed from \|\n\| --- \| --- \|\n((?:\|.*\n)+)",
                         guide)
        self.assertIsNotNone(rows, "docs/owners/install.md lost its table of Bose software")
        listed = [tuple(c.strip() for c in row.strip("|").split("|"))
                  for row in rows.group(1).splitlines()]
        self.assertEqual(listed, [(r["model"], r["minimum"]) for r in TABLE["supported"]])

    def test_no_installer_carries_its_own_versions(self):
        """An absence: no run shows a hard-coded copy that happens to agree today."""
        for name in ("install.sh", "install.ps1", "index.html"):
            text = (INSTALLER / name).read_text()
            for rule in TABLE["supported"]:
                self.assertFalse(rule["minimum"] in text, f"{name} names {rule['minimum']}: read it from versions.json")


class InstallersAgreeTest(unittest.TestCase):
    def check(self, name, got):
        self.assertEqual(len(got), len(CASES), got)
        for case, (status, note) in zip(CASES, got):
            want, rule = reference(*case)
            self.assertEqual(status, want, f"{name}: {case}: {note}")
            if want == "no":
                self.assertRegex(note, r"(?i)not supported yet", f"{name}: {case}")
                self.assertNotIn("update Bose", note, f"{name}: {case}")
                if rule:
                    self.assertIn(rule["model"], note, f"{name}: {case}")
            if want == "old":
                self.assertIn("Update Bose first".lower(), note.lower(), f"{name}: {case}")
                self.assertIn(rule["minimum"], note, f"{name}: {case}")
                self.assertNotIn("not supported", note, f"{name}: {case}")

    def test_install_sh(self):
        script = r'''
. docs/installer/install.sh
table=$(cat docs/installer/versions.json)
while IFS='|' read -r t m v; do
	classify "$table" "$t" "$m" "$v"
	printf '%s|%s\n' "$status" "$note"
done
'''
        lines = "".join(f"{t}|{m}|{v}\n" for t, m, v in CASES)
        for shell in ("sh", "dash"):  # macOS sh is bash; Debian sh is dash
            if not shutil.which(shell):
                continue
            with self.subTest(shell=shell):
                out = subprocess.run([shutil.which(shell), "-c", script], cwd=ROOT, input=lines,
                                     text=True, capture_output=True, check=True,
                                     env={**os.environ, "SOUNDTAP_INSTALLER_FUNCTIONS": "1"})
                self.check(shell, [tuple(line.split("|", 1)) for line in out.stdout.splitlines()])

    @unittest.skipUnless(shutil.which("node"), "the browser installer runs under Node.js")
    def test_browser_installer(self):
        # The page cannot read the speaker: its owner picks the rule the speaker would match.
        picks = []
        for case in CASES:
            rule = reference(*case)[1]
            if rule in TABLE["supported"]:
                pick = "s" + str(TABLE["supported"].index(rule))
            elif rule in TABLE["unsupported"]:
                pick = "u" + str(TABLE["unsupported"].index(rule))
            else:
                pick = "other"
            picks.append([pick, case[2]])
        program = r'''
const vm = require('node:vm');
const fs = require('node:fs');
const source = fs.readFileSync('docs/installer/index.html', 'utf8').match(/<script>([\s\S]*?)<\/script>/)[1];
const table = JSON.parse(fs.readFileSync('docs/installer/versions.json', 'utf8'));
const elements = {};
const document = {
  getElementById(id) {
    return elements[id] ||= { value: '', checked: false, textContent: '',
      addEventListener(name, fn) { this[name] = fn; }, append() {} };
  },
  createElement() { return {}; },
};
const english = JSON.parse(fs.readFileSync('docs/installer/i18n/en.json', 'utf8'));
const fetch = async (url) => {
  if (url === 'i18n/en.json') return { ok: true, json: async () => english };
  if (url !== 'versions.json') throw new Error('unexpected ' + url);
  return { ok: true, json: async () => table };
};
const ctx = vm.createContext({document, fetch, AbortSignal, Date, setTimeout});
vm.runInContext(source, ctx);
(async () => {
  await vm.runInContext('tableLoaded', ctx);
  const out = [];
  for (const [pick, version] of JSON.parse(fs.readFileSync(0, 'utf8'))) {
    elements.model.value = pick; elements.version.value = version;
    elements.agree.checked = true; elements.ip.value = '192.168.1.23';
    elements.model.input();
    const v = vm.runInContext('judge(chosenRule(), version.value)', ctx);
    // The page dispatches exactly when the table says ok.
    if ((v.status === 'ok') === elements.go.disabled) throw new Error('button disagrees: ' + pick + ' ' + version);
    if (v.text && elements.modelNote.textContent !== v.text) throw new Error('note not shown: ' + v.text);
    out.push([v.status, v.text]);
  }
  console.log(JSON.stringify(out));
})().catch((err) => { console.error(err); process.exitCode = 1; });
'''
        out = subprocess.run(["node", "--input-type=commonjs", "-e", program], cwd=ROOT,
                             input=json.dumps(picks), text=True, capture_output=True, check=False)
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.check("index.html", [tuple(x) for x in json.loads(out.stdout)])

    @unittest.skipUnless(shutil.which("pwsh"), "install.ps1 runs under PowerShell")
    def test_install_ps1(self):
        # The script runs inside & { }; dot-sourcing the same block keeps its functions.
        source = (INSTALLER / "install.ps1").read_text().replace("\n& {\n", "\n. {\n", 1)
        program = source + r'''
$table = Get-Content -Raw docs/installer/versions.json | ConvertFrom-Json
foreach ($line in [Console]::In.ReadToEnd().Split("`n")) {
    if (-not $line) { continue }
    $t, $m, $v = $line.Split('|')
    $r = Get-Verdict $table $t $m $v
    Write-Output "$($r.Status)|$($r.Note)"
}
'''
        lines = "".join(f"{t}|{m}|{v}\n" for t, m, v in CASES)
        out = subprocess.run(["pwsh", "-NoProfile", "-NonInteractive", "-Command", program],
                             cwd=ROOT, input=lines, text=True, capture_output=True, check=False,
                             env={**os.environ, "SOUNDTAP_INSTALLER_FUNCTIONS": "1"})
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)
        self.check("install.ps1", [tuple(line.split("|", 1)) for line in out.stdout.splitlines()])


if __name__ == "__main__":
    unittest.main()
