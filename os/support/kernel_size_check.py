#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""The kernel's size budget: a hard limit, and a warning well before it.

Every model boots one kernel, and its uImage must fit 4.75 MiB (linux.fragment says which
options were cut to make room). Each feature that goes into the kernel costs every model
room, so a build reports how much is left and warns once less than WARN_BELOW remains:
the warning comes while there is still time to cut options, not on the build that fails.

    kernel_size_check.py output/os/images/uImage-st10 output/os/images/uImage-st20

Exit 1 when a kernel is over the limit. Under GitHub Actions the warning is also an
annotation, and the sizes go to the job summary.
"""

import argparse
import os
import sys
from pathlib import Path

LIMIT = 4_980_736  # 4.75 MiB
WARN_BELOW = 128 * 1024  # headroom left before the build warns


def report(path: Path) -> tuple[str, str]:
    """Return the level ("ok", "warn" or "fail") and one line about the kernel at path."""
    size = path.stat().st_size
    left = LIMIT - size
    used = f"{size:,} bytes, {size * 100 / LIMIT:.1f}% of the 4.75 MiB limit"
    if left < 0:
        return "fail", f"{path.name}: {used}; {-left:,} bytes over"
    if left < WARN_BELOW:
        return "warn", (f"{path.name}: {used}; only {left:,} bytes left, less than "
                        f"{WARN_BELOW // 1024} KiB: cut kernel options before adding more")
    return "ok", f"{path.name}: {used}; {left:,} bytes left"


def main(argv: list[str] | None = None) -> int:
    """Check each kernel against the budget."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("kernels", nargs="+", type=Path)
    args = parser.parse_args(argv)
    actions = os.environ.get("GITHUB_ACTIONS") == "true"
    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    lines = []
    failed = False
    for path in args.kernels:
        level, line = report(path)
        lines.append(line)
        if level == "ok":
            sys.stdout.write(line + "\n")
            continue
        failed |= level == "fail"
        word = "error" if level == "fail" else "warning"
        sys.stderr.write((f"::{word} title=Kernel size::{line}" if actions else f"{word}: {line}") + "\n")
    if actions and summary:
        with Path(summary).open("a", encoding="utf-8") as out:
            out.write("### Kernel size\n\n" + "".join(f"- {line}\n" for line in lines))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
