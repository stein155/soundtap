# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Pin the one-product release shape.

build.yml builds only the universal STU, from the
one Buildroot profile, hands off as the run's `candidate` artifact, release.yml publishes
that exact candidate as a draft, and sign.yml, alone with the release key, signs and
publishes it.
"""
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
from typing import TypedDict, Unpack

root = Path(__file__).parents[3]
workflow = (root / ".github/workflows/build.yml").read_text()
release = (root / ".github/workflows/release.yml").read_text()
sign = (root / ".github/workflows/sign.yml").read_text()

ci = (root / ".github/workflows/build-stu.yml").read_text()
# The package job: everything before the install gate's matrix.
package_job = workflow.split("\n  install-gate:\n")[0]


class ReleaseShapeTest(unittest.TestCase):
    """The shape of build.yml, release.yml and the other workflows, read from the files."""

    def test_one_buildroot_tree_builds_everything(self) -> None:
        """One Buildroot tree builds kernel, rootfs and bootloaders (ADR 0011), with no script leg left.

        The only matrix is the install gate's, one job per model, after the one package job.
        The build goes through the entry script a build host runs too (test_ci_entry.py runs it).
        """
        self.assertNotIn("matrix", package_job)
        self.assertIn("model: [st10, st20, wla, st300]", workflow)
        self.assertNotIn("os/bootloader/build-nand.sh", workflow)
        self.assertNotIn("build-nand-rootfs.sh", workflow)
        self.assertNotIn("build-wla-uboot.sh", workflow)
        self.assertNotIn("build-wla-product-spl.sh", workflow)
        self.assertIn("run: tools/ci/run-build.sh os\n", workflow)
        self.assertIn("run: tools/ci/run-build.sh stu run\n", workflow)
        self.assertIn("run: tools/ci/run-build.sh gate\n", workflow)

    def test_every_job_runs_on_a_github_hosted_runner(self) -> None:
        """Every job of every workflow runs on a GitHub-hosted runner (ADR 0011).

        So every build starts without a Buildroot tree and says so to release.yml, which
        publishes only such a build.
        """
        self.assertIn("runs-on: ubuntu-24.04", workflow)
        self.assertNotIn("self-hosted", workflow)
        self.assertNotIn("dist/reuse", workflow)
        self.assertNotIn("dist/reuse", release)
        self.assertEqual(release.count("runs-on: ubuntu-24.04"), 3)
        self.assertEqual(set(re.findall(r"runs-on: (\S+)", ci)), {"ubuntu-24.04"})
        for path in (root / ".github/workflows").glob("*.yml"):
            text = path.read_text()
            self.assertNotIn("self-hosted", text, path.name)
            for runner in re.findall(r"runs-on: (\S+)", text):
                self.assertIn(runner, ("ubuntu-24.04", "ubuntu-24.04-arm", "${{"), (path.name, runner))
        self.assertNotIn("github.event.pull_request.head.repo.full_name", ci)

    def test_the_documented_build_command_declares_its_inputs(self) -> None:
        """Every workflow-dispatch input in the documented build command is declared, and one build per ref runs at a time."""
        instructions = (root / "RELEASING.md").read_text()
        for command in re.findall(r"^gh workflow run build.yml.*$", instructions, re.MULTILINE):
            for argument in re.findall(r"-f (\w+)=", command):
                self.assertIn(f"      {argument}:", workflow.split("permissions:")[0], argument)
        self.assertNotIn("buildroot-reuse.sh", workflow)
        self.assertIn("group: buildroot-nand-${{ github.ref }}", workflow)

    def test_the_candidate_is_an_artifact_of_the_build_run(self) -> None:
        """ADR 0011: no CI-storage release; the candidate is an artifact of the Build run.

        Build publishes nothing, so it cannot write the repository's contents. Release reads
        only that artifact, so a build that could not upload it fails.
        """
        self.assertNotIn("ci-store", workflow)
        self.assertNotIn("ci-store", release)
        self.assertIn("name: candidate", workflow)
        self.assertIn("actions/upload-artifact", workflow)
        self.assertNotIn("gh release", workflow)
        self.assertNotIn("contents: write", workflow)
        self.assertNotIn("continue-on-error", package_job)
        self.assertNotIn("gh release download candidate-", release)

    def test_tags_and_names_come_from_the_version_files(self) -> None:
        """ADR 0035: tags and names come from the VERSION files, never from a typed input."""
        self.assertNotIn("version:", release.split("jobs:")[0])
        self.assertIn("tag=os-v$version stu=SoundTap-Core-$version.stu", release)
        self.assertIn("tag=app-v$version bin=SoundTap-app-$version-armv7.bin", release)
        self.assertIn('--title "SoundTap Core $version"', release)
        self.assertIn('--title "SoundTap app $version"', release)
        self.assertEqual(release.count("tools/release/notes.py"), 2)

    def test_release_publishes_the_build_of_this_commit(self) -> None:
        """Release downloads the candidate the Build run made from this very commit."""
        self.assertIn('gh run download "$run"', release)
        self.assertIn("-n candidate -D dist", release)
        self.assertIn('-w build.yml --commit "$GITHUB_SHA"', release)
        self.assertIn('test "$built" = "$GITHUB_SHA"', release)

    def test_only_an_alpha_draft_is_a_prerelease(self) -> None:
        """ADR 0035: an X.Y.Z-alpha.N version makes its draft a prerelease, on both lanes; nothing else does.

        HostedDraftTest runs the Core lane's script with both.
        """
        self.assertEqual(release.count('if [[ "$version" == *-alpha.* ]]; then pre=(--prerelease); fi'), 2)
        self.assertEqual(release.count('--draft "${pre[@]}"'), 2)
        self.assertEqual(release.count("--prerelease"), 2)

    def test_neither_lane_publishes_a_commit_ci_has_not_passed(self) -> None:
        """ADR 0035: neither lane publishes a commit CI has not passed on.

        Both lanes wait on the README screenshots of the released UI (test_screenshots.py).
        """
        self.assertIn('-w build-stu.yml --commit "$GITHUB_SHA"', release)
        self.assertEqual(release.count("needs: ci-passed"), 2)
        self.assertIn('python3 tools/release/screenshots.py check "$GITHUB_SHA"', release.split("\n  release:")[0])

    def test_release_never_sees_the_release_key(self) -> None:
        """ADR 0035: release.yml never sees the release key; both lanes publish a draft.

        sign.yml signs it in the release environment (SignWorkflowTest) and makes it public on
        request.
        """
        self.assertNotIn("SOUNDTAP_RELEASE_KEY", release)
        self.assertNotIn("sign.sh", release)
        self.assertNotIn("secrets.", release)
        self.assertNotIn("environment:", release)
        self.assertEqual(release.count("--draft"), 2)
        sign_release = (root / "tools/release/sign-release.sh").read_text()
        self.assertIn("sign.sh", sign_release)
        self.assertIn("--draft=false", sign_release)

    def test_only_an_alpha_draft_is_signed_right_away(self) -> None:
        """An alpha's draft, and only an alpha's, is signed right after it is made, never published.

        GitHub gives a called workflow no secret, its environment's included, unless the caller
        inherits them; release.yml's own jobs still read none.
        """
        for job, needs in (("sign-os", "release"), ("sign-app", "app")):
            block = release.split(f"\n  {job}:\n", 1)[1].split("\n\n", 1)[0]
            self.assertIn(f"needs: {needs}\n", block, job)
            self.assertIn(f"if: contains(needs.{needs}.outputs.tag, '-alpha.')\n", block, job)
            self.assertIn("uses: ./.github/workflows/sign.yml\n", block, job)
            self.assertNotIn("publish", block, job)
            self.assertIn("    secrets: inherit", block.splitlines(), job)
        self.assertEqual(release.count('echo "tag=$tag" >> "$GITHUB_OUTPUT"'), 2)


def core_release_script() -> str:
    """Return the workflow's actual Core draft script, to run against local candidate fixtures."""
    section = release.split("      - name: Publish the candidate built from this commit\n", 1)[1]
    block = section.split("        run: |\n", 1)[1].split("\n  app:", 1)[0]
    return textwrap.dedent(block)


