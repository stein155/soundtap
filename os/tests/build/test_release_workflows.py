#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Pin the one-product release shape: build.yml builds only the universal STU, from the
one Buildroot profile, hands off as the run's `candidate` artifact, and release.yml
publishes that exact candidate."""
import hashlib
import json
import os
import re
import shutil
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path

root = Path(__file__).parents[3]
workflow = (root / ".github/workflows/build.yml").read_text()
release = (root / ".github/workflows/release.yml").read_text()

# One Buildroot tree for kernel, rootfs and bootloaders (ADR 0011); no script leg left. The
# only matrix is the install gate's, one job per model, after the one package job.
package_job = workflow.split("\n  install-gate:\n")[0]
assert "matrix" not in package_job
assert "model: [st10, st20, wla, st300]" in workflow
assert "os/bootloader/build-nand.sh" not in workflow
assert "build-nand-rootfs.sh" not in workflow
assert "build-wla-uboot.sh" not in workflow and "build-wla-product-spl.sh" not in workflow
# The build goes through the entry script a build host runs too (test_ci_entry.py runs it).
assert "run: tools/ci/run-build.sh os\n" in workflow and "run: tools/ci/run-build.sh stu run\n" in workflow
assert "run: tools/ci/run-build.sh gate\n" in workflow
# ADR 0011: a GitHub-hosted runner, so every build starts without a Buildroot tree and
# says so to release.yml, which publishes only such a build.
assert "runs-on: ubuntu-24.04" in workflow and "self-hosted" not in workflow
assert "dist/reuse" not in workflow and "dist/reuse" not in release
ci = (root / ".github/workflows/build-stu.yml").read_text()
assert release.count("runs-on: ubuntu-24.04") == 3
assert set(re.findall(r"runs-on: (\S+)", ci)) == {"ubuntu-24.04"}
# Every job of every workflow runs on a GitHub-hosted runner (ADR 0011).
for path in (root / ".github/workflows").glob("*.yml"):
    text = path.read_text()
    assert "self-hosted" not in text, path.name
    for runner in re.findall(r"runs-on: (\S+)", text):
        assert runner in ("ubuntu-24.04", "ubuntu-24.04-arm", "${{"), (path.name, runner)
assert "github.event.pull_request.head.repo.full_name" not in ci
# Every workflow-dispatch input in the documented build command is declared.
instructions = (root / "RELEASING.md").read_text()
for command in re.findall(r"^gh workflow run build.yml.*$", instructions, re.M):
    for argument in re.findall(r"-f (\w+)=", command):
        assert f"      {argument}:" in workflow.split("permissions:")[0], argument
assert "buildroot-reuse.sh" not in workflow
# One build per ref at a time.
assert "group: buildroot-nand-${{ github.ref }}" in workflow
# ADR 0011: no CI-storage release; the candidate is an artifact of the Build run, and Build
# publishes nothing, so it cannot write the repository's contents.
assert "ci-store" not in workflow and "ci-store" not in release
assert "name: candidate" in workflow and "actions/upload-artifact" in workflow
assert "gh release" not in workflow and "contents: write" not in workflow
# Release reads only that artifact, so a build that could not upload it fails.
assert "continue-on-error" not in package_job
assert "gh release download candidate-" not in release

# ADR 0035: tags and names come from the VERSION files, never from a typed input.
assert "version:" not in release.split("jobs:")[0]
assert "tag=os-v$version stu=SoundTap-Core-$version.stu" in release
assert "tag=app-v$version bin=SoundTap-app-$version-armv7.bin" in release
assert '--title "SoundTap Core $version"' in release and '--title "SoundTap app $version"' in release
assert release.count("tools/release/notes.py") == 2

assert "gh run download \"$run\"" in release and "-n candidate -D dist" in release
assert "-w build.yml --commit \"$GITHUB_SHA\"" in release
assert 'test "$built" = "$GITHUB_SHA"' in release
# ADR 0035: an X.Y.Z-alpha.N version makes its draft a prerelease, on both lanes; nothing else
# does (HostedDraftTest runs the Core lane's script with both).
assert release.count('if [[ "$version" == *-alpha.* ]]; then pre=(--prerelease); fi') == 2
assert release.count('--draft "${pre[@]}"') == 2 and release.count("--prerelease") == 2

# ADR 0035: neither lane publishes a commit CI has not passed on.
assert "-w build-stu.yml --commit \"$GITHUB_SHA\"" in release
assert release.count("needs: ci-passed") == 2
# Both lanes wait on the README screenshots of the released UI (test_screenshots.py).
assert 'python3 tools/release/screenshots.py check "$GITHUB_SHA"' in release.split("\n  release:")[0]

# ADR 0035: the key never reaches CI. Both lanes publish a draft; sign-release.sh signs
# offline and makes it public.
assert "SOUNDTAP_RELEASE_KEY" not in release and "sign.sh" not in release
assert release.count("--draft") == 2
sign_release = (root / "tools/release/sign-release.sh").read_text()
assert "sign.sh" in sign_release and "--draft=false" in sign_release

# Execute the workflow's actual Core draft script against local candidate fixtures.
# GitHub, tag lookup and package inspection are stubbed; checksums and shell gates run.

def core_release_script():
    section = release.split('      - name: Publish the candidate built from this commit\n', 1)[1]
    block = section.split('        run: |\n', 1)[1].split('\n  app:', 1)[0]
    return textwrap.dedent(block)


class HostedDraftTest(unittest.TestCase):
    def draft(self, scenario, version='0.1.0', tags=''):
        """Runs the script; returns its result, the `gh release create` arguments (None when
        it creates no draft) and the arguments notes.py was given."""
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'os').mkdir()
            (base / 'os/VERSION').write_text(version + '\n')
            candidate = base / 'candidate'
            candidate.mkdir()
            stu = f'SoundTap-Core-{version}.stu'
            data = b'local candidate fixture; not firmware'
            (candidate / stu).write_bytes(data)
            digest = hashlib.sha256(data).hexdigest()
            (candidate / (stu + '.sha256')).write_text(f'{digest}  {stu}\n')
            (candidate / 'commit').write_text('other' if scenario == 'commit' else 'candidate-sha')
            (candidate / 'profile').write_text('alpha' if scenario == 'alpha' else 'production')
            for asset in (f'SoundTap-Core-{version}-sources.tar', f'SoundTap-Core-{version}.cdx.json'):
                if scenario != 'missing-' + asset.rsplit('.', 1)[1]:
                    (candidate / asset).write_text('fixture')
            if scenario == 'checksum':
                (candidate / stu).write_bytes(b'changed')
            bin_dir = base / 'bin'
            bin_dir.mkdir()
            gh = bin_dir / 'gh'
            gh.write_text('''#!/usr/bin/env python3
import json, os, pathlib, shutil, sys
args = sys.argv[1:]
base = pathlib.Path(os.environ['FIXTURE'])
with (base / 'calls').open('a') as f:
    f.write(json.dumps(args) + '\\n')
if args[:2] == ['run', 'list']:
    assert args[args.index('--commit') + 1] == 'candidate-sha'
    print('' if os.environ['SCENARIO'] == 'no-build' else '42')
elif args[:2] == ['run', 'download']:
    shutil.copytree(base / 'candidate', base / 'dist')
elif args[:2] == ['release', 'create']:
    assert '--draft' in args
    assert args[args.index('--target') + 1] == 'candidate-sha'
    stu = 'SoundTap-Core-' + os.environ['VERSION'] + '.stu'
    assert (base / 'dist' / stu).read_bytes() == (base / 'candidate' / stu).read_bytes()
else:
    sys.exit('unexpected gh command')
''')
            # The tag list comes back as git would sort it with the alphas' suffix.
            (bin_dir / 'git').write_text('''#!/bin/sh
if [ "$1" = -c ]; then
	[ "$2" = versionsort.suffix=-alpha. ] || exit 98
	shift 2
fi
case "$1" in
rev-parse) exit 1;;
tag) [ "$2" != -l ] || printf '%s' "$TAGS";;
*) exit 99;;
esac
''')
            # Keep the independent STU parser out of this release-orchestration fixture.
            inspector = base / 'os/support'
            inspector.mkdir(parents=True)
            (inspector / 'stu_check.py').write_text(
                "import os, sys\nsys.exit(1 if os.environ['SCENARIO'] == 'structure' else 0)\n")
            (base / 'tools/release').mkdir(parents=True)
            (base / 'tools/release/notes.py').write_text(
                "import json, pathlib, sys\n"
                "pathlib.Path('notes-args').write_text(json.dumps(sys.argv[1:]))\n"
                "print('fixture release notes')\n")
            shutil.copy(root / 'tools/release/changelog.py', base / 'tools/release/changelog.py')
            tag = 'os-v0.0.9' if scenario == 'no-changelog' else f'os-v{version}'
            (base / 'CHANGELOG.md').write_text(f'# Changelog\n\n## [Unreleased]\n\n## [{tag}] - 2026-12-01\n\n- First.\n')
            for command in bin_dir.iterdir():
                command.chmod(0o755)
            env = dict(os.environ, PATH=str(bin_dir) + os.pathsep + os.environ['PATH'],
                       FIXTURE=tmp, SCENARIO=scenario, GITHUB_SHA='candidate-sha',
                       GITHUB_REPOSITORY='fixture/soundtap', VERSION=version, TAGS=tags)
            result = subprocess.run(['bash', '-e', '-o', 'pipefail', '-c', core_release_script()],
                                    cwd=base, env=env, text=True, capture_output=True, check=False)
            calls_file = base / 'calls'  # Absent when the script stops before it asks gh anything.
            calls = [json.loads(line) for line in calls_file.read_text().splitlines()] if calls_file.exists() else []
            created = next((call for call in calls if call[:2] == ['release', 'create']), None)
            notes = base / 'notes-args'
            return result, created, json.loads(notes.read_text()) if notes.exists() else None

    @unittest.skipUnless(shutil.which('sha256sum'), 'GNU checksum tools required')
    def test_hosted_candidate_draft_gates(self):
        for scenario in ('success', 'commit', 'alpha', 'checksum', 'missing-tar',
                         'missing-json', 'no-build', 'structure', 'no-changelog'):
            with self.subTest(scenario=scenario):
                result, created, _ = self.draft(scenario)
                self.assertEqual(result.returncode == 0, scenario == 'success', result.stderr)
                self.assertEqual(created is not None, scenario == 'success', result.stdout + result.stderr)
                if created:
                    self.assertNotIn('--prerelease', created)

    # ADR 0035: an alpha's draft is a prerelease, with every gate of a release.
    @unittest.skipUnless(shutil.which('sha256sum'), 'GNU checksum tools required')
    def test_an_alpha_draft_is_a_prerelease(self):
        result, created, _ = self.draft('success', version='0.1.0-alpha.1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('--prerelease', created)
        self.assertIn('SoundTap Core 0.1.0-alpha.1', created)
        for scenario in ('alpha', 'missing-tar', 'commit'):
            with self.subTest(scenario=scenario):
                result, created, _ = self.draft(scenario, version='0.1.0-alpha.1')
                self.assertNotEqual(result.returncode, 0)
                self.assertIsNone(created)

    @unittest.skipUnless(shutil.which('sha256sum'), 'GNU checksum tools required')
    def test_other_suffixes_are_not_versions(self):
        for version in ('0.1.0-beta.1', '0.1.0-alpha', '0.1.0-alpha.0', '0.1.0-rc.1'):
            with self.subTest(version=version):
                result, created, _ = self.draft('success', version=version)
                self.assertEqual(result.returncode, 2, result.stderr)
                self.assertIsNone(created)

    # An alpha's notes compare with the newest tag; a release's with the previous release,
    # since its owners never saw the alphas.
    @unittest.skipUnless(shutil.which('sha256sum'), 'GNU checksum tools required')
    def test_the_notes_compare_with_the_previous_tag_of_their_kind(self):
        tags = 'os-v0.2.0-alpha.1\nos-v0.1.0\nos-v0.1.0-alpha.2\nos-v0.1.0-alpha.1\n'
        for version, previous in (('0.2.0', 'os-v0.1.0'), ('0.2.0-alpha.2', 'os-v0.2.0-alpha.1'),
                                  ('0.1.0', None)):
            with self.subTest(version=version):
                result, _, notes = self.draft('success', version=version,
                                              tags=tags if previous else 'os-v0.1.0-alpha.1\n')
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(notes[4:5], [previous] if previous else [])
