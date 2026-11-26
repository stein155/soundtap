# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""MkDocs hook: the repository's front pages on the site.

README.md, CHANGELOG.md, RUNBOOK.md and SECURITY.md live at the repository root, where GitHub
shows them, outside docs/. This hook adds them to the site, the README as overview.md, so an
owner reads the same pages on github.com and on the site. The home page, index.md, is the
landing page of docs/overrides/home.html; the hook adds it as a page with only that template, once
per web UI language (index.md in English, <lang>/index.md for the others), with the texts of
docs/overrides/landing/<lang>.json.
docs/README.md, the map of the documentation, makes room for it as documentation.md (MkDocs would
otherwise take a README for the index). Links to any of these pages
point at the site's copy; a root page's other links are made relative to docs/, so
github_links.py (the next hook) sends what is not on the site to GitHub. GitHub's alert blocks
(`> [!NOTE]`) become admonitions.

The functions below the hooks do the rewriting and need no MkDocs; os/tests/docs tests them.
"""
from __future__ import annotations

import json
import posixpath
import re
from pathlib import Path
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from mkdocs.config.defaults import MkDocsConfig
    from mkdocs.structure.files import Files
    from mkdocs.structure.pages import Page

# Repository path -> path on the site (relative to docs_dir).
PAGES = {
    "README.md": "overview.md",
    "CHANGELOG.md": "changelog.md",
    "RUNBOOK.md": "runbook.md",
    "SECURITY.md": "security.md",
    "docs/README.md": "documentation.md",
}
SITE = {site: repo for repo, site in PAGES.items()}
# The landing page: its sections are in the template, not in Markdown.
HOME = "index.md"
HOME_MARKDOWN = "---\ntemplate: home.html\ntitle: A second life for your Bose SoundTouch\n---\n"
LANDING = "overrides/landing"
# The web UI's languages (app/frontend/src/lib/i18n/index.ts), in its order, with the BCP 47 tag
# a page in that language carries.
HREFLANG = {"af": "af", "cs": "cs", "da": "da", "de": "de", "en": "en", "es": "es", "fr": "fr",
            "it": "it", "hu": "hu", "nl": "nl", "nb": "nb", "pl": "pl", "pt": "pt-BR", "ro": "ro",
            "sl": "sl", "fi": "fi", "sv": "sv", "tr": "tr", "el": "el", "ru": "ru", "th": "th",
            "ja": "ja", "ko": "ko", "zh-hans": "zh-Hans", "zh-hant": "zh-Hant"}
LINK = re.compile(r"\]\((?!https?:|mailto:|#|/)([^)\s]+)\)")
ATTR = re.compile(r'\b(href|src|srcset)="(?!https?:|mailto:|#|/)([^"\s]+)"')
ALERT = re.compile(r"^> \[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\][ \t]*\n((?:>.*(?:\n|$))*)", re.MULTILINE)
ADMONITION = {"NOTE": "note", "TIP": "tip", "IMPORTANT": "info", "WARNING": "warning",
              "CAUTION": "danger"}


def on_files(files: Files, config: MkDocsConfig) -> Files:
    """Add the root pages and the landing page to the site, docs/README.md as documentation.md."""
    from mkdocs.structure.files import File, InclusionLevel

    root = Path(config["config_file_path"]).resolve().parent
    for repo, site in PAGES.items():
        if repo.startswith("docs/"):
            old = files.get_file_from_path(repo[len("docs/"):])
            if old is None:
                continue
            files.remove(old)
            source, edit = old.abs_src_path, repo[len("docs/"):]
        else:
            source, edit = str(root / repo), "../" + repo
        page = File.generated(config, site, abs_src_path=source)
        page.edit_uri = edit
        files.append(page)
    files.append(File.generated(config, HOME, content=HOME_MARKDOWN))
    for code, texts in landing_texts(Path(config["docs_dir"])).items():
        if code != "en":
            page = File.generated(config, landing_page(code), content=landing_markdown(code, texts))
            page.inclusion = InclusionLevel.NOT_IN_NAV  # reached through the landing's picker
            files.append(page)
    return files


def on_page_context(context: dict, page: Page, config: MkDocsConfig, nav: object) -> dict:
    """Give a landing page its texts (`t`) and the list of languages (`landing_langs`)."""
    del nav
    if page.meta.get("template") != "home.html":
        return context
    texts = landing_texts(Path(config["docs_dir"]))
    code = page.meta.get("landing_lang", "en")
    context["t"] = texts[code]
    context["landing_lang"] = code
    context["landing_hreflang"] = HREFLANG[code]
    context["landing_langs"] = [
        {"code": c, "hreflang": HREFLANG[c], "name": texts[c]["language"],
         "path": "" if c == "en" else posixpath.dirname(landing_page(c)) + "/"}
        for c in HREFLANG]
    return context


def on_post_page(output: str, page: Page, config: MkDocsConfig) -> str:
    """Give a translated landing page its own language on <html> and Material's back-to-top words."""
    code = page.meta.get("landing_lang")
    if not code or code == "en":
        return output
    # Material's own words (search, back to top, skip link) come from the site's one theme
    # language; a landing page takes them from Material's translation for its language.
    top = landing_texts(Path(config["docs_dir"]))[code]["back_to_top"]
    output = set_html_lang(output, HREFLANG[code]).replace("Back to top", top)
    return translate_theme(output, theme_words("en"), theme_words(MATERIAL_LANG.get(code, code)))


