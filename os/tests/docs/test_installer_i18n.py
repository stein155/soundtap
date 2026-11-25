# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The installer page's languages (docs/installer/i18n/) are the web UI's 25.

Each has exactly the English keys, filled in, translated and with the same placeholders and markup.
"""
import json
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
INSTALLER = ROOT / "docs/installer"
I18N = INSTALLER / "i18n"
PAGE = (INSTALLER / "index.html").read_text()
ENGLISH = json.loads((I18N / "en.json").read_text())
PLACEHOLDER = re.compile(r"\{\w+\}")
TAG = re.compile(r"</?(?:b|i|code|kbd|a)>")
# A string with more than this many lower-case words is prose, which a translation changes.
MAX_NAME_WORDS = 2


def ui_languages() -> list[tuple[str, str]]:
    """Return (code, native name) of the web UI's language picker."""
    source = (ROOT / "app/frontend/src/lib/i18n/index.ts").read_text()
    return re.findall(r"\{ code: '([\w-]+)', label: '([^']+)'", source)


def page_languages() -> list[tuple[str, str]]:
    """Return (code, native name) of the installer page's language picker."""
    block = re.search(r"const LANGS = \[(.*?)\];", PAGE, re.DOTALL).group(1)
    return re.findall(r"\['([\w-]+)', '([^']+)'\]", block)


def normalized(markup: str) -> str:
    """Return markup with attributes dropped and whitespace collapsed, as en.json writes it."""
    return re.sub(r"\s+", " ", re.sub(r"<(/?\w+)[^>]*>", r"<\1>", markup)).strip()


class InstallerLanguagesTest(unittest.TestCase):
    """The installer's translations against English and the web UI."""

    def test_the_page_offers_the_web_uis_languages(self) -> None:
        """The page offers the web UI's 25 languages in the same order."""
        self.assertEqual(len(ui_languages()), 25)
        self.assertEqual(page_languages(), ui_languages())

    def test_one_file_per_language(self) -> None:
        """There is one translation file per language, and no other."""
        files = sorted(p.stem for p in I18N.glob("*.json"))
        self.assertEqual(files, sorted(code for code, _ in ui_languages()))

    def test_every_language_has_the_english_keys_filled_in(self) -> None:
        """Every language has exactly the English keys, each a non-empty string."""
        for path in sorted(I18N.glob("*.json")):
            with self.subTest(path.name):
                strings = json.loads(path.read_text())
                self.assertEqual(list(strings), list(ENGLISH))
                self.assertEqual([k for k, v in strings.items() if not isinstance(v, str) or not v.strip()], [])

    def test_placeholders_and_markup_match_english(self) -> None:
        """Every string keeps the English placeholders, tags and links."""
        for path in sorted(I18N.glob("*.json")):
            strings = json.loads(path.read_text())
            for key, english in ENGLISH.items():
                with self.subTest(f"{path.name}: {key}"):
                    self.assertEqual(sorted(PLACEHOLDER.findall(strings[key])), sorted(PLACEHOLDER.findall(english)))
                    self.assertEqual(sorted(TAG.findall(strings[key])), sorted(TAG.findall(english)))
                    # Each link keeps its place among the links: a data-href list is in order.
                    self.assertEqual(strings[key].count("<a>"), english.count("<a>"))

    def test_translations_are_not_left_in_english(self) -> None:
        """No language leaves prose in English.

        A string that is nothing but names and values ("SoundTap Core {version} · {size} MB")
        can read the same; prose cannot.
        """
        for path in sorted(I18N.glob("*.json")):
            if path.stem == "en":
                continue
            strings = json.loads(path.read_text())
            same = [k for k, v in ENGLISH.items()
                    if strings[k] == v and len(re.findall(r"[a-z]{4,}", PLACEHOLDER.sub("", v))) > MAX_NAME_WORDS]
            with self.subTest(path.name):
                self.assertEqual(same, [], "translate these")

    def test_every_key_the_page_uses_exists_and_every_string_is_used(self) -> None:
        """Every key the page uses is in en.json and every string there is used."""
        used = set(re.findall(r'data-i18n(?:-label|-placeholder)?="([\w.]+)"', PAGE))
        used |= set(re.findall(r"\b[tp]\('([\w.]+)'(?=\s*[,)])", PAGE))
        used |= set(re.findall(r"\b(?:show|p)\((?:[^,()]+|\$\('\w+'\)), '([\w.]+)'(?=\s*[,)])", PAGE))
        # Built from parts: os.<system>.1, os.<system>.2 and the setup-mode choice.
        used |= {f"os.{system}.{n}" for system in ("mac", "windows", "linux") for n in (1, 2)}
        used |= {"setup.st1020", "setup.manual", "setup.any"}
        self.assertEqual(sorted(used - set(ENGLISH)), [], "keys missing from en.json")
        self.assertEqual(sorted(set(ENGLISH) - used), [], "strings the page never shows")

    def test_the_pages_own_english_is_en_json(self) -> None:
        """Without JavaScript or before a language loads, the page reads en.json's text."""
        elements = re.findall(r'<(\w+)\b[^>]*\bdata-i18n="([\w.]+)"[^>]*>(.*?)</\1>', PAGE, re.DOTALL)
        self.assertGreater(len(elements), 40)
        for _tag, key, inner in elements:
            with self.subTest(key):
                self.assertEqual(normalized(inner), ENGLISH[key])

    def test_links_in_a_string_have_their_addresses(self) -> None:
        """Each <a> of a string on the page takes the next address of the element's data-href."""
        for tag in re.findall(r"<\w+\b[^>]*\bdata-i18n=\"[\w.]+\"[^>]*>", PAGE):
            key = re.search(r'data-i18n="([\w.]+)"', tag).group(1)
            hrefs = re.search(r'data-href="([^"]*)"', tag)
            with self.subTest(key):
                self.assertEqual(len(hrefs.group(1).split()) if hrefs else 0, ENGLISH[key].count("<a>"))

if __name__ == "__main__":
    unittest.main()
