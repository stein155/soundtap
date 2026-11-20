#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""sign-release.sh signs an OS release only with passing reports for all four models made
against the exact asset, and leaves it a draft; --publish makes public only a draft whose
.sig states that release. gh and sign.sh are stubbed: nothing reaches GitHub and no key is
used."""
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))
from tools.release import release_approval as ra  # noqa: E402

ASSET = b"universal STU bytes"


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def write_reports(d: Path, asset_sha=None, fail=None, skip=None):
    asset_sha = asset_sha or sha(ASSET)
    d.mkdir(parents=True, exist_ok=True)
    for model, spec in ra.MODELS.items():
        if model == skip:
            continue
        evidence = {}
        for name in spec["evidence"]:
            f = d / f"hardware-{model}-{name}.txt"
            f.write_text(f"{model} {name}\n")
            evidence[name] = {"path": f.name, "sha256": sha(f.read_bytes())}
        checks = dict.fromkeys(spec["checks"], "pass")
        if fail and fail[0] == model:
            checks[fail[1]] = "fail"
        report = {
            "schema_version": 2, "target": model, "completed_at": "2026-10-24T12:00:00Z",
            "artifact": {"name": "Update-soundtap.stu", "sha256": asset_sha},
            "device": spec["device"], "checks": checks, "evidence": evidence,
        }
        (d / f"hardware-{model}.json").write_text(json.dumps(report))


def statement(kind="core", version="1.0.0", profile="production", data=ASSET):
    """A .sig line as sign.sh writes it; the signature itself is not checked here."""
    return (f"soundtap-package v1 kind={kind} profile={profile} version={version} "
            f"sha256={sha(data)} sig={'A' * 86}==\n")


class SignReleaseTests(unittest.TestCase):
    def run_sign(self, tag, reports, publish=False, sig=None, draft=True):
        tmp = Path(self.enterContextTemp())
        release = tmp / "release"
        release.mkdir()
        (release / "Update-soundtap.stu").write_bytes(ASSET)
        (release / "Update-soundtap.stu.sha256").write_text(
            f"{sha(ASSET)}  Update-soundtap.stu\n")
        if sig is not None:
            (release / "Update-soundtap.stu.sig").write_text(sig)
        bin_ = tmp / "bin"
        bin_.mkdir()
        log = tmp / "gh.log"
        (bin_ / "gh").write_text(f"""#!/bin/sh
