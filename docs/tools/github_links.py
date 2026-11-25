# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""MkDocs hook: a link that leaves the site points at GitHub instead.

A link to a source file or another page outside the site (RUNBOOK.md) goes to GitHub, so the
same Markdown reads right on github.com and on the docs site.
"""
from __future__ import annotations

import posixpath
import re
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from mkdocs.config.defaults import MkDocsConfig
    from mkdocs.structure.files import Files
    from mkdocs.structure.pages import Page

BLOB = "https://github.com/stein155/soundtap/blob/main/"
LINK = re.compile(r"\]\((?!https?:|mailto:|#|/)([^)\s]+)\)")
OFF_SITE = ()


def on_page_markdown(markdown: str, page: Page, config: MkDocsConfig, files: Files) -> str:
    """Point every relative link in `markdown` that is not a file on the site at GitHub."""
    del config  # MkDocs passes every argument by keyword; this hook needs no configuration.
    here = posixpath.dirname(page.file.src_uri)

    def fix(m: re.Match[str]) -> str:
        target, _, anchor = m.group(1).partition("#")
        path = posixpath.normpath(posixpath.join("docs", here, target))
        inside = path.startswith("docs/") and not path[5:].startswith(OFF_SITE)
        if inside and files.get_file_from_path(path[5:]):
            return m.group(0)
        return "](" + BLOB + path + ("#" + anchor if anchor else "") + ")"

    return LINK.sub(fix, markdown)