class HostedDraftTest(unittest.TestCase):
    """The Core draft script, with GitHub, tag lookup and package inspection stubbed; checksums and shell gates run."""

    def draft(self, scenario: str, version: str = "0.1.0",
              tags: str = "") -> tuple[subprocess.CompletedProcess[str], list[str] | None, list[str] | None]:
        """Run the script and return its result, the `gh release create` arguments and the notes.py arguments.

        The `gh release create` arguments are None when it creates no draft, the notes.py
        arguments None when notes.py did not run.
        """
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / "os").mkdir()
            (base / "os/VERSION").write_text(version + "\n")
            candidate = base / "candidate"
            candidate.mkdir()
            stu = f"SoundTap-Core-{version}.stu"
            data = b"local candidate fixture; not firmware"
            (candidate / stu).write_bytes(data)
            digest = hashlib.sha256(data).hexdigest()
            (candidate / (stu + ".sha256")).write_text(f"{digest}  {stu}\n")
            # The preview package (ADR 0133) travels with the candidate, with its own checksum.
            preview = f"SoundTap-Core-{version}-preview.stu"
            if scenario != "missing-preview":
                pdata = b"local preview fixture; not firmware"
                (candidate / preview).write_bytes(pdata)
                (candidate / (preview + ".sha256")).write_text(
                    f"{hashlib.sha256(pdata).hexdigest()}  {preview}\n")
            if scenario == "preview-checksum":
                (candidate / preview).write_bytes(b"changed")
            (candidate / "commit").write_text("other" if scenario == "commit" else "candidate-sha")
            (candidate / "profile").write_text("alpha" if scenario == "alpha" else "production")
            for asset in (f"SoundTap-Core-{version}-sources.tar", f"SoundTap-Core-{version}.cdx.json"):
                if scenario != "missing-" + asset.rsplit(".", 1)[1]:
                    (candidate / asset).write_text("fixture")
            if scenario == "checksum":
                (candidate / stu).write_bytes(b"changed")
            bin_dir = base / "bin"
            bin_dir.mkdir()
            gh = bin_dir / "gh"
            gh.write_text("""#!/usr/bin/env python3
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
    for stu in ('SoundTap-Core-' + os.environ['VERSION'] + '.stu',
                'SoundTap-Core-' + os.environ['VERSION'] + '-preview.stu'):
        assert (base / 'dist' / stu).read_bytes() == (base / 'candidate' / stu).read_bytes()
        assert 'dist/' + stu in args and 'dist/' + stu + '.sha256' in args, args
else:
    sys.exit('unexpected gh command')
""")
            # The tag list comes back as git would sort it with the alphas' suffix.
            (bin_dir / "git").write_text("""#!/bin/sh
if [ "$1" = -c ]; then
	[ "$2" = versionsort.suffix=-alpha. ] || exit 98
	shift 2
fi
case "$1" in
rev-parse) exit 1;;
tag) [ "$2" != -l ] || printf '%s' "$TAGS";;
*) exit 99;;
esac
""")
            # Keep the independent STU parser out of this release-orchestration fixture.
            inspector = base / "os/support"
            inspector.mkdir(parents=True)
            (inspector / "stu_check.py").write_text(
                "import os, sys\n"
                "scenario = os.environ['SCENARIO']\n"
                "sys.exit(1 if scenario == 'structure' or (scenario == 'payloads' and sys.argv[1] == 'same-payloads') else 0)\n")
            (base / "tools/release").mkdir(parents=True)
            (base / "tools/release/notes.py").write_text(
                "import json, pathlib, sys\n"
                "pathlib.Path('notes-args').write_text(json.dumps(sys.argv[1:]))\n"
                "print('fixture release notes')\n")
            shutil.copy(root / "tools/release/changelog.py", base / "tools/release/changelog.py")
            tag = "os-v0.0.9" if scenario == "no-changelog" else f"os-v{version}"
            (base / "CHANGELOG.md").write_text(f"# Changelog\n\n## [Unreleased]\n\n## [{tag}] - 2026-12-01\n\n- First.\n")
            for command in bin_dir.iterdir():
                command.chmod(0o755)
            env = dict(os.environ, PATH=str(bin_dir) + os.pathsep + os.environ["PATH"],
                       FIXTURE=tmp, SCENARIO=scenario, GITHUB_SHA="candidate-sha",
                       GITHUB_REPOSITORY="fixture/soundtap", VERSION=version, TAGS=tags,
                       GITHUB_OUTPUT=str(base / "output"))
            result = subprocess.run(["bash", "-e", "-o", "pipefail", "-c", core_release_script()],
                                    cwd=base, env=env, text=True, capture_output=True, check=False)
            # The tag goes to the job's outputs, for sign.yml to sign an alpha right away.
            if result.returncode == 0:
                self.assertEqual((base / "output").read_text(), f"tag=os-v{version}\n")
            calls_file = base / "calls"  # Absent when the script stops before it asks gh anything.
            calls = [json.loads(line) for line in calls_file.read_text().splitlines()] if calls_file.exists() else []
            created = next((call for call in calls if call[:2] == ["release", "create"]), None)
            notes = base / "notes-args"
            return result, created, json.loads(notes.read_text()) if notes.exists() else None

    @unittest.skipUnless(shutil.which("sha256sum"), "GNU checksum tools required")
    def test_hosted_candidate_draft_gates(self) -> None:
        """Only a complete, matching candidate of this commit becomes a draft, and not a prerelease."""
        for scenario in ("success", "commit", "alpha", "checksum", "missing-tar",
                         "missing-json", "no-build", "structure", "no-changelog",
                         "missing-preview", "preview-checksum", "payloads"):
            with self.subTest(scenario=scenario):
                result, created, _ = self.draft(scenario)
                self.assertEqual(result.returncode == 0, scenario == "success", result.stderr)
                self.assertEqual(created is not None, scenario == "success", result.stdout + result.stderr)
                if created:
                    self.assertNotIn("--prerelease", created)

    @unittest.skipUnless(shutil.which("sha256sum"), "GNU checksum tools required")
    def test_an_alpha_draft_is_a_prerelease(self) -> None:
        """ADR 0035: an alpha's draft is a prerelease, with every gate of a release."""
        result, created, _ = self.draft("success", version="0.1.0-alpha.1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--prerelease", created)
        self.assertIn("SoundTap Core 0.1.0-alpha.1", created)
        for scenario in ("alpha", "missing-tar", "commit"):
            with self.subTest(scenario=scenario):
                result, created, _ = self.draft(scenario, version="0.1.0-alpha.1")
                self.assertNotEqual(result.returncode, 0)
                self.assertIsNone(created)

    @unittest.skipUnless(shutil.which("sha256sum"), "GNU checksum tools required")
    def test_other_suffixes_are_not_versions(self) -> None:
        """A suffix other than -alpha.N is no version and makes no draft."""
        for version in ("0.1.0-beta.1", "0.1.0-alpha", "0.1.0-alpha.0", "0.1.0-rc.1"):
            with self.subTest(version=version):
                result, created, _ = self.draft("success", version=version)
                self.assertEqual(result.returncode, 2, result.stderr)
                self.assertIsNone(created)

    @unittest.skipUnless(shutil.which("sha256sum"), "GNU checksum tools required")
    def test_the_notes_compare_with_the_previous_tag_of_their_kind(self) -> None:
        """An alpha's notes compare with the newest tag; a release's with the previous release, since its owners never saw the alphas."""
        tags = "os-v0.2.0-alpha.1\nos-v0.1.0\nos-v0.1.0-alpha.2\nos-v0.1.0-alpha.1\n"
        for version, previous in (("0.2.0", "os-v0.1.0"), ("0.2.0-alpha.2", "os-v0.2.0-alpha.1"),
                                  ("0.1.0", None)):
            with self.subTest(version=version):
                result, _, notes = self.draft("success", version=version,
                                              tags=tags if previous else "os-v0.1.0-alpha.1\n")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(notes[4:notes.index("--preview")], [previous] if previous else [])
                self.assertEqual(notes[notes.index("--preview") + 1], f"SoundTap-Core-{version}-preview.stu")


