# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Every code reference in an ADR resolves to code in this tree.

An ADR cites the code that implements it (docs/decisions/README.md). This test checks the
references that can be checked cheaply and without false alarms. The rules, applied to the
prose of every docs/decisions/NNNN-*.md (fenced code blocks are skipped):

1. Path in backticks. `app/...`, `os/...`, `bco/...`, `tools/...` or `.github/...` names a
   tracked file, or a directory that holds one. Trailing `/`, `.`, `,` and `:` are ignored;
   a path holding a placeholder or glob character (`*`, `<`, `>`, `{`, `$`, `[`, `...`) is
   skipped. In a command line (`tools/x.sh --flag`) only the first word counts.
   `path/file.go:Symbol` is rule 3.
2. Link target. A relative link whose target, resolved from docs/decisions/, lies under one
   of those five roots names a tracked file or directory (a `#...` anchor is ignored).
   Links to other docs pages are test_doc_links.py's job.
3. `path/file.go:Symbol` in backticks. The file exists and declares Symbol at top level
   (func, type, var or const, also inside a grouped block) or as a method name.
   `path/file.go:Type.Member` checks Type, and that Member is a method of Type or a word in
   that file (a field).
4. Linked symbol. A link whose whole text is one backticked identifier, `Name` or
   `Name.member`, optionally followed by `()`, and whose target is a `.go` file: that file
   declares Name as rule 3 says, and member is a method of Name or a word in the file. When
   the target is a directory, the same holds for the Go package in that directory. A text
   `pkg.Name` whose pkg is the target's own package checks Name.
5. Package-qualified identifier in backticks: `pkg.Name`, `pkg.Name.Member`, optionally with
   a call `(...)` after it, where pkg is the package clause of non-test Go code in this
   module (outside app/vendor) and Name is exported (starts with an upper-case letter). Some
   directory of that package declares Name at top level; Member is a method of Name or a word
   in that package. A pkg that is not one of ours (`http`, `slog`, `os`) is not checked, nor
   is a lower-case Name (often a JSON field such as `audio.volume`).

Not checked: identifiers without a package or a link, struct fields beyond "the word
appears", and bare file names such as `bootconfirm.go`.

