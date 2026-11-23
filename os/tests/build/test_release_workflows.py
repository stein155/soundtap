#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Pin the one-product release shape: build.yml builds only the universal STU, from the
one Buildroot profile, hands off as the run's `candidate` artifact, release.yml publishes
that exact candidate as a draft, and sign.yml, alone with the release key, signs and
publishes it."""
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
sign = (root / ".github/workflows/sign.yml").read_text()

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

# ADR 0035: release.yml never sees the release key. Both lanes publish a draft; sign.yml signs
# it in the release environment (SignWorkflowTest) and makes it public on request.
assert "SOUNDTAP_RELEASE_KEY" not in release and "sign.sh" not in release
assert "secrets." not in release and "environment:" not in release
assert release.count("--draft") == 2
sign_release = (root / "tools/release/sign-release.sh").read_text()
assert "sign.sh" in sign_release and "--draft=false" in sign_release
# An alpha's draft, and only an alpha's, is signed right after it is made, never published.
for job, needs in (("sign-os", "release"), ("sign-app", "app")):
    block = release.split(f"\n  {job}:\n", 1)[1].split("\n\n", 1)[0]
    assert f"needs: {needs}\n" in block, job
    assert f"if: contains(needs.{needs}.outputs.tag, '-alpha.')\n" in block, job
    assert "uses: ./.github/workflows/sign.yml\n" in block, job
    assert "publish" not in block and "secrets" not in block, job
assert release.count('echo "tag=$tag" >> "$GITHUB_OUTPUT"') == 2

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
                       GITHUB_REPOSITORY='fixture/soundtap', VERSION=version, TAGS=tags,
                       GITHUB_OUTPUT=str(base / 'output'))
            result = subprocess.run(['bash', '-e', '-o', 'pipefail', '-c', core_release_script()],
                                    cwd=base, env=env, text=True, capture_output=True, check=False)
            # The tag goes to the job's outputs, for sign.yml to sign an alpha right away.
            if result.returncode == 0:
                self.assertEqual((base / 'output').read_text(), f'tag=os-v{version}\n')
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


# sign.yml, the one workflow that reads the release key (ADR 0035): its job's steps, parsed
# from the file and run below with a stub sign-release.sh, in the order Actions runs them.
def sign_steps():
    job = sign.split('\n    steps:\n', 1)[1]
    steps = []
    for chunk in re.split(r'\n      - ', '\n' + job)[1:]:
        step = {'env': {}}
        lines = chunk.splitlines()
        i = 0
        while i < len(lines):
            line = lines[i].strip() if i else lines[i]
            if line.startswith('name: '):
                step['name'] = line[6:]
            elif line.startswith('uses: '):
                step['uses'] = line[6:]
            elif line.startswith('if: '):
                step['if'] = line[4:]
            elif line == 'env:':
                while i + 1 < len(lines) and lines[i + 1].startswith(' ' * 10):
                    i += 1
                    key, value = lines[i].strip().split(': ', 1)
                    step['env'][key] = value
            elif line == 'run: |':
                body = []
                while i + 1 < len(lines) and (lines[i + 1].startswith(' ' * 10) or not lines[i + 1].strip()):
                    i += 1
                    body.append(lines[i])
                step['run'] = textwrap.dedent('\n'.join(body)) + '\n'
            elif line.startswith('run: '):
                step['run'] = line[5:] + '\n'
            i += 1
        steps.append(step)
    return steps


KEY = '-----BEGIN ENCRYPTED PRIVATE KEY-----\nfixture key, not a key\n-----END ENCRYPTED PRIVATE KEY-----'
PASSPHRASE = 'fixture passphrase'


class SignWorkflowTest(unittest.TestCase):
    def test_shape(self):
        head, job = sign.split('\njobs:\n', 1)
        # Dispatched by hand, or called by release.yml for an alpha; inputs tag and publish.
        self.assertIn('  workflow_dispatch:\n', head)
        self.assertIn('  workflow_call:\n', head)
        self.assertEqual(head.count('      tag:\n'), 2)
        self.assertEqual(head.count('      publish:\n'), 2)
        self.assertEqual(head.count('        default: false\n'), 2)
        # Read-only by default; the one job writes releases and nothing else.
        self.assertIn('\npermissions:\n  contents: read\n', head)
        self.assertEqual(re.findall(r'\n    permissions:\n((?:      .*\n)+)', job), ['      contents: write\n'])
        # The key lives in the release environment, whose branch rule is main.
        self.assertIn('\n    environment: release\n', job)
        self.assertEqual(job.count('runs-on: ubuntu-24.04'), 1)
        self.assertNotIn('set -x', sign)
        self.assertNotIn('ACTIONS_STEP_DEBUG', sign)
        # The secrets are read once each, into one step's environment.
        self.assertEqual(sorted(re.findall(r'secrets\.(\w+)', sign)),
                         ['SOUNDTAP_RELEASE_KEY', 'SOUNDTAP_RELEASE_KEY_PASSPHRASE'])

    def test_no_expression_in_a_script(self):
        # Inputs reach the shell through the environment, never spliced into its text.
        for step in sign_steps():
            self.assertNotIn('${{', step.get('run', ''), step.get('name'))
            for value in step['env'].values():
                self.assertRegex(value, r'^\$\{\{ (inputs\.tag|github\.token|github\.repository|secrets\.\w+) \}\}$')

    def test_actions_are_pinned_by_commit(self):
        for path in (root / '.github/workflows').glob('*.yml'):
            for ref in re.findall(r'uses: (\S+)', path.read_text()):
                if not ref.startswith('./'):
                    self.assertRegex(ref, r'^[\w.-]+/[\w./-]+@[0-9a-f]{40}$', path.name)

    def test_order(self):
        names = [step.get('name') or step['uses'].split('@')[0] for step in sign_steps()]
        self.assertEqual(names, [
            'Only main, only a release tag', 'actions/checkout', 'Hardware reports from the repository',
            'Release key into a private file', 'Sign the draft', 'Remove the release key',
            'Publish the signed draft'])
        steps = sign_steps()
        self.assertEqual(steps[5].get('if'), 'always()')
        self.assertEqual(steps[6].get('if'), 'inputs.publish')
        self.assertIn('persist-credentials: false', sign)

    def run_job(self, tag, ref='refs/heads/main', publish=False, reports=False, fail=False,
                key=KEY):
        """Runs the job's steps as Actions would; returns the step results, the stub's log
        and the runner's temporary directory afterwards."""
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp)
        tree, temp = tmp / 'tree', tmp / 'runner-temp'
        (tree / 'tools/release').mkdir(parents=True)
        temp.mkdir()
        if reports:
            (tree / 'releases' / tag).mkdir(parents=True)
        log = tmp / 'log'
        stub = tree / 'tools/release/sign-release.sh'
        stub.write_text(f'''#!/bin/sh
{{
	echo "args $*"
	if [ "$1" != --publish ]; then
		echo "file $SOUNDTAP_RELEASE_KEY_FILE"
		echo "mode $(stat -c %a "$SOUNDTAP_RELEASE_KEY_FILE" 2>/dev/null || stat -f %Lp "$SOUNDTAP_RELEASE_KEY_FILE")"
		echo "passin $SOUNDTAP_RELEASE_KEY_PASSIN"
		var=${{SOUNDTAP_RELEASE_KEY_PASSIN#env:}}
		eval "[ \\"\\${{$var}}\\" = '{PASSPHRASE}' ]" && echo "passphrase reaches openssl"
		cmp -s "$SOUNDTAP_RELEASE_KEY_FILE" "$KEY_COPY" && echo "key intact"
	fi
}} >> {log}
exit {1 if fail else 0}
''')
        stub.chmod(0o755)
        (tmp / 'key-copy').write_text(key + '\n')
        values = {'inputs.tag': tag, 'github.token': 'fixture-token',
                  'github.repository': 'fixture/soundtap',
                  'secrets.SOUNDTAP_RELEASE_KEY': key,
                  'secrets.SOUNDTAP_RELEASE_KEY_PASSPHRASE': PASSPHRASE}
        results, failed = [], False
        for step in sign_steps():
            condition = step.get('if', 'success()')
            run = {'success()': not failed, 'always()': True,
                   'inputs.publish': not failed and publish}[condition]
            if not run or 'run' not in step:
                continue
            env = dict(os.environ, GITHUB_REF=ref, RUNNER_TEMP=str(temp), KEY_COPY=str(tmp / 'key-copy'))
            for name, value in step['env'].items():
                env[name] = values[value[4:-3]]
            done = subprocess.run(['bash', '--noprofile', '--norc', '-eo', 'pipefail', '-c', step['run']],
                                  cwd=tree, env=env, text=True, capture_output=True, check=False)
            results.append((step['name'], done))
            failed = failed or done.returncode != 0
        return results, log.read_text() if log.exists() else '', temp

    def assertNoSecretShown(self, results):
        for name, done in results:
            for secret in (PASSPHRASE, 'fixture key, not a key'):
                self.assertNotIn(secret, done.stdout + done.stderr, name)

    def test_an_alpha_is_signed_without_reports_and_the_key_removed(self):
        for tag in ('os-v0.1.0-alpha.1', 'app-v0.1.0-alpha.1', 'app-v0.1.0'):
            with self.subTest(tag=tag):
                results, log, temp = self.run_job(tag)
                self.assertTrue(all(done.returncode == 0 for _, done in results), results)
                self.assertIn(f'args {tag}\n', log)
                self.assertIn(f'file {temp}/release-key.pem\n', log)
                self.assertIn('mode 600\n', log)
                self.assertIn('passin env:SOUNDTAP_RELEASE_KEY_PASSPHRASE\n', log)
                self.assertIn('passphrase reaches openssl\n', log)
                self.assertIn('key intact\n', log)
                self.assertNotIn('--publish', log)
                self.assertEqual(list(temp.iterdir()), [])
                self.assertNoSecretShown(results)

    def test_a_core_release_is_signed_against_the_committed_reports(self):
        results, log, temp = self.run_job('os-v0.1.0', reports=True)
        self.assertTrue(all(done.returncode == 0 for _, done in results), results)
        self.assertIn('args os-v0.1.0 releases/os-v0.1.0\n', log)
        self.assertEqual(list(temp.iterdir()), [])
        # An alpha's reports, when there are any, are checked as well.
        _, log, _ = self.run_job('os-v0.1.0-alpha.2', reports=True)
        self.assertIn('args os-v0.1.0-alpha.2 releases/os-v0.1.0-alpha.2\n', log)

    def test_a_core_release_without_reports_never_touches_the_key(self):
        results, log, temp = self.run_job('os-v0.1.0')
        self.assertIn('no releases/os-v0.1.0 on main', results[-2][1].stderr)
        self.assertEqual(log, '')
        self.assertEqual(list(temp.iterdir()), [])

    def test_publish_comes_after_the_key_is_gone(self):
        results, log, temp = self.run_job('os-v0.1.0-alpha.1', publish=True)
        self.assertTrue(all(done.returncode == 0 for _, done in results), results)
        self.assertEqual([name for name, _ in results][-2:], ['Remove the release key', 'Publish the signed draft'])
        self.assertTrue(log.endswith('args --publish os-v0.1.0-alpha.1\n'), log)
        self.assertEqual(list(temp.iterdir()), [])

    def test_a_failed_signature_removes_the_key_and_publishes_nothing(self):
        results, log, temp = self.run_job('app-v0.1.0', publish=True, fail=True)
        self.assertNotEqual(results[-2][1].returncode, 0)
        self.assertEqual(results[-1][0], 'Remove the release key')
        self.assertNotIn('--publish', log)
        self.assertEqual(list(temp.iterdir()), [])

    def test_only_main_and_only_release_tags(self):
        for tag, ref in (('os-v0.1.0-alpha.1', 'refs/heads/feature'),
                         ('os-v0.1.0-alpha.1', 'refs/tags/os-v0.1.0-alpha.1'),
                         ('os-v0.1.0-beta.1', 'refs/heads/main'),
                         ('os-v0.1.0-alpha.0', 'refs/heads/main'),
                         ('candidate-abc', 'refs/heads/main'),
                         ('os-v0.1.0; echo pwned', 'refs/heads/main'),
                         ('os-v0.1.0\nos-v0.2.0', 'refs/heads/main'),
                         ('app-v$(id)', 'refs/heads/main')):
            with self.subTest(tag=tag, ref=ref):
                results, log, temp = self.run_job(tag, ref=ref, reports=True)
                self.assertNotEqual(results[0][1].returncode, 0)
                self.assertEqual(log, '')
                self.assertEqual(list(temp.iterdir()), [])

    def test_an_empty_key_secret_is_refused(self):
        results, log, temp = self.run_job('app-v0.1.0', key='')
        self.assertIn('no SOUNDTAP_RELEASE_KEY', results[-2][1].stderr)
        self.assertEqual(log, '')
        self.assertEqual(list(temp.iterdir()), [])