def on_post_build(config: MkDocsConfig) -> None:
    """Write a sitemap beside each translated landing page.

    The theme looks up <alternate>/sitemap.xml for every hreflang link the landing carries; without
    one each view costs 24 failed requests.
    """
    site = Path(config["site_dir"])
    base = config["site_url"] or "/"
    for code in HREFLANG:
        if code != "en":
            path = posixpath.dirname(landing_page(code))
            (site / path).mkdir(parents=True, exist_ok=True)
            (site / path / "sitemap.xml").write_text(landing_sitemap(base, path + "/"), encoding="utf-8")


# Material's name for a landing language's translation file, where it differs.
MATERIAL_LANG = {"pt": "pt-BR", "zh-hans": "zh", "zh-hant": "zh-Hant"}

# The theme's words a landing page shows or announces: the search box and its results, the
# skip link and the landmarks' names.
THEME_KEYS = ("search", "search.placeholder", "search.share", "search.reset", "search.result.initializer",
              "search.result.placeholder", "search.result.none", "search.result.one", "search.result.other",
              "search.result.more.one", "search.result.more.other", "search.result.term.missing",
              "action.skip", "header", "footer", "nav", "toc")


def theme_words(lang: str) -> dict[str, str]:
    """Return Material's translation of its own words for lang ({} when it has none)."""
    import material  # the theme is there whenever MkDocs runs this hook

    path = Path(material.__file__).parent / "templates/partials/languages" / f"{lang}.html"
    if not path.exists():
        return {}
    return dict(re.findall(r'^\s*"([a-z.]+)": "(.*)",?$', path.read_text(encoding="utf-8"), re.MULTILINE))


def translate_theme(html: str, english: dict[str, str], words: dict[str, str]) -> str:
    """Put the theme's words in another language.

    Its labels, placeholders and the strings its search script reads from the page's config.
    """
    for key in THEME_KEYS:
        en, there = english.get(key), words.get(key)
        if not en or not there or en == there:
            continue
        html = html.replace(f'"{key}": "{en}"', f'"{key}": "{there}"')
        for attr in ("placeholder", "aria-label", "title"):
            html = html.replace(f'{attr}="{en}"', f'{attr}="{there}"')
        html = re.sub(rf">(\s*){re.escape(en)}(\s*)<", lambda m, t=there: f">{m[1]}{t}{m[2]}<", html)
    return html


def set_html_lang(html: str, tag: str) -> str:
    """Replace the lang attribute of the document's <html> element."""
    return re.sub(r'(<html\b[^>]*?\blang=")[^"]*(")', lambda m: m.group(1) + tag + m.group(2), html, count=1)


def landing_sitemap(base: str, path: str) -> str:
    """Return the sitemap of one translated landing page."""
    url = base.rstrip("/") + "/" + path
    return ('<?xml version="1.0" encoding="UTF-8"?>\n'
            '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n'
            f"  <url><loc>{url}</loc></url>\n</urlset>\n")


def landing_page(code: str) -> str:
    """Return the site path of the landing page in a language."""
    return HOME if code == "en" else f"{code}/{HOME}"


