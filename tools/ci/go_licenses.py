#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Check the licence of every Go module soundtapd builds with (ADR 0124).

`go list -m -json all` in app/ names the modules, and `go list -deps ./...` says which of
them provide a package the module builds: the rest of the module graph (a dependency's own
test dependencies) is never compiled in and is not checked. Each built module's licence is
read from the LICENSE or COPYING file at the root of its module-cache directory. A module
passes when NOTICE.md's Go module table has a row for it, that row names the licence its
file shows, and every licence in the row is compatible with GPL-3.0-only. A row entry may be
an SPDX choice, "A OR B", for a module its authors offer under either licence; it is
compatible when one of the alternatives is. A row for a module that is no longer in the
module graph fails too. With no modules the check passes.

The module cache must hold the modules: `go mod download` in app/ fetches them
(`make lint-go-licenses` does). The check itself never touches the network.
"""

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

# SPDX identifiers whose code may be combined into a GPL-3.0-only program. GPL-2.0-only is
# the notable one that may not.
ALLOWED = {
    "0BSD", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "BSL-1.0", "CC0-1.0", "ISC", "MIT",
    "MPL-2.0", "Unlicense", "Zlib",
    "LGPL-2.1-only", "LGPL-2.1-or-later", "LGPL-3.0-only", "LGPL-3.0-or-later",
    "GPL-2.0-or-later", "GPL-3.0-only", "GPL-3.0-or-later",
}

# A licence text says which licence it is, not whether "or later" applies: that is in the
# module's own notices, so the GPL family texts name only the family and NOTICE.md's row
# says -only or -or-later. Phrases are matched with whitespace folded, and the first match
# wins: the GPL-3.0 text mentions the Lesser GPL, so each family is told by its title line.
TEXTS = [
    # Eclipse projects (Paho) offer their code under the EPL-2.0 or the Eclipse Distribution
    # License 1.0, which is the BSD-3-Clause text; the LICENSE file states the choice.
    ("EPL-2.0 OR BSD-3-Clause", ["made available under the terms of the eclipse public license v2.0 "
                                 "and eclipse distribution license v1.0"]),
    ("LGPL-3.0", ["gnu lesser general public license version 3, 29 june 2007"]),
    ("LGPL-2.1", ["gnu lesser general public license version 2.1, february 1999"]),
    ("GPL-3.0", ["gnu general public license version 3, 29 june 2007"]),
    ("GPL-2.0", ["gnu general public license version 2, june 1991"]),
    ("Apache-2.0", ["apache license version 2.0, january 2004"]),
    ("MPL-2.0", ["mozilla public license version 2.0"]),
    ("BSL-1.0", ["boost software license - version 1.0"]),
    ("CC0-1.0", ["cc0 1.0 universal"]),
    ("Unlicense", ["this is free and unencumbered software released into the public domain"]),
    ("Zlib", ["altered source versions must be plainly marked as such"]),
    ("ISC", ["permission to use, copy, modify, and/or distribute this software for any purpose",
             "provided that the above copyright notice and this permission notice appear"]),
    ("0BSD", ["permission to use, copy, modify, and/or distribute this software for any purpose"]),
    ("MIT", ["permission is hereby granted, free of charge, to any person obtaining a copy"]),
    ("BSD-3-Clause", ["redistribution and use in source and binary forms",
                      "may be used to endorse or promote products derived from this software"]),
    ("BSD-2-Clause", ["redistribution and use in source and binary forms"]),
]

LICENCE_FILE = re.compile(r"^(licen[cs]e|copying|unlicense)([.-].*)?$", re.IGNORECASE)
NOTICE_HEADER = "| Go module | Licence |"


def parse_go_list(text: str) -> list[dict]:
    """`go list -m -json` prints one JSON object after another, not an array."""
    decoder = json.JSONDecoder()
    modules, pos = [], 0
    while True:
        while pos < len(text) and text[pos].isspace():
            pos += 1
        if pos == len(text):
            return modules
        module, pos = decoder.raw_decode(text, pos)
        modules.append(module)


def go(module_dir: Path, *args: str) -> str:
    env = dict(os.environ, GOWORK="off", GOFLAGS="-mod=readonly", GOPROXY="off")
    result = subprocess.run(["go", *args], cwd=module_dir, env=env, capture_output=True, text=True)
    if result.returncode != 0:
        raise SystemExit(f"go {' '.join(args)} failed in {module_dir}:\n{result.stderr}")
    return result.stdout


def built_modules(module_dir: Path) -> set[str]:
    """Modules that provide a package the main module's packages import, tests excluded.

    -e keeps going past a package that does not load (the embedded web UI is absent until
    it is built); its imports are still listed."""
    listing = go(module_dir, "list", "-e", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", "./...")
    return {line for line in listing.splitlines() if line}


def allowed(entry: str) -> bool:
    """An SPDX id, or a choice "A OR B" of which one alternative is enough."""
    return any(alternative.strip() in ALLOWED for alternative in entry.split(" OR "))


def classify(text: str) -> str | None:
    words = " ".join(text.lower().split())
    for spdx, phrases in TEXTS:
        if all(phrase in words for phrase in phrases):
            return spdx
    return None


def module_licences(directory: Path) -> tuple[set[str], list[str]]:
    """The licences the files at the module root show, and the files no rule recognises."""
    found, unknown = set(), []
    for path in sorted(directory.iterdir()):
        if path.is_file() and LICENCE_FILE.match(path.name):
            spdx = classify(path.read_text(errors="replace"))
            if spdx:
                found.add(spdx)
            else:
                unknown.append(path.name)
    return found, unknown


def notice_modules(notice: str) -> dict[str, list[str]]:
    lines = notice.splitlines()
    try:
        start = lines.index(NOTICE_HEADER)
    except ValueError:
        raise SystemExit(f"NOTICE.md has no Go module table ({NOTICE_HEADER})") from None
    rows = {}
    for line in lines[start + 2:]:
        if not line.startswith("|"):
            break
        cells = [cell.strip().strip("`") for cell in line.strip("|").split("|")]
        rows[cells[0]] = [spdx.strip() for spdx in cells[1].split(",") if spdx.strip()]
    return rows


def matches(listed: str, found: str) -> bool:
    """A row's GPL-family entry carries -only or -or-later; the text shows only the family."""
    return listed in (found, f"{found}-only", f"{found}-or-later")


