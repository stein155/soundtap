#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every container image the repository pulls is pinned by digest (CONTRIBUTING.md, ADR 0011).

    image_pins.py [repo root]

Looks at the Makefile, the shell scripts, the workflows and the Dockerfiles: each `*_IMAGE`
assignment, each image a `docker run` starts, each workflow `image:`/`container:`/`docker://`
and each `FROM`. An image passes when it names `@sha256:<digest>`, or when it is one this
repository builds itself (LOCAL, from a Dockerfile whose FROM this check also holds to a
digest). A tag alone fails: a moved tag would change what a build or a test runs.
"""

import os
import re
import sys
from collections.abc import Iterator
from pathlib import Path

DIGEST = re.compile(r"@sha256:[0-9a-f]{64}\b")

# Images built here rather than pulled, with where they come from.
LOCAL = {
    "soundtap-builder": "make builder, from os/container/Dockerfile.builder",
    "soundtap-bco-toolchain": "built by make test-bco from bco/app/Dockerfile",
    "soundtap-rom-usb-loader": "built by tools/recovery/rom-usb-loader/build.sh from Dockerfile.loader",
    "soundtap-wla-uboot": "built by os/bootloader/build-wla-uboot.sh from Dockerfile.wla-uboot",
    "wla-armhf-gcc": "built by os/installer/module/build.sh from its toolchain/Dockerfile",
    "soundtap-nandsim-guest": "committed by nandsim-install-test.sh from its pinned RUNNER_IMAGE",
    "soundtap-rootfs-run": "committed by rootfs-run-test.sh from its pinned RUNNER_IMAGE",
}
# Image words that are not a variable: build.yml reads BUILDER_IMAGE out of the Makefile.
SUBSTITUTIONS = {"$(sed -n 's/^BUILDER_IMAGE := //p' Makefile)": ("Makefile", "BUILDER_IMAGE")}

SKIP_DIRS = {".git", "buildroot", ".cache", "dl", "output", "artifacts", "dist", "node_modules"}
# Hardware evidence is kept as captured; it runs nothing. Vendored Go modules keep their
# upstream bytes (ADR 0124): their maintainer scripts are never run here.
SKIP_PATHS = {"os/tests/fixtures", "app/vendor"}

# docker run options that take a value as the next word, and those that do not.
VALUE_OPTIONS = {
    "-v", "--volume", "-w", "--workdir", "-e", "--env", "--env-file", "-u", "--user", "--name",
    "--entrypoint", "--net", "--network", "--device", "--cap-add", "--cap-drop", "--mount",
    "-p", "--publish", "--platform", "--tmpfs", "--security-opt", "-h", "--hostname", "-l",
    "--label", "--add-host", "--ulimit", "-m", "--memory", "--cpus", "--pull", "--shm-size",
    "--ipc", "--pid", "--userns", "--group-add", "--restart", "--cidfile",
}
FLAG_OPTIONS = {
    "--rm", "-i", "-t", "-it", "-ti", "-d", "--detach", "--init", "--privileged",
    "--interactive", "--tty", "--read-only",
}

RUN = re.compile(r"(?:\bdocker|\bpodman|\$\(DOCKER\)|\$\{?DOCKER\}?)\s+(?:run|create)\b")
WORD = re.compile(r"""(?:"[^"]*"|'[^']*'|[^\s"'])+""")
MAKE_ASSIGN = re.compile(r"^(\w+)\s*[:?]?=\s*(.*?)\s*$", re.MULTILINE)
SHELL_ASSIGN = re.compile(r"""^\s*(?:export\s+|local\s+)?(\w+)=((?:"[^"]*"|'[^']*'|[^\s"'])*)""", re.MULTILINE)
VAR_WORD = re.compile(r"^\$(?:\{(\w+)\}|\((\w+)\)|(\w+))$")
# The shortest quoted word: the two quotes.
QUOTED_MIN_LENGTH = 2

# What a scan finds: the line, the image (None for a problem without one) and the problem.
Found = tuple[int, str | None, str | None]


def unquote(word: str) -> str:
    """Return word without the quotes around it."""
    if len(word) >= QUOTED_MIN_LENGTH and word[0] == word[-1] and word[0] in "\"'":
        return word[1:-1]
    return word


def shell_value(raw: str) -> str:
    """Return the value a shell assignment gives, the default of a ${OVERRIDE:-default}."""
    value = unquote(raw)
    if m := re.fullmatch(r"\$\{\w+:?-(.*)\}", value):  # ${OVERRIDE:-default}
        return m[1]
    return value


def assignments(path: Path, text: str) -> dict[str, list[str]]:
    """Return the values text assigns to each variable, read as make or shell by path's name."""
    found: dict[str, list[str]] = {}
    if path.name == "Makefile" or path.suffix == ".mk":
        for name, value in MAKE_ASSIGN.findall(text):
            found.setdefault(name, []).append(value)
    else:
        for name, value in SHELL_ASSIGN.findall(text):
            found.setdefault(name, []).append(shell_value(value))
    return found


def pinned_or_local(image: str) -> bool:
    """Return whether image is pinned by digest or built in this repository."""
    if DIGEST.search(image):
        return True
    return image.split("@")[0].split(":")[0] in LOCAL


