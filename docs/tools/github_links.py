# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""MkDocs hook: a link that leaves the site (a source file, RUNBOOK.md) points at
GitHub instead, so the same Markdown reads right on github.com and on the docs site."""
import posixpath
import re

BLOB = "https://github.com/stein155/soundtap/blob/main/"
LINK = re.compile(r"\]\((?!https?:|mailto:|#|/)([^)\s]+)\)")
OFF_SITE = ()


def on_page_markdown(markdown, page, config, files):
    here = posixpath.dirname(page.file.src_uri)

    def fix(m):
        target, _, anchor = m.group(1).partition("#")
        path = posixpath.normpath(posixpath.join("docs", here, target))
        inside = path.startswith("docs/") and not path[5:].startswith(OFF_SITE)
        if inside and files.get_file_from_path(path[5:]):
            return m.group(0)
        return "](" + BLOB + path + ("#" + anchor if anchor else "") + ")"

    return LINK.sub(fix, markdown)