ADR bodies that predate this test may still hold broken references. Those exact failures are
listed in adr_code_refs_allowlist.txt, which may only shrink: an entry that resolves again
must be removed, and the list may not grow past ALLOWLIST_CEILING. A new reference resolves.
"""
import posixpath
import re
import subprocess
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
ADRS = "docs/decisions"
ALLOWLIST = Path(__file__).with_name("adr_code_refs_allowlist.txt")
# The number of entries when the list was introduced. Lower it as entries go; never raise it.
ALLOWLIST_CEILING = 0

CODE_ROOTS = ("app/", "os/", "bco/", "tools/", ".github/")
SKIP_CHARS = "*<>{$[…"
TICK = re.compile(r"`([^`\n]+)`")
LINK = re.compile(r"\[([^\]\n]*)\]\(([^)\s]+)\)")
IDENT = r"[A-Za-z_]\w*"
GO_FILE_SYMBOL = re.compile(rf"^(\S+\.go):({IDENT})(?:\.({IDENT}))?(?:\(.*\))?$")
LINKED_SYMBOL = re.compile(rf"^`({IDENT})(?:\.({IDENT}))?(?:\(\))?`$")
PKG_SYMBOL = re.compile(rf"^([a-z][a-z0-9_]*)\.([A-Z]\w*)(?:\.({IDENT}))?(?:\(.*\))?$")

FUNC = re.compile(rf"^func\s+({IDENT})")
METHOD = re.compile(rf"^func\s+\(\s*(?:{IDENT}\s+)?\*?\s*({IDENT})(?:\[[^\]]*\])?\s*\)\s*({IDENT})")
SINGLE = re.compile(rf"^(?:type|var|const)\s+({IDENT}(?:\s*,\s*{IDENT})*)")
GROUP_OPEN = re.compile(r"^(?:type|var|const)\s*\(\s*$")
GROUP_MEMBER = re.compile(rf"^\t({IDENT}(?:\s*,\s*{IDENT})*)\b")
PACKAGE = re.compile(rf"^package\s+({IDENT})", re.M)


def prose_lines(text):
    """Lines outside fenced code blocks, numbered from 1."""
    fence = None
    for n, line in enumerate(text.splitlines(), 1):
        marker = re.match(r"^ {0,3}(`{3,}|~{3,})(.*)$", line)
        if marker:
            run, tail = marker.groups()
            if fence is None:
                fence = run
            elif run[0] == fence[0] and len(run) >= len(fence) and not tail.strip():
                fence = None
            continue
        if fence is None:
            yield n, line


class Decls:
    """Top-level names and methods declared in some Go source, plus every word in it."""

    def __init__(self, sources=()):
        self.names, self.methods, self.words = set(), set(), set()
        for text in sources:
            self.add(text)

    def add(self, text):
        group = False
        for line in text.splitlines():
            if group:
                if line.startswith(")"):
                    group = False
                elif m := GROUP_MEMBER.match(line):
                    self.names.update(re.split(r"\s*,\s*", m.group(1)))
                continue
            if GROUP_OPEN.match(line):
                group = True
            elif m := METHOD.match(line):
                self.methods.add((m.group(1), m.group(2)))
            elif m := FUNC.match(line):
                self.names.add(m.group(1))
            elif m := SINGLE.match(line):
                self.names.update(re.split(r"\s*,\s*", m.group(1)))
        self.words.update(re.findall(r"\b\w+\b", text))

    def has(self, name, member=None):
        """name is declared (or is a method name); member, if given, belongs to it."""
        if name not in self.names and name not in {m for _, m in self.methods}:
            return False
        return member is None or (name, member) in self.methods or member in self.words


class Tree:
    """The tracked files of a checkout and its Go packages, read on demand."""

    def __init__(self, root, files):
        self.root = root
        self.files = set(files)
        self.known = self.files | {str(p) for f in self.files for p in Path(f).parents}
        self._decls = {}
        self.packages = {}
        for name in sorted(self.files):
            if (name.endswith(".go") and not name.endswith("_test.go")
                    and not name.startswith("app/vendor/") and "/testdata/" not in name):
                m = PACKAGE.search(self.read(name))
                if m:
                    self.packages.setdefault(m.group(1), set()).add(posixpath.dirname(name))

    def read(self, name):
        return (self.root / name).read_text(errors="replace")

    def decls(self, *names):
        key = tuple(sorted(names))
        if key not in self._decls:
            self._decls[key] = Decls(self.read(n) for n in key)
        return self._decls[key]

    def package_decls(self, directory):
        return self.decls(*(f for f in self.files if posixpath.dirname(f) == directory
                            and f.endswith(".go") and not f.endswith("_test.go")))


def tick_error(tree, ref):
    """What is wrong with one backticked reference (rules 1, 3 and 5), or None."""
    if ref.startswith(CODE_ROOTS):
        ref = ref.split()[0]  # A command line: the script is the reference.
        if any(c in ref for c in SKIP_CHARS) or "..." in ref:
            return None
        sym = GO_FILE_SYMBOL.match(ref)
        path = sym.group(1) if sym else ref.rstrip("/.,:")
        if path not in tree.known:
            return f"missing path {path}"
        if sym and not tree.decls(path).has(sym.group(2), sym.group(3)):
            return f"missing symbol {ref}"
        return None
    sym = PKG_SYMBOL.match(ref)
    if sym and sym.group(1) in tree.packages:
        pkg, ident, member = sym.groups()
        if not any(tree.package_decls(d).has(ident, member) for d in tree.packages[pkg]):
            return f"missing symbol {ref}"
    return None


def link_error(tree, name, label, target):
    """What is wrong with one link from the ADR at name (rules 2 and 4), or None."""
    if re.match(r"^[a-z][a-z0-9+.-]*:", target) or target.startswith(("#", "/")):
        return None
    path = posixpath.normpath(posixpath.join(posixpath.dirname(name), target.partition("#")[0]))
    if not path.startswith(CODE_ROOTS):
        return None
    if path not in tree.known:
        return f"missing path {path}"
    label = label.strip()
    sym = LINKED_SYMBOL.match(label)
    if not sym or re.search(r"\.\w+$", label.strip("`()")):
        return None  # Not a symbol, or a file name such as `bootconfirm.go`.
    if path in tree.files and not path.endswith(".go"):
        return None  # A symbol linked to a shell script, C file or page is not checked.
    ident, member = sym.groups()
    directory = posixpath.dirname(path) if path in tree.files else path
    if member and directory in tree.packages.get(ident, ()):
        ident, member = member, None  # [`release.Verify`](.../release/verify.go)
    decls = tree.decls(path) if path in tree.files else tree.package_decls(path)
    return None if decls.has(ident, member) else f"missing symbol {label} in {path}"


def reference_errors(tree, name, text):
    """Unresolved references in one ADR, as 'file: message' without line numbers."""
    errors = []
    for _, line in prose_lines(text):
        found = [tick_error(tree, m.group(1).strip()) for m in TICK.finditer(line)]
        found += [link_error(tree, name, *m.groups()) for m in LINK.finditer(line)]
        for message in filter(None, found):
            entry = f"{name}: {message}"
            if entry not in errors:
                errors.append(entry)
    return errors


def read_allowlist(path):
    if not path.exists():
        return []
    return [line.strip() for line in path.read_text().splitlines()
            if line.strip() and not line.lstrip().startswith("#")]


def tracked(root):
    return subprocess.run(["git", "ls-files"], cwd=root, check=True, capture_output=True,
                          text=True).stdout.split()


def all_errors(root):
    tree = Tree(root, tracked(root))
    errors = []
    for adr in sorted((root / ADRS).glob("[0-9][0-9][0-9][0-9]-*.md")):
        name = f"{ADRS}/{adr.name}"
        errors += reference_errors(tree, name, adr.read_text(errors="replace"))
    return errors


class AdrCodeRefsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.errors = all_errors(ROOT)
        cls.allowed = read_allowlist(ALLOWLIST)

    def test_every_reference_not_on_the_allow_list_resolves(self):
        self.assertEqual([e for e in self.errors if e not in self.allowed], [])

    def test_allow_list_only_shrinks(self):
        stale = [e for e in self.allowed if e not in self.errors]
        self.assertEqual(stale, [], "these references resolve now: remove them from the allow-list")
        self.assertEqual(len(self.allowed), len(set(self.allowed)), "duplicate allow-list entries")
        self.assertLessEqual(len(self.allowed), ALLOWLIST_CEILING,
                             "the allow-list may not grow: fix the new reference instead")


class AdrCodeRefRulesTest(unittest.TestCase):
    GO = '''package release

import "errors"

type PublicKey [32]byte

type (
\tManager struct{ Repo string }
\tlevel int
)

const (
\tAPILevel, minLevel = 3, 1
)

var ErrClockNotSet = errors.New("clock")

func Verify() error { return nil }

func (m *Manager) Check() {}

func (k PublicKey[T]) String() string { return "" }
'''

    def tree(self, files):
        import tempfile
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        root = Path(tmp.name)
        for name, text in files.items():
            (root / name).parent.mkdir(parents=True, exist_ok=True)
            (root / name).write_text(text)
        return Tree(root, files)

    def test_declarations(self):
        d = Decls([self.GO])
        for name in ("PublicKey", "Manager", "level", "APILevel", "minLevel", "ErrClockNotSet",
                     "Verify", "Check", "String"):
            with self.subTest(name=name):
                self.assertTrue(d.has(name))
        self.assertTrue(d.has("Manager", "Check"))
        self.assertTrue(d.has("Manager", "Repo"))
        self.assertTrue(d.has("PublicKey", "String"))
        self.assertFalse(d.has("Missing"))
        self.assertFalse(d.has("Manager", "Gone"))
        self.assertFalse(d.has("errors"))

    def test_rules(self):
        tree = self.tree({"app/internal/release/release.go": self.GO,
                          "app/internal/release/release_test.go": "package release\nfunc Hidden() {}\n",
                          "os/board/init.sh": "#!/bin/sh\n"})
        adr = f"{ADRS}/0001-x.md"
        text = """# 0001. X