def in_comment(text: str, pos: int) -> bool:
    """Return whether pos in text lies in a comment."""
    line = text[text.rfind("\n", 0, pos) + 1:pos]
    return re.search(r"(^|\s|@)#", line) is not None


def run_images(text: str, assigned: dict[str, list[str]], root: Path) -> Iterator[Found]:
    """Yield (line, image or None, problem) for each docker run in text."""
    flat = text.replace("\\\n", "  ")  # continuation lines; offsets stay those of text
    for m in RUN.finditer(flat):
        if in_comment(flat, m.start()):
            continue
        line = text.count("\n", 0, m.start()) + 1
        words = WORD.finditer(flat, m.end())
        image = None
        for w in words:
            word = w.group(0)
            if word.startswith("--") and "=" in word:
                continue
            if word in FLAG_OPTIONS:
                continue
            if word in VALUE_OPTIONS:
                next(words, None)
                continue
            if word.startswith("-"):
                yield line, None, f"unknown docker run option {word}; teach image_pins.py its arity"
                break
            v = VAR_WORD.match(word)
            if v and all(x == "" or x.startswith("-") for x in assigned.get(v[1] or v[2] or v[3], ["?"])):
                continue  # an unquoted variable that holds options ($KVM), or nothing
            image = unquote(word)
            break
        if image is None:
            continue
        yield from resolve(line, image, assigned, root)


def resolve(line: int, image: str, assigned: dict[str, list[str]], root: Path) -> Iterator[Found]:
    """Yield the image or images a docker run's image word stands for, or a problem."""
    if image in SUBSTITUTIONS:
        source, name = SUBSTITUTIONS[image]
        text = (root / source).read_text()
        values = assignments(root / source, text).get(name)
    elif v := VAR_WORD.match(image):
        name = v[1] or v[2] or v[3]
        values = assigned.get(name)
    else:
        name, values = None, [image]
    if not values:
        yield line, image, f"cannot resolve the image {image}"
        return
    for value in values:
        yield line, value, None


def scanned(root: Path) -> Iterator[Path]:
    """Yield the files under root that can name an image: Makefiles, shell scripts, workflows and Dockerfiles."""
    for top, dirs, names in os.walk(root):
        rel = Path(top).relative_to(root)
        # A directory with its own .git is another checkout (a worktree or clone kept inside
        # this one), not part of this tree.
        dirs[:] = sorted(d for d in dirs if d not in SKIP_DIRS and str(rel / d) not in SKIP_PATHS
                         and not Path(top, d, ".git").exists())
        for name in sorted(names):
            path = Path(top, name)
            workflow = rel.parts[:2] == (".github", "workflows") and name.endswith((".yml", ".yaml"))
            if name == "Makefile" or name.endswith(".sh") or "Dockerfile" in name or workflow:
                yield path
            elif "." not in name and path.is_file() and not path.is_symlink():
                try:
                    with path.open("rb") as f:
                        first = f.readline(200)
                except OSError:
                    continue
                if re.match(rb"#!.*[/ ](ba|a|da)?sh( |$)", first):
                    yield path


def dockerfile_images(text: str) -> list[Found]:
    """Return the image each FROM of a Dockerfile pulls, leaving out scratch and earlier stages."""
    found: list[Found] = []
    stages = set()
    for n, line in enumerate(text.splitlines(), 1):
        if m := re.match(r"\s*FROM\s+(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+(\S+))?", line, re.IGNORECASE):
            if m[1] != "scratch" and m[1] not in stages:
                found.append((n, m[1], None))
            if m[2]:
                stages.add(m[2])
    return found


def script_images(path: Path, text: str, root: Path) -> list[Found]:
    """Return the images a Makefile, script or workflow names: docker runs, *_IMAGE assignments and workflow images."""
    found: list[Found] = []
    assigned = assignments(path, text)
    found.extend(run_images(text, assigned, root))
    pattern = MAKE_ASSIGN if path.name == "Makefile" else SHELL_ASSIGN
    for m in pattern.finditer(text):
        if re.search(r"(?i)image$", m[1]):
            value = m[2] if path.name == "Makefile" else shell_value(m[2])
            found.append((text.count("\n", 0, m.start()) + 1, value, None))
    if path.suffix in (".yml", ".yaml"):
        found.extend((text.count("\n", 0, m.start()) + 1, unquote(m[1] or m[2]), None)
                     for m in re.finditer(r"^\s*(?:-\s*)?(?:image|container):\s*(\S+)|uses:\s*docker://(\S+)", text, re.MULTILINE))
    return found


def problems(root: Path) -> list[str]:
    """Return a line for each image under root that is neither pinned by digest nor built here."""
    out = []
    for path in scanned(root):
        text = path.read_text(errors="replace")
        rel = path.relative_to(root)
        found = dockerfile_images(text) if "Dockerfile" in path.name else script_images(path, text, root)
        for line, image, problem in found:
            if problem:
                out.append(f"{rel}:{line}: {problem}")
            elif not pinned_or_local(image):
                out.append(f"{rel}:{line}: {image} is not pinned by @sha256")
    return out


def main(argv: list[str]) -> int:
    """Print every unpinned image under the repository root in argv[1]; return 1 if there is one."""
    root = Path(argv[1] if len(argv) > 1 else Path(__file__).resolve().parents[2])
    found = problems(root)
    for p in found:
        print(p, file=sys.stderr)
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
