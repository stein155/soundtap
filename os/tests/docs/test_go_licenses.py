# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The Go module licence check, against fake module caches; no network, no go."""
import importlib.util
import json
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/ci/go_licenses.py"
SPEC = importlib.util.spec_from_file_location("go_licenses", SCRIPT)
lic = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(lic)

# Licence files of fake modules, not this file's own notices.
# REUSE-IgnoreStart
MIT = """MIT License

Copyright (c) 2024 Someone

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction.
"""
BSD3 = """Copyright (c) 2009 The Go Authors. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.
"""
ECLIPSE_DUAL = """Eclipse Public License - v 2.0 (EPL-2.0)

This program and the accompanying materials
are made available under the terms of the Eclipse Public License v2.0
and Eclipse Distribution License v1.0 which accompany this distribution.
"""
# REUSE-IgnoreEnd
GPL2 = """                    GNU GENERAL PUBLIC LICENSE
                       Version 2, June 1991
"""
GPL3_MENTIONING_LGPL = """                    GNU GENERAL PUBLIC LICENSE
                       Version 3, 29 June 2007
... use the GNU Lesser General Public License instead of this License.
"""
LGPL3 = """                   GNU LESSER GENERAL PUBLIC LICENSE
                       Version 3, 29 June 2007
  This version of the GNU Lesser General Public License incorporates
the terms and conditions of version 3 of the GNU General Public License.
"""

MAIN = {"Path": "github.com/stein155/soundtap/app", "Main": True, "Dir": "/src/app"}


def notice(*rows: str) -> str:
    """Return a NOTICE.md with a Go module table holding `rows`."""
    return "\n".join(["## Third-party", "", "| Go module | Licence |", "|---|---|", *rows,
                      "", "## Next section"])


