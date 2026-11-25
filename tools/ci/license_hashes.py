#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Fail when a local package's .hash file no longer matches its license files.

Buildroot only checks these hashes in `make legal-info`, which CI does not run, so a
changed LICENSE or a regenerated THIRD_PARTY_LICENSES.txt went unnoticed.
Only packages whose source lives in this repository are checked.
"""

import hashlib
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
EXTERNAL = ROOT / "os"
# A .hash line: "sha256 <digest> <file>".
HASH_LINE_FIELDS = 3


def var(mk: str, name: str) -> str:
    """Return the value the .mk text assigns to its <PKG>_name variable, or ""."""
    m = re.search(rf"^\w+_{name}\s*=\s*(.+)$", mk, re.MULTILINE)
    return m.group(1).strip() if m else ""


def main() -> int:
    """Print each local package licence file whose hash is out of date; return 1 if there is one."""
    bad = []
    for mk_path in sorted(EXTERNAL.glob("package/*/*.mk")):
        mk = mk_path.read_text().replace("\\\n", " ")  # join continued lines
        site = var(mk, "SITE")
        if not site.startswith("$(BR2_EXTERNAL_SOUNDTAP_PATH)"):
            continue
        hash_path = mk_path.with_suffix(".hash")
        if not hash_path.exists():
            continue
        src = (EXTERNAL / site.removeprefix("$(BR2_EXTERNAL_SOUNDTAP_PATH)").lstrip("/")).resolve()
        known = {}
        for line in hash_path.read_text().splitlines():
            parts = line.split()
            if len(parts) == HASH_LINE_FIELDS and parts[0] == "sha256":
                known[parts[2]] = parts[1]
        for name in var(mk, "LICENSE_FILES").split():
            if not (src / name).exists():
                continue  # copied in at build time; its own hook checks the hash
            got = hashlib.sha256((src / name).read_bytes()).hexdigest()
            if known.get(name) != got:
                bad.append(f"{hash_path.relative_to(ROOT)}: sha256  {got}  {name}")
    for line in bad:
        print(line, file=sys.stderr)
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