def landing_markdown(code: str, texts: dict[str, str]) -> str:
    """Return the Markdown of a translated landing page: its template, language and title."""
    title = json.dumps(texts["title"], ensure_ascii=False)
    return (f"---\ntemplate: home.html\nlanding_lang: {code}\ntitle: {title}\n"
            "search:\n  exclude: true\n---\n")


def landing_texts(docs: Path) -> dict[str, dict[str, str]]:
    """Read the landing texts of every web UI language, English's keys filling any gap."""
    english = json.loads((docs / LANDING / "en.json").read_text(encoding="utf-8"))
    texts = {}
    for code in HREFLANG:
        path = docs / LANDING / f"{code}.json"
        own = json.loads(path.read_text(encoding="utf-8")) if path.exists() else {}
        texts[code] = {**english, **own}
    return texts


def on_page_markdown(markdown: str, page: Page, config: MkDocsConfig, files: Files) -> str:
    """Rewrite the links and alerts of a page for the site."""
    del files  # MkDocs passes every argument by keyword; this hook needs no file list.
    blob = config["repo_url"].rstrip("/") + "/blob/main/"
    return rewrite(markdown, page.file.src_uri, blob)


def repo_path(src_uri: str) -> str:
    """Return the repository path a site page was read from."""
    return SITE.get(src_uri, "docs/" + src_uri)


def site_path(repo: str) -> str | None:
    """Return a repository path's place on the site, or None when it is not there."""
    if repo in PAGES:
        return PAGES[repo]
    if repo.startswith("docs/"):
        return repo[len("docs/"):]
    return None


def _resolve(target: str, repo_dir: str) -> tuple[str, str, str]:
    path, sep, anchor = target.partition("#")
    full = posixpath.normpath(posixpath.join(repo_dir, path))
    return full, ("/" if path.endswith("/") else ""), sep + anchor


def rewrite(markdown: str, src_uri: str, blob: str) -> str:
    """Rewrite the links of `markdown`, the page at src_uri on the site.

    blob is the GitHub URL files resolve under.
    """
    repo = repo_path(src_uri)
    repo_dir, site_dir = posixpath.dirname(repo), posixpath.dirname(src_uri) or "."
    # Raw HTML is left alone by MkDocs, so its links are relative to the page's URL, which is
    # a directory of its own (overview.md is served as overview/) unless the page is an index.
    stem = posixpath.splitext(src_uri)[0]
    url_dir = site_dir if posixpath.basename(stem) in ("index", "README") else stem
    generated = repo in PAGES and not repo.startswith("docs/")

    def link(m: re.Match[str]) -> str:
        target = m.group(1)
        if target.startswith("#"):
            return m.group(0)
        full, slash, anchor = _resolve(target, repo_dir)
        site = site_path(full)
        if full in PAGES or (generated and site is not None):
            return f"]({posixpath.relpath(site, site_dir)}{slash}{anchor})"
        if generated:  # Relative to docs/, so github_links.py can send it to GitHub.
            return f"]({posixpath.relpath(full, posixpath.join('docs', site_dir))}{slash}{anchor})"
        return m.group(0)

    def attr(m: re.Match[str]) -> str:
        name, target = m.groups()
        full, slash, anchor = _resolve(target, repo_dir)
        site = site_path(full)
        if site is None:
            return f'{name}="{blob}{full}{slash}{anchor}"'
        if site.endswith(".md"):  # A page's URL: MkDocs leaves raw HTML alone.
            stem = site[:-3]
            base = posixpath.dirname(stem) if posixpath.basename(stem) in ("index", "README") else stem
            url = posixpath.relpath(base or ".", url_dir) + "/"
            return f'{name}="{url}{anchor}"'
        return f'{name}="{posixpath.relpath(site, url_dir)}{slash}{anchor}"'

    markdown = LINK.sub(link, markdown)
    if generated:
        markdown = ATTR.sub(attr, markdown)
    return alerts(markdown)


def alerts(markdown: str) -> str:
    """Turn GitHub's `> [!WARNING]` blocks into MkDocs admonitions."""
    def admonition(m: re.Match[str]) -> str:
        body = [re.sub(r"^> ?", "", line) for line in m.group(2).splitlines()]
        return (f"!!! {ADMONITION[m.group(1)]}\n\n"
                + "".join(f"    {line}\n" if line.strip() else "\n" for line in body) + "\n")
    return ALERT.sub(admonition, markdown)