class GoLicensesTest(unittest.TestCase):
    """The licence check on fake modules and on the repository's own NOTICE.md."""

    def setUp(self) -> None:
        """Make a fake module cache."""
        self.tmp = tempfile.TemporaryDirectory()
        self.cache = Path(self.tmp.name)

    def tearDown(self) -> None:
        """Remove the fake module cache."""
        self.tmp.cleanup()

    def module(self, path: str, files: dict[str, str], version: str = "v1.0.0") -> dict:
        """Add module `path` with licence `files` to the cache; return its go list entry."""
        directory = self.cache / f"{path}@{version}"
        directory.mkdir(parents=True)
        for name, text in files.items():
            (directory / name).write_text(text)
        (directory / "go.mod").write_text(f"module {path}\n")
        return {"Path": path, "Version": version, "Dir": str(directory)}

    def check(self, modules: list[dict], notice_text: str) -> list[str]:
        """Return the problems of `modules` (with the main module) against `notice_text`."""
        return lic.problems([MAIN, *modules], lic.notice_modules(notice_text))

    def test_no_modules_passes(self) -> None:
        """A build with no modules and no rows passes."""
        self.assertEqual(self.check([], notice()), [])

    def test_listed_permissive_modules_pass(self) -> None:
        """Permissive modules with matching rows pass, backticked or not."""
        modules = [self.module("example.com/a", {"LICENSE": MIT}),
                   self.module("golang.org/x/sys", {"LICENSE": BSD3})]
        rows = ["| `example.com/a` | MIT |", "| golang.org/x/sys | BSD-3-Clause |"]
        self.assertEqual(self.check(modules, notice(*rows)), [])

    def test_a_module_without_a_row_fails(self) -> None:
        """A module without a row fails and the message names its licence."""
        errors = self.check([self.module("example.com/a", {"LICENSE": MIT})], notice())
        self.assertEqual(len(errors), 1)
        self.assertIn("example.com/a v1.0.0 has no row", errors[0])
        self.assertIn("MIT", errors[0])

    def test_gpl2_only_is_refused_and_or_later_allowed(self) -> None:
        """GPL-2.0-only is refused and GPL-2.0-or-later allowed."""
        modules = [self.module("example.com/gpl", {"COPYING": GPL2})]
        errors = self.check(modules, notice("| example.com/gpl | GPL-2.0-only |"))
        self.assertEqual(len(errors), 1)
        self.assertIn("GPL-2.0-only is not compatible", errors[0])
        self.assertEqual(self.check(modules, notice("| example.com/gpl | GPL-2.0-or-later |")), [])

    def test_a_row_that_disagrees_with_the_licence_file_fails(self) -> None:
        """A row that names another licence than the licence file fails."""
        modules = [self.module("example.com/a", {"LICENSE": BSD3})]
        errors = self.check(modules, notice("| example.com/a | MIT |"))
        self.assertTrue(any("NOTICE.md says MIT, its licence file BSD-3-Clause" in e for e in errors),
                        errors)

    def test_the_gpl3_text_is_not_taken_for_the_lgpl(self) -> None:
        """The GPL-3.0 text, which mentions the LGPL, is not classified as the LGPL."""
        self.assertEqual(lic.classify(GPL3_MENTIONING_LGPL), "GPL-3.0")
        self.assertEqual(lic.classify(LGPL3), "LGPL-3.0")
        self.assertEqual(lic.classify((ROOT / "LICENSE").read_text()), "GPL-3.0")

    def test_dual_licensed_module_lists_both(self) -> None:
        """A module with two licence files needs both in its row."""
        modules = [self.module("example.com/dual", {"LICENSE-MIT": MIT, "LICENSE-BSD": BSD3})]
        self.assertEqual(self.check(modules, notice("| example.com/dual | MIT, BSD-3-Clause |")), [])
        self.assertNotEqual(self.check(modules, notice("| example.com/dual | MIT |")), [])

    def test_an_eclipse_choice_of_licences_passes_on_its_bsd_alternative(self) -> None:
        """An EPL-2.0 or BSD-3-Clause choice passes on its BSD alternative; EPL-2.0 alone does not."""
        modules = [self.module("example.com/paho", {"LICENSE": ECLIPSE_DUAL})]
        self.assertEqual(lic.classify(ECLIPSE_DUAL), "EPL-2.0 OR BSD-3-Clause")
        self.assertEqual(self.check(modules, notice("| example.com/paho | EPL-2.0 OR BSD-3-Clause |")), [])
        errors = self.check(modules, notice("| example.com/paho | EPL-2.0 |"))
        self.assertTrue(any("EPL-2.0 is not compatible" in e for e in errors), errors)
        self.assertTrue(lic.allowed("EPL-2.0 OR BSD-3-Clause"))
        self.assertFalse(lic.allowed("EPL-2.0 OR GPL-2.0-only"))

    def test_unknown_or_missing_licence_text_fails(self) -> None:
        """An unknown licence text or a module without a licence file fails."""
        unknown = [self.module("example.com/odd", {"LICENSE": "Do what you like."})]
        errors = self.check(unknown, notice("| example.com/odd | MIT |"))
        self.assertTrue(any("cannot tell the licence of LICENSE" in e for e in errors), errors)
        missing = [self.module("example.com/none", {})]
        errors = self.check(missing, notice("| example.com/none | MIT |"))
        self.assertTrue(any("has no LICENSE or COPYING file" in e for e in errors), errors)

    def test_a_module_outside_the_cache_fails(self) -> None:
        """A module missing from the cache fails with the command that fetches it."""
        errors = self.check([{"Path": "example.com/a", "Version": "v1.0.0"}],
                            notice("| example.com/a | MIT |"))
        self.assertEqual(errors, ["example.com/a v1.0.0 is not in the module cache: "
                                  "run `go mod download` in app/"])

    def test_a_replacement_is_read_from_its_own_directory(self) -> None:
        """A replaced module is read from its replacement's directory."""
        replaced = self.module("example.com/fork", {"LICENSE": MIT})
        module = {"Path": "example.com/a", "Version": "v1.0.0", "Replace": replaced}
        self.assertEqual(self.check([module], notice("| example.com/a | MIT |")), [])

    def test_only_built_modules_need_a_row(self) -> None:
        """Only modules the build uses need a row."""
        built = self.module("example.com/a", {"LICENSE": MIT})
        test_only = self.module("example.com/cmp", {"LICENSE": BSD3})
        rows = notice("| example.com/a | MIT |")
        self.assertEqual(lic.problems([MAIN, built, test_only], lic.notice_modules(rows),
                                      {"example.com/a"}), [])
        errors = lic.problems([MAIN, built, test_only], lic.notice_modules(notice()),
                              {"example.com/a"})
        self.assertEqual(len(errors), 1)
        self.assertIn("example.com/a v1.0.0 has no row", errors[0])

    def test_a_row_for_an_unused_module_fails(self) -> None:
        """A row for a module go.mod does not use fails."""
        errors = self.check([], notice("| example.com/gone | MIT |"))
        self.assertEqual(errors, ["NOTICE.md lists example.com/gone, which app/go.mod does not use"])

    def test_the_script_reads_go_list_output(self) -> None:
        """The script reads go list JSON and exits 0 with the row and 1 without it."""
        modules = [MAIN, self.module("example.com/a", {"LICENSE": MIT})]
        listing = self.cache / "list.json"
        listing.write_text("".join(json.dumps(m, indent="\t") + "\n" for m in modules))
        notice_file = self.cache / "NOTICE.md"
        for row, code in (("| example.com/a | MIT |", 0), ("", 1)):
            notice_file.write_text(notice(row) if row else notice())
            run = subprocess.run([sys.executable, str(SCRIPT), "--go-list-json", str(listing),
                                  "--notice", str(notice_file)], check=False, capture_output=True, text=True)
            self.assertEqual(run.returncode, code, run.stderr)

    def test_the_repository_notice_rows_are_in_go_mod(self) -> None:
        """No row is left over and each is allowed; make lint-go-licenses checks the rest."""
        go_mod = (ROOT / "app/go.mod").read_text()
        blocks = re.findall(r"^require \((.*?)^\)", go_mod, re.MULTILINE | re.DOTALL)
        lines = [line for block in blocks for line in block.splitlines()]
        lines += re.findall(r"^require (\S+ \S+)", go_mod, re.MULTILINE)
        required = {line.split()[0] for line in lines if line.strip() and not line.strip().startswith("//")}
        rows = lic.notice_modules((ROOT / "NOTICE.md").read_text())
        self.assertLessEqual(set(rows), required)
        for path, licences in rows.items():
            self.assertTrue(all(lic.allowed(entry) for entry in licences), f"{path}: {licences}")


if __name__ == "__main__":
    unittest.main()