echo "$*" >> {log}
case "$1 $2" in
"release view") echo {'true' if draft else 'false'} ;;
"release download") cp {release}/* "$5" ;;
esac
""")
        (bin_ / "gh").chmod(0o755)
        tools = tmp / "tools"
        shutil.copytree(ROOT / "tools/release", tools / "release")
        shutil.copytree(ROOT / "os/support", tmp / "os/support")
        (tools / "__init__.py").touch()
        (tmp / "os/board").mkdir(parents=True)
        shutil.copy(ROOT / "os/board/nand-geometry.json", tmp / "os/board")
        signed = tmp / "sign.log"
        (tools / "release" / "sign.sh").write_text(
            "#!/bin/sh\n"
            f'echo "$SOUNDTAP_PACKAGE_KIND $SOUNDTAP_SIGN_PROFILE $SOUNDTAP_PACKAGE_VERSION" >> {signed}\n'
            'touch "$1.sig"\n')
        (tools / "release" / "sign.sh").chmod(0o755)
        env = dict(os.environ, PATH=f"{bin_}:{os.environ['PATH']}")
        args = ["sh", str(tools / "release/sign-release.sh")]
        if publish:
            args.append("--publish")
        args.append(tag)
        if reports is not None:
            args.append(str(reports))
        done = subprocess.run(args, env=env, capture_output=True, text=True)
        self.calls = log.read_text() if log.exists() else ""
        self.uploaded = "release upload" in self.calls
        self.signed = signed.read_text() if signed.exists() else ""
        return done, "--draft=false" in self.calls

    def enterContextTemp(self):
        d = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, d)
        return d

    def reports(self, **kw):
        d = Path(self.enterContextTemp()) / "reports"
        write_reports(d, **kw)
        return d

    def test_all_four_models_pass(self):
        done, published = self.run_sign("os-v1.0.0", self.reports())
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertEqual(self.signed, "core production 1.0.0\n")
        self.assertTrue(self.uploaded)
        # Signed, still a draft: the maintainer's bench speakers take it first.
        self.assertFalse(published)

    def test_os_release_without_reports_is_refused(self):
        done, published = self.run_sign("os-v1.0.0", None)
        self.assertNotEqual(done.returncode, 0)
        self.assertFalse(self.uploaded)
        self.assertFalse(published)

    def test_missing_wla_report_is_refused(self):
        done, _published = self.run_sign("os-v1.0.0", self.reports(skip="wla"))
        self.assertIn("no wla hardware report", done.stderr)
        self.assertFalse(self.uploaded)

    def test_missing_st300_report_is_refused(self):
        done, _published = self.run_sign("os-v1.0.0", self.reports(skip="st300"))
        self.assertIn("no st300 hardware report", done.stderr)
        self.assertFalse(self.uploaded)

    def test_failed_st300_tv_input_is_refused(self):
        done, _published = self.run_sign("os-v1.0.0", self.reports(fail=("st300", "tv-input")))
        self.assertIn("tv-input", done.stderr)
        self.assertFalse(self.uploaded)

    def test_failed_wla_rf_check_is_refused(self):
        done, _published = self.run_sign("os-v1.0.0", self.reports(fail=("wla", "rf-config")))
        self.assertIn("rf-config", done.stderr)
        self.assertFalse(self.uploaded)

    def test_report_for_other_bytes_is_refused(self):
        done, _published = self.run_sign("os-v1.0.0", self.reports(asset_sha=sha(b"other")))
        self.assertIn("other bytes", done.stderr)
        self.assertFalse(self.uploaded)

    def test_changed_evidence_is_refused(self):
        d = self.reports()
        (d / "hardware-st20-dmesg.txt").write_text("edited afterwards\n")
        done, _published = self.run_sign("os-v1.0.0", d)
        self.assertIn("hash mismatch", done.stderr)
        self.assertFalse(self.uploaded)

    def test_app_release_needs_no_reports(self):
        done, published = self.run_sign("app-v1.0.0", None)
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertTrue(self.uploaded)
        self.assertFalse(published)

    def test_app_release_is_signed_as_the_app_of_its_tag(self):
        # Not os/VERSION: an app release names its own version (ADR 0048).
        done, _ = self.run_sign("app-v2.3.4", None)
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertEqual(self.signed, "app production 2.3.4\n")

    def test_other_tags_are_not_signed(self):
        done, published = self.run_sign("candidate-abc", None)
        self.assertNotEqual(done.returncode, 0)
        self.assertEqual(self.signed, "")
        self.assertFalse(published)

    def test_publish_makes_the_signed_draft_public(self):
        done, published = self.run_sign("os-v1.0.0", None, publish=True, sig=statement())
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertTrue(published)
        self.assertIn("--latest\n", self.calls)
        # Publishing signs nothing and uploads nothing.
        self.assertEqual(self.signed, "")
        self.assertFalse(self.uploaded)

    def test_publish_of_an_app_is_never_latest(self):
        done, published = self.run_sign("app-v2.3.4", None, publish=True,
                                        sig=statement(kind="app", version="2.3.4"))
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertTrue(published)
        self.assertIn("--latest=false", self.calls)

    # ADR 0035: an alpha (X.Y.Z-alpha.N) is signed as a production release without the four
    # hardware reports, says so, and is published as a prerelease, never latest.
    def test_an_alpha_is_signed_without_reports_and_says_so(self):
        done, published = self.run_sign("os-v1.0.0-alpha.1", None)
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertEqual(self.signed, "core production 1.0.0-alpha.1\n")
        self.assertIn("WITHOUT the four hardware reports", done.stderr)
        self.assertTrue(self.uploaded)
        self.assertFalse(published)

    def test_reports_given_for_an_alpha_are_still_checked(self):
        done, _ = self.run_sign("os-v1.0.0-alpha.1", self.reports(fail=("st20", "wifi")))
        self.assertNotEqual(done.returncode, 0)
        self.assertFalse(self.uploaded)

    def test_only_an_alpha_suffix_skips_the_reports(self):
        # Other suffixes still need the reports (and the real sign.sh refuses their version).
        for tag in ("os-v1.0.0-alpha", "os-v1.0.0-beta.1", "os-v1.0.0-rc.1"):
            done, _ = self.run_sign(tag, None)
            self.assertIn("needs the hardware reports", done.stderr, tag)
            self.assertFalse(self.uploaded, tag)

    def test_publish_of_an_alpha_is_a_prerelease_never_latest(self):
        for tag, kind in (("os-v1.0.0-alpha.2", "core"), ("app-v1.0.0-alpha.2", "app")):
            done, published = self.run_sign(tag, None, publish=True,
                                            sig=statement(kind=kind, version="1.0.0-alpha.2"))
            self.assertEqual(done.returncode, 0, done.stderr)
            self.assertTrue(published)
            self.assertIn("--draft=false --prerelease --latest=false\n", self.calls)

    def test_publish_of_a_release_is_not_a_prerelease(self):
        done, published = self.run_sign("os-v1.0.0", None, publish=True, sig=statement())
        self.assertEqual(done.returncode, 0, done.stderr)
        self.assertIn("--draft=false --prerelease=false --latest\n", self.calls)

    def test_publish_of_an_alpha_needs_the_alphas_statement(self):
        done, published = self.run_sign("os-v1.0.0-alpha.2", None, publish=True,
                                        sig=statement(version="1.0.0"))
        self.assertNotEqual(done.returncode, 0)
        self.assertFalse(published)

    def test_publish_needs_this_releases_statement(self):
        for name, sig in {
            "no .sig": None,
            "empty .sig": "",
            "another version": statement(version="0.9.0"),
            "an app": statement(kind="app"),
            "alpha": statement(profile="alpha"),
            "other bytes": statement(data=b"other"),
        }.items():
            done, published = self.run_sign("os-v1.0.0", None, publish=True, sig=sig)
            self.assertNotEqual(done.returncode, 0, name)
            self.assertFalse(published, name)

    def test_publish_needs_a_draft(self):
        done, published = self.run_sign("os-v1.0.0", None, publish=True, sig=statement(),
                                        draft=False)
        self.assertIn("not a draft", done.stderr)
        self.assertFalse(published)


if __name__ == "__main__":
    unittest.main()
