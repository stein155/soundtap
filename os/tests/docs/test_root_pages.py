# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The docs site's root-pages hook (docs/tools/root_pages.py).

The README as the overview page, the landing page as the home page in every web UI language, its links pointing at the site or at GitHub, and GitHub alerts as
admonitions. Runs the hook's rewriting functions on sample Markdown; MkDocs itself is not needed.
"""
import importlib.util
import json
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
_spec = importlib.util.spec_from_file_location("root_pages", ROOT / "docs/tools/root_pages.py")
root_pages = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(root_pages)
BLOB = "https://github.com/stein155/soundtap/blob/main/"


class RootPagesTest(unittest.TestCase):
    """The hook's link rewriting, alerts and page list."""

    def test_the_readme_links_to_the_site_or_to_github(self) -> None:
        """The README's links reach the site's pages and files, or GitHub for the rest.

        Its raw HTML is served from overview/, so those links climb one directory more.
        """
        readme = ('<img src="docs/assets/hero.png"> <a href="docs/owners/install.md">Install</a> '
                  '<a href="CONTRIBUTING.md">Contribute</a> <a href="#how">How</a> '
                  '<a href="https://example.org/x">x</a>\n'
                  "[FAQ](docs/owners/faq.md#does-it-phone-home) [ADR](docs/decisions/0049-no-stock-bytes.md) "
                  "[log](CHANGELOG.md) [license](LICENSE) [tools](docs/tools/) [top](#how)\n")
        out = root_pages.rewrite(readme, "overview.md", BLOB)
        self.assertEqual(out, (
            '<img src="../assets/hero.png"> <a href="../owners/install/">Install</a> '
            f'<a href="{BLOB}CONTRIBUTING.md">Contribute</a> <a href="#how">How</a> '
            '<a href="https://example.org/x">x</a>\n'
            "[FAQ](owners/faq.md#does-it-phone-home) [ADR](decisions/0049-no-stock-bytes.md) "
            "[log](changelog.md) [license](../LICENSE) [tools](tools/) [top](#how)\n"))

    def test_docs_pages_reach_the_root_pages_on_the_site_and_keep_other_links(self) -> None:
        """A docs page's links to root pages go to the site's copies; other links stay."""
        page = ("[home](../../README.md) [runbook](../../RUNBOOK.md#snapcast-clients) "
                "[map](../README.md) [design](../DESIGN.md) [releasing](../../RELEASING.md)\n")
        self.assertEqual(root_pages.rewrite(page, "st20/README.md", BLOB), (
            "[home](../overview.md) [runbook](../runbook.md#snapcast-clients) "
            "[map](../documentation.md) [design](../DESIGN.md) [releasing](../../RELEASING.md)\n"))

    def test_the_documentation_map_keeps_its_own_relative_links(self) -> None:
        """The documentation map's links stay relative to its own place."""
        page = "[home](../README.md) [install](install.md) [contributing](../CONTRIBUTING.md)\n"
        self.assertEqual(root_pages.rewrite(page, "documentation.md", BLOB),
                         "[home](overview.md) [install](install.md) [contributing](../CONTRIBUTING.md)\n")

    def test_github_alerts_become_admonitions(self) -> None:
        """GitHub alert blocks become admonitions; a plain quote stays."""
        text = "Before.\n\n> [!WARNING]\n> SoundTap is **alpha**.\n>\n> Second paragraph.\n\nAfter.\n"
        self.assertEqual(root_pages.alerts(text), "Before.\n\n!!! warning\n\n    SoundTap is **alpha**.\n"
                                                  "\n    Second paragraph.\n\n\nAfter.\n")
        self.assertEqual(root_pages.alerts("> A plain quote.\n"), "> A plain quote.\n")

    def test_every_page_the_hook_adds_exists_and_is_in_the_navigation(self) -> None:
        """Every page the hook adds exists, is in the navigation and clashes with no docs file."""
        nav = set(re.findall(r":\s+(\S+\.md)\s*$", (ROOT / "mkdocs.yml").read_text(), re.MULTILINE))
        for repo, site in root_pages.PAGES.items():
            with self.subTest(page=repo):
                self.assertTrue((ROOT / repo).is_file())
                self.assertIn(site, nav)
                self.assertFalse((ROOT / "docs" / site).exists(), f"docs/{site} would clash")

    def test_the_landing_page_is_the_home_template(self) -> None:
        """The hook's home page is in the navigation, uses the landing template and clashes with nothing."""
        nav = set(re.findall(r":\s+(\S+\.md)\s*$", (ROOT / "mkdocs.yml").read_text(), re.MULTILINE))
        self.assertIn(root_pages.HOME, nav)
        self.assertNotIn(root_pages.HOME, root_pages.PAGES.values())
        self.assertFalse((ROOT / "docs" / root_pages.HOME).exists(), "a Markdown home page would clash")
        template = re.search(r"^template: (\S+)$", root_pages.HOME_MARKDOWN, re.MULTILINE).group(1)
        self.assertTrue((ROOT / "docs/overrides" / template).is_file())

    def test_the_landing_comes_in_every_web_ui_language(self) -> None:
        """One landing page per web UI language, each with every English text of its own.

        The languages are the web app's, in its order; a text left in English, a key missing or
        extra, or HTML that differs from the English fails.
        """
        index = (ROOT / "app/frontend/src/lib/i18n/index.ts").read_text(encoding="utf-8")
        ui = re.findall(r"\{ code: '([a-z-]+)', label: '([^']+)'", index)
        self.assertEqual([c for c, _ in ui], list(root_pages.HREFLANG))
        landing = ROOT / "docs" / root_pages.LANDING
        english = json.loads((landing / "en.json").read_text(encoding="utf-8"))
        tags = re.compile(r"<[^>]+>|&[a-z]+;")
        for code, label in ui:
            with self.subTest(code=code):
                own = json.loads((landing / f"{code}.json").read_text(encoding="utf-8"))
                self.assertEqual(sorted(own), sorted(english))
                self.assertEqual(own["language"], label)
                for key, text in own.items():
                    self.assertEqual(tags.findall(text), tags.findall(english[key]), key)
                if code != "en":
                    same = [k for k in ("hero_lead", "new_title", "still_text", "install_title")
                            if own[k] == english[k]]
                    self.assertEqual(same, [], "left in English")

    def test_a_translated_landing_is_its_own_page_kept_out_of_search(self) -> None:
        """nl/index.md carries the home template, its language and its own title."""
        self.assertEqual(root_pages.landing_page("en"), "index.md")
        self.assertEqual(root_pages.landing_page("nl"), "nl/index.md")
        md = root_pages.landing_markdown("nl", {"title": "Een tweede leven: \"SoundTouch\""})
        self.assertEqual(md, ('---\ntemplate: home.html\nlanding_lang: nl\n'
                              'title: "Een tweede leven: \\"SoundTouch\\""\n'
                              'search:\n  exclude: true\n---\n'))

    def test_a_translated_landing_names_its_language_and_has_a_sitemap(self) -> None:
        """<html lang> becomes the page's language, and nl/sitemap.xml lists nl/."""
        html = '<!doctype html>\n<html lang="en" class="no-js">\n<body lang="en"></body>'
        self.assertEqual(root_pages.set_html_lang(html, "zh-Hant"),
                         '<!doctype html>\n<html lang="zh-Hant" class="no-js">\n<body lang="en"></body>')
        sitemap = root_pages.landing_sitemap("https://example.org/soundtap/", "nl/")
        self.assertIn("<loc>https://example.org/soundtap/nl/</loc>", sitemap)


if __name__ == "__main__":
    unittest.main()
