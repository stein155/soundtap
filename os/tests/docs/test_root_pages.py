# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The docs site's root-pages hook (docs/tools/root_pages.py): the README as the home page,
its links pointing at the site or at GitHub, and GitHub alerts as admonitions. Runs the
hook's rewriting functions on sample Markdown; MkDocs itself is not needed."""
import importlib.util
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
_spec = importlib.util.spec_from_file_location("root_pages", ROOT / "docs/tools/root_pages.py")
root_pages = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(root_pages)
BLOB = "https://github.com/stein155/soundtap/blob/main/"


class RootPagesTest(unittest.TestCase):
    def test_the_home_page_links_to_the_site_or_to_github(self):
        readme = ('<img src="docs/assets/hero.png"> <a href="docs/owners/install.md">Install</a> '
                  '<a href="CONTRIBUTING.md">Contribute</a> <a href="#how">How</a> '
                  '<a href="https://example.org/x">x</a>\n'
                  "[FAQ](docs/owners/faq.md#does-it-phone-home) [ADR](docs/decisions/0049-no-stock-bytes.md) "
                  "[log](CHANGELOG.md) [license](LICENSE) [tools](docs/tools/) [top](#how)\n")
        out = root_pages.rewrite(readme, "index.md", BLOB)
        self.assertEqual(out, (
            '<img src="assets/hero.png"> <a href="owners/install/">Install</a> '
            f'<a href="{BLOB}CONTRIBUTING.md">Contribute</a> <a href="#how">How</a> '
            '<a href="https://example.org/x">x</a>\n'
            "[FAQ](owners/faq.md#does-it-phone-home) [ADR](decisions/0049-no-stock-bytes.md) "
            "[log](changelog.md) [license](../LICENSE) [tools](tools/) [top](#how)\n"))

    def test_docs_pages_reach_the_root_pages_on_the_site_and_keep_other_links(self):
        page = ("[home](../../README.md) [runbook](../../RUNBOOK.md#snapcast-clients) "
                "[map](../README.md) [design](../DESIGN.md) [releasing](../../RELEASING.md)\n")
        self.assertEqual(root_pages.rewrite(page, "st20/README.md", BLOB), (
            "[home](../index.md) [runbook](../runbook.md#snapcast-clients) "
            "[map](../documentation.md) [design](../DESIGN.md) [releasing](../../RELEASING.md)\n"))

    def test_the_documentation_map_keeps_its_own_relative_links(self):
        page = "[home](../README.md) [install](install.md) [contributing](../CONTRIBUTING.md)\n"
        self.assertEqual(root_pages.rewrite(page, "documentation.md", BLOB),
                         "[home](index.md) [install](install.md) [contributing](../CONTRIBUTING.md)\n")

    def test_github_alerts_become_admonitions(self):
        text = "Before.\n\n> [!WARNING]\n> SoundTap is **alpha**.\n>\n> Second paragraph.\n\nAfter.\n"
        self.assertEqual(root_pages.alerts(text), "Before.\n\n!!! warning\n\n    SoundTap is **alpha**.\n"
                                                  "\n    Second paragraph.\n\n\nAfter.\n")
        self.assertEqual(root_pages.alerts("> A plain quote.\n"), "> A plain quote.\n")

    def test_every_page_the_hook_adds_exists_and_is_in_the_navigation(self):
        nav = set(re.findall(r":\s+(\S+\.md)\s*$", (ROOT / "mkdocs.yml").read_text(), re.M))
        for repo, site in root_pages.PAGES.items():
            with self.subTest(page=repo):
                self.assertTrue((ROOT / repo).is_file())
                self.assertIn(site, nav)
                self.assertFalse((ROOT / "docs" / site).exists(), f"docs/{site} would clash")


if __name__ == "__main__":
    unittest.main()