def problems(modules: list[dict], notice: dict[str, list[str]],
             built: set[str] | None = None) -> list[str]:
    """built: the modules compiled in; None checks every module given.

    Every built module needs a row. A module only in the graph (a test dependency) may have
    one; a row for a module outside the graph is stale."""
    errors = []
    in_graph = {module["Path"] for module in modules if not module.get("Main")}
    for module in modules:
        if module.get("Main") or (built is not None and module["Path"] not in built):
            continue
        path, version = module["Path"], module.get("Version", "")
        name = f"{path} {version}".strip()
        source = module.get("Replace") or module
        directory = source.get("Dir")
        if not directory or not Path(directory).is_dir():
            errors.append(f"{name} is not in the module cache: run `go mod download` in app/")
            continue
        found, unknown = module_licences(Path(directory))
        listed = notice.get(path)
        if unknown:
            errors.append(f"{name}: cannot tell the licence of {', '.join(unknown)}")
        if not found and not unknown:
            errors.append(f"{name} has no LICENSE or COPYING file")
        if listed is None:
            errors.append(f"{name} has no row in NOTICE.md's Go module table "
                          f"(its licence file says {', '.join(sorted(found)) or 'nothing known'})")
            continue
        for spdx in listed:
            if not allowed(spdx):
                errors.append(f"{name}: {spdx} is not compatible with GPL-3.0-only (ADR 0124)")
        for spdx in sorted(found):
            if not any(matches(entry, spdx) for entry in listed):
                errors.append(f"{name}: NOTICE.md says {', '.join(listed)}, its licence file {spdx}")
        for entry in listed:
            if found and not any(matches(entry, spdx) for spdx in found):
                errors.append(f"{name}: NOTICE.md lists {entry}, which no licence file shows")
    for path in sorted(set(notice) - in_graph):
        errors.append(f"NOTICE.md lists {path}, which app/go.mod does not use")
    return errors


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--module-dir", type=Path, default=Path("app"))
    parser.add_argument("--notice", type=Path, default=Path("NOTICE.md"))
    parser.add_argument("--go-list-json", type=Path,
                        help="check every module in this `go list -m -json all` output instead of running go")
    args = parser.parse_args()

    if args.go_list_json:
        modules, built = parse_go_list(args.go_list_json.read_text()), None
    else:
        modules = parse_go_list(go(args.module_dir, "list", "-m", "-json", "all"))
        built = built_modules(args.module_dir)
    errors = problems(modules, notice_modules(args.notice.read_text()), built)
    if errors:
        print("\n".join(errors), file=sys.stderr)
        raise SystemExit(1)
    count = sum(1 for module in modules
                if not module.get("Main") and (built is None or module["Path"] in built))
    print(f"Go module licences: {count} module(s), all allowed and listed in NOTICE.md")


if __name__ == "__main__":
    main()