def sign_steps() -> list[dict]:
    """Parse the steps of sign.yml's job, in the order Actions runs them.

    sign.yml is the one workflow that reads the release key (ADR 0035); its steps are run
    below with a stub sign-release.sh.
    """
    job = sign.split("\n    steps:\n", 1)[1]
    steps = []
    for chunk in re.split(r"\n      - ", "\n" + job)[1:]:
        step = {"env": {}}
        lines = chunk.splitlines()
        i = 0
        while i < len(lines):
            line = lines[i].strip() if i else lines[i]
            if line.startswith("name: "):
                step["name"] = line[6:]
            elif line.startswith("uses: "):
                step["uses"] = line[6:]
            elif line.startswith("if: "):
                step["if"] = line[4:]
            elif line == "env:":
                while i + 1 < len(lines) and lines[i + 1].startswith(" " * 10):
                    i += 1
                    key, value = lines[i].strip().split(": ", 1)
                    step["env"][key] = value
            elif line == "run: |":
                body = []
                while i + 1 < len(lines) and (lines[i + 1].startswith(" " * 10) or not lines[i + 1].strip()):
                    i += 1
                    body.append(lines[i])
                step["run"] = textwrap.dedent("\n".join(body)) + "\n"
            elif line.startswith("run: "):
                step["run"] = line[5:] + "\n"
            i += 1
        steps.append(step)
    return steps