Fine: `app/internal/release/release.go`, `app/internal/release/`, `os/board/init.sh --now`,
`app/internal/release/release.go:Verify`, `app/internal/release/release.go:Manager.Check`,
`release.Verify`, `release.PublicKey`, `release.Manager.Repo`, `release.Verify(ctx, raw)`,
`http.Server`, `release.unexported`, `audio.volume`, `app/internal/*/x.go`, `os/<model>/x`,
[`Verify`](../../app/internal/release/release.go),
[`Manager.Check`](../../app/internal/release/release.go#L3),
[`release.go`](../../app/internal/release/release.go),
[`Verify()`](../../app/internal/release/), [`thing`](../../os/board/init.sh),
[`release.Verify`](../../app/internal/release/release.go),
[design](../DESIGN.md), [web](https://example.org/app/gone.go).
```sh
`app/gone.go` in a fence
```
Broken: `app/gone.go`, `app/internal/release/release.go:Gone`, `release.Gone`,
`release.Hidden`, `release.Manager.Nope`, [`Gone`](../../app/internal/release/release.go),
[`Verify`](../../app/internal/old/release.go), `app/gone.go`.
"""
        self.assertEqual(reference_errors(tree, adr, text), [
            f"{adr}: missing path app/gone.go",
            f"{adr}: missing symbol app/internal/release/release.go:Gone",
            f"{adr}: missing symbol release.Gone",
            f"{adr}: missing symbol release.Hidden",
            f"{adr}: missing symbol release.Manager.Nope",
            f"{adr}: missing symbol `Gone` in app/internal/release/release.go",
            f"{adr}: missing path app/internal/old/release.go",
        ])

    def test_allowlist_ignores_comments_and_blank_lines(self):
        import tempfile
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "allow.txt"
            path.write_text("# header\n\n0001-x.md: missing path app/gone.go\n")
            self.assertEqual(read_allowlist(path), ["0001-x.md: missing path app/gone.go"])


if __name__ == "__main__":
    unittest.main()
