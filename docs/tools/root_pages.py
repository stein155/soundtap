# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""MkDocs hook: the repository's front pages on the site.

README.md, CHANGELOG.md, RUNBOOK.md and SECURITY.md live at the repository root, where GitHub
shows them, outside docs/. This hook adds them to the site, the README as its home page, so an
owner reads the same pages on github.com and on the site. docs/README.md, the map of the
documentation, makes room for the home page as documentation.md. Links to any of these pages
point at the site's copy; a root page's other links are made relative to docs/, so
github_links.py (the next hook) sends what is not on the site to GitHub. GitHub's alert blocks
(`> [!NOTE]`) become admonitions.

The functions below the hooks do the rewriting and need no MkDocs; os/tests/docs tests them.
"""
from __future__ import annotations

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
    "README.md": "index.md",
    "CHANGELOG.md": "changelog.md",
    "RUNBOOK.md": "runbook.md",
    "SECURITY.md": "security.md",
    "docs/README.md": "documentation.md",
}
SITE = {site: repo for repo, site in PAGES.items()}
LINK = re.compile(r"\]\((?!https?:|mailto:|#|/)([^)\s]+)\)")
ATTR = re.compile(r'\b(href|src|srcset)="(?!https?:|mailto:|#|/)([^"\s]+)"')
ALERT = re.compile(r"^> \[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\][ \t]*\n((?:>.*(?:\n|$))*)", re.MULTILINE)
ADMONITION = {"NOTE": "note", "TIP": "tip", "IMPORTANT": "info", "WARNING": "warning",
              "CAUTION": "danger"}


def on_files(files: Files, config: MkDocsConfig) -> Files:
    """Add the root pages to the site, docs/README.md as documentation.md."""
    from mkdocs.structure.files import File

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
    return files


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
            url = posixpath.relpath(base or ".", site_dir) + "/"
            return f'{name}="{url}{anchor}"'
        return f'{name}="{posixpath.relpath(site, site_dir)}{slash}{anchor}"'

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