KEY = "-----BEGIN ENCRYPTED PRIVATE KEY-----\nfixture key, not a key\n-----END ENCRYPTED PRIVATE KEY-----"
KEY_PHRASE = "fixture passphrase"


class JobOptions(TypedDict, total=False):
    """How run_job runs sign.yml's job; see run_job for each option."""

    ref: str
    publish: bool
    reports: bool
    fail: bool
    key: str


class SignWorkflowTest(unittest.TestCase):
    """sign.yml's shape, and its job's steps run with a stub sign-release.sh."""

    def test_shape(self) -> None:
        """sign.yml is dispatched or called, writes only releases, in the release environment, reading each secret once."""
        head, job = sign.split("\njobs:\n", 1)
        # Dispatched by hand, or called by release.yml for an alpha; inputs tag and publish.
        self.assertIn("  workflow_dispatch:\n", head)
        self.assertIn("  workflow_call:\n", head)
        self.assertEqual(head.count("      tag:\n"), 2)
        self.assertEqual(head.count("      publish:\n"), 2)
        self.assertEqual(head.count("        default: false\n"), 2)
        # Read-only by default; the one job writes releases and nothing else.
        self.assertIn("\npermissions:\n  contents: read\n", head)
        self.assertEqual(re.findall(r"\n    permissions:\n((?:      .*\n)+)", job), ["      contents: write\n"])
        # The key lives in the release environment, whose branch rule is main.
        self.assertIn("\n    environment: release\n", job)
        self.assertEqual(job.count("runs-on: ubuntu-24.04"), 1)
        self.assertNotIn("set -x", sign)
        self.assertNotIn("ACTIONS_STEP_DEBUG", sign)
        # The secrets are read once each, into one step's environment.
        self.assertEqual(sorted(re.findall(r"secrets\.(\w+)", sign)),
                         ["SOUNDTAP_RELEASE_KEY", "SOUNDTAP_RELEASE_KEY_PASSPHRASE"])

    def test_no_expression_in_a_script(self) -> None:
        """Inputs reach the shell through the environment, never spliced into its text."""
        for step in sign_steps():
            self.assertNotIn("${{", step.get("run", ""), step.get("name"))
            for value in step["env"].values():
                self.assertRegex(value, r"^\$\{\{ (inputs\.tag|github\.token|github\.repository|secrets\.\w+) \}\}$")

    def test_actions_are_pinned_by_commit(self) -> None:
        """Every action of every workflow is pinned by commit."""
        for path in (root / ".github/workflows").glob("*.yml"):
            for ref in re.findall(r"uses: (\S+)", path.read_text()):
                if not ref.startswith("./"):
                    self.assertRegex(ref, r"^[\w.-]+/[\w./-]+@[0-9a-f]{40}$", path.name)

    def test_order(self) -> None:
        """The steps run in order: the key is removed always, and publishing comes last, on request."""
        names = [step.get("name") or step["uses"].split("@")[0] for step in sign_steps()]
        self.assertEqual(names, [
            "Only main, only a release tag", "actions/checkout", "Hardware reports from the repository",
            "Release key into a private file", "Sign the draft", "Remove the release key",
            "Publish the signed draft"])
        steps = sign_steps()
        self.assertEqual(steps[5].get("if"), "always()")
        self.assertEqual(steps[6].get("if"), "inputs.publish")
        self.assertIn("persist-credentials: false", sign)

    def run_job(self, tag: str,
                **options: Unpack[JobOptions]) -> tuple[list[tuple[str, subprocess.CompletedProcess[str]]], str, Path]:
        """Run the job's steps as Actions would; return the step results, the stub's log and the runner's temporary directory.

        ref is the ref the job runs on, publish the publish input, reports whether the tag's
        reports are committed, fail whether the stub fails, and key the key secret.
        """
        ref = options.get("ref", "refs/heads/main")
        publish = options.get("publish", False)
        reports = options.get("reports", False)
        fail = options.get("fail", False)
        key = options.get("key", KEY)
        tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, tmp)
        tree, temp = tmp / "tree", tmp / "runner-temp"
        (tree / "tools/release").mkdir(parents=True)
        temp.mkdir()
        if reports:
            (tree / "releases" / tag).mkdir(parents=True)
        log = tmp / "log"
        stub = tree / "tools/release/sign-release.sh"
        stub.write_text(f"""#!/bin/sh
{{
	echo "args $*"
	if [ "$1" != --publish ]; then
		echo "file $SOUNDTAP_RELEASE_KEY_FILE"
		echo "mode $(stat -c %a "$SOUNDTAP_RELEASE_KEY_FILE" 2>/dev/null || stat -f %Lp "$SOUNDTAP_RELEASE_KEY_FILE")"
		echo "passin $SOUNDTAP_RELEASE_KEY_PASSIN"
		var=${{SOUNDTAP_RELEASE_KEY_PASSIN#env:}}
		eval "[ \\"\\${{$var}}\\" = '{KEY_PHRASE}' ]" && echo "passphrase reaches openssl"
		cmp -s "$SOUNDTAP_RELEASE_KEY_FILE" "$KEY_COPY" && echo "key intact"
	fi
}} >> {log}
exit {1 if fail else 0}
""")
        stub.chmod(0o755)
        (tmp / "key-copy").write_text(key + "\n")
        values = {"inputs.tag": tag, "github.token": "fixture-token",
                  "github.repository": "fixture/soundtap",
                  "secrets.SOUNDTAP_RELEASE_KEY": key,
                  "secrets.SOUNDTAP_RELEASE_KEY_PASSPHRASE": KEY_PHRASE}
        results, failed = [], False
        for step in sign_steps():
            condition = step.get("if", "success()")
            run = {"success()": not failed, "always()": True,
                   "inputs.publish": not failed and publish}[condition]
            if not run or "run" not in step:
                continue
            env = dict(os.environ, GITHUB_REF=ref, RUNNER_TEMP=str(temp), KEY_COPY=str(tmp / "key-copy"))
            for name, value in step["env"].items():
                env[name] = values[value[4:-3]]
            done = subprocess.run(["bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", step["run"]],
                                  cwd=tree, env=env, text=True, capture_output=True, check=False)
            results.append((step["name"], done))
            failed = failed or done.returncode != 0
        return results, log.read_text() if log.exists() else "", temp

    def assert_no_secret_shown(self, results: list[tuple[str, subprocess.CompletedProcess[str]]]) -> None:
        """Check that no step's output shows the passphrase or the key."""
        for name, done in results:
            for secret in (KEY_PHRASE, "fixture key, not a key"):
                self.assertNotIn(secret, done.stdout + done.stderr, name)

    def test_an_alpha_is_signed_without_reports_and_the_key_removed(self) -> None:
        """An alpha or an app is signed without reports, the key reaching openssl intact and removed after."""
        for tag in ("os-v0.1.0-alpha.1", "app-v0.1.0-alpha.1", "app-v0.1.0"):
            with self.subTest(tag=tag):
                results, log, temp = self.run_job(tag)
                self.assertTrue(all(done.returncode == 0 for _, done in results), results)
                self.assertIn(f"args {tag}\n", log)
                self.assertIn(f"file {temp}/release-key.pem\n", log)
                self.assertIn("mode 600\n", log)
                self.assertIn("passin env:SOUNDTAP_RELEASE_KEY_PASSPHRASE\n", log)
                self.assertIn("passphrase reaches openssl\n", log)
                self.assertIn("key intact\n", log)
                self.assertNotIn("--publish", log)
                self.assertEqual(list(temp.iterdir()), [])
                self.assert_no_secret_shown(results)

    def test_a_core_release_is_signed_against_the_committed_reports(self) -> None:
        """A Core release is signed against the committed reports, an alpha's too when there are any."""
        results, log, temp = self.run_job("os-v0.1.0", reports=True)
        self.assertTrue(all(done.returncode == 0 for _, done in results), results)
        self.assertIn("args os-v0.1.0 releases/os-v0.1.0\n", log)
        self.assertEqual(list(temp.iterdir()), [])
        # An alpha's reports, when there are any, are checked as well.
        _, log, _ = self.run_job("os-v0.1.0-alpha.2", reports=True)
        self.assertIn("args os-v0.1.0-alpha.2 releases/os-v0.1.0-alpha.2\n", log)

    def test_a_core_release_without_reports_never_touches_the_key(self) -> None:
        """A Core release without committed reports never touches the key."""
        results, log, temp = self.run_job("os-v0.1.0")
        self.assertIn("no releases/os-v0.1.0 on main", results[-2][1].stderr)
        self.assertEqual(log, "")
        self.assertEqual(list(temp.iterdir()), [])

    def test_publish_comes_after_the_key_is_gone(self) -> None:
        """Publishing comes after the key is removed."""
        results, log, temp = self.run_job("os-v0.1.0-alpha.1", publish=True)
        self.assertTrue(all(done.returncode == 0 for _, done in results), results)
        self.assertEqual([name for name, _ in results][-2:], ["Remove the release key", "Publish the signed draft"])
        self.assertTrue(log.endswith("args --publish os-v0.1.0-alpha.1\n"), log)
        self.assertEqual(list(temp.iterdir()), [])

    def test_a_failed_signature_removes_the_key_and_publishes_nothing(self) -> None:
        """A failed signature still removes the key and publishes nothing."""
        results, log, temp = self.run_job("app-v0.1.0", publish=True, fail=True)
        self.assertNotEqual(results[-2][1].returncode, 0)
        self.assertEqual(results[-1][0], "Remove the release key")
        self.assertNotIn("--publish", log)
        self.assertEqual(list(temp.iterdir()), [])

    def test_only_main_and_only_release_tags(self) -> None:
        """Only main signs, and only a release tag, before the key is touched."""
        for tag, ref in (("os-v0.1.0-alpha.1", "refs/heads/feature"),
                         ("os-v0.1.0-alpha.1", "refs/tags/os-v0.1.0-alpha.1"),
                         ("os-v0.1.0-beta.1", "refs/heads/main"),
                         ("os-v0.1.0-alpha.0", "refs/heads/main"),
                         ("candidate-abc", "refs/heads/main"),
                         ("os-v0.1.0; echo pwned", "refs/heads/main"),
                         ("os-v0.1.0\nos-v0.2.0", "refs/heads/main"),
                         ("app-v$(id)", "refs/heads/main")):
            with self.subTest(tag=tag, ref=ref):
                results, log, temp = self.run_job(tag, ref=ref, reports=True)
                self.assertNotEqual(results[0][1].returncode, 0)
                self.assertEqual(log, "")
                self.assertEqual(list(temp.iterdir()), [])

    def test_an_empty_key_secret_is_refused(self) -> None:
        """An empty key secret is refused."""
        results, log, temp = self.run_job("app-v0.1.0", key="")
        self.assertIn("no SOUNDTAP_RELEASE_KEY", results[-2][1].stderr)
        self.assertEqual(log, "")
        self.assertEqual(list(temp.iterdir()), [])
