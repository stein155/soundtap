# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""Repository links and evidence bookkeeping; never a hardware approval check."""
import html
import re
import subprocess
import tempfile
import unicodedata
import unittest
from collections.abc import Iterable, Iterator
from pathlib import Path
from urllib.parse import unquote, urljoin, urlsplit

ROOT = Path(__file__).resolve().parents[3]
ADR_REF = re.compile(r"\bADRs?\s+((?:\d{4}(?:,\s*|\s+and\s+|\s*/\s*)?)+)")
REPO_PATH = re.compile(r"`((?:app|os|bco|tools|docs|firmware|\.github)/[^`\s]*)`")
LINK = re.compile(r"\]\(([^)\s]+)\)")
# A docs page named in code: a comment's "see docs/wla/buttons.md", or a relative ../docs/ path.
CODE_DOC_REF = re.compile(r"(?<![\w/.-])(?:\.\./)*(docs/[\w./-]+\.md)")
CODE_FILE = re.compile(r"\.(go|c|h|sh|py|ts|tsx|js|mk|dts|dtsi)$|(^|/)Makefile$")


def tracked(*pattern: str) -> list[str]:
    """Return our tracked files; vendored Go modules keep their upstream docs as they are."""
    return subprocess.run(["git", "ls-files", "--", *pattern, ":(exclude)app/vendor/**"], cwd=ROOT,
                          check=True, capture_output=True, text=True).stdout.split()


def prose_lines(text: str) -> Iterator[tuple[int, str]]:
    """Yield the numbered lines outside fenced examples, for headings and inline links."""
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


def anchors(text: str) -> set[str]:
    """Return the anchors of GitHub-style ATX/setext headings and explicit HTML IDs used by these docs."""
    found, counts = set(), {}
    previous = ""
    previous_line = 0
    for n, line in prose_lines(text):
        found.update(re.findall(r'<[^>]+\b(?:id|name)=[\"\']([^\"\']+)[\"\']', line))
        heading = re.match(r"^ {0,3}#{1,6}\s+(.+?)\s*#*\s*$", line)
        title = heading.group(1) if heading else None
        if not heading and n == previous_line + 1 and previous.strip() and re.fullmatch(r" {0,3}(?:=+|-+)\s*", line):
            title = previous.strip()
        if title is not None:
            title = re.sub(r"!?\[([^]]+)\]\([^)]*\)", r"\1", title)
            title = html.unescape(re.sub(r"<[^>]*>", "", title)).lower()
            slug = "".join(c for c in title if c in "-_ " or
                           unicodedata.category(c)[0] in "LNM").replace(" ", "-")
            candidate = slug
            while candidate in found:
                counts[slug] = counts.get(slug, 0) + 1
                candidate = f"{slug}-{counts[slug]}"
            found.add(candidate)
        previous, previous_line = line, n
    return found


def link_errors(path: Path) -> list[str]:
    """Return the relative links of the Markdown file `path` whose file or anchor is missing."""
    errors = []
    for n, line in prose_lines(path.read_text(errors="replace")):
        for m in LINK.finditer(line):
            target = m.group(1)
            url = urlsplit(target)
            if url.scheme or url.netloc or target.startswith(("../../issues", "../../pulls")):
                continue
            dest = path.parent / unquote(url.path) if url.path else path
            if not dest.exists():
                errors.append(f"{n}: missing file {target}")
            elif (url.fragment and dest.suffix == ".md"
                  and unquote(url.fragment) not in anchors(dest.read_text(errors="replace"))):
                errors.append(f"{n}: missing anchor {target}")
    return errors


SITE = "https://stein155.github.io/soundtap/"
SITE_LINK = re.compile(r"""(?:href|data-href)=["']([^"']+)["']|(https://stein155\.github\.io/soundtap/[^\s'"<>)]*)""")


def site_link_errors(docs: Path, name: str, text: str) -> list[str]:
    """Return the links into the docs site that do not exist there, anchors included.

    text is a page the docs site serves at docs/<name> (or a script naming the site).
    """
    errors = []
    for m in SITE_LINK.finditer(text):
        for target in (m.group(1) or m.group(2)).split():
            full = urljoin(SITE + name, target)
            if not full.startswith(SITE):
                continue
            url = urlsplit(full)
            path = unquote(url.path[len(urlsplit(SITE).path):])
            if path and not path.endswith("/"):
                dest = docs / path  # a file the site copies as it is
            else:
                # A page: docs/x.md or docs/x/README.md is x/; the home page is the README.
                page = path.rstrip("/")
                dest = next((p for p in (docs / (page + ".md"), docs / page / "README.md",
                                         docs / page / "index.html") if page and p.is_file()),
                            None) if page else ROOT / "README.md"
            if dest is None or not dest.exists():
                errors.append(f"missing {target}")
            elif url.fragment and dest.suffix == ".md" and url.fragment not in anchors(dest.read_text()):
                errors.append(f"missing anchor {target}")
    return errors


def code_doc_ref_errors(root: Path, names: Iterable[str]) -> list[str]:
    """Return every docs/*.md path named in a code file that does not exist under the repository root."""
    dead = []
    for name in names:
        if not CODE_FILE.search(name):
            continue
        for n, line in enumerate((root / name).read_text(errors="replace").splitlines(), 1):
            dead += [f"{name}:{n}: {m.group(1)}" for m in CODE_DOC_REF.finditer(line)
                     if not (root / m.group(1)).is_file()]
    return dead


def evidence_errors(text: str) -> list[str]:
    """Return each Observed model without its own source link or an explicit missing-run entry."""
    errors, header, table_found = [], None, False
    for n, line in prose_lines(text):
        if not line.startswith("|"):
            header = None
            continue
        cells = [c.strip() for c in line.strip().strip("|").split("|")]
        if cells[0] == "Feature":
            header = cells
            if "ST10" in header:
                table_found = True
            continue
        if not header or "ST10" not in header or all(c in "-: " for c in cells[0]):
            continue
        if len(cells) != len(header) or "Evidence by model" not in header:
            errors.append(f"{n}: feature table needs an Evidence by model column")
            continue
        row = dict(zip(header, cells, strict=False))
        entries = {}
        for entry in row["Evidence by model"].split("; "):
            model, sep, value = entry.partition(": ")
            if sep:
                entries[model] = value
        for model in ("ST10", "ST20", "WLA", "ST300"):
            if row.get(model) != "Observed":
                continue
            evidence = entries.get(model, "")
            if evidence != "Missing run" and not LINK.search(evidence):
                errors.append(f"{n}: {row['Feature']} / {model} needs a source link or Missing run")
    if not table_found:
        errors.append("missing model feature table")
    return errors


class DocLinksTest(unittest.TestCase):
    """Links, ADR numbers and paths in this repository resolve."""

    def test_relative_links_and_anchors_resolve(self) -> None:
        """Every relative link in a tracked Markdown file reaches a file and its anchor."""
        dead = [f"{name}:{error}" for name in tracked("*.md")
                for error in link_errors(ROOT / name)]
        self.assertEqual(dead, [])

    def test_observed_features_have_evidence_or_explicit_gaps(self) -> None:
        """Every Observed cell of the compatibility table has a source link or Missing run."""
        self.assertEqual(evidence_errors((ROOT / "docs/owners/compatibility.md").read_text()), [])

    def test_adr_numbers_and_repository_paths_exist(self) -> None:
        """Every ADR number and backticked repository path in the Markdown exists."""
        adrs = {p.name[:4] for p in (ROOT / "docs/decisions").glob("[0-9]*.md")}
        known = set(subprocess.run(["git", "ls-files"], cwd=ROOT, check=True, capture_output=True,
                                   text=True).stdout.split())  # app/vendor included: docs name it
        known |= {str(parent) for name in known for parent in Path(name).parents}
        dead = []
        for name in tracked("*.md"):
            text = (ROOT / name).read_text(errors="replace")
            for n, line in enumerate(text.splitlines(), 1):
                for m in ADR_REF.finditer(line):
                    dead += [f"{name}:{n}: ADR {d}" for d in re.findall(r"\d{4}", m.group(1))
                             if d not in adrs]
                for m in REPO_PATH.finditer(line):
                    # `path/file.go:Symbol` names a file; test_adr_code_refs.py checks the symbol.
                    target = re.sub(r"(\.go):[A-Za-z_][\w.]*$", r"\1", m.group(1).rstrip("/.,"))
                    if not any(c in target for c in "*<>{$[…") and target not in known:
                        dead.append(f"{name}:{n}: {target}")
        self.assertEqual(dead, [])


    def test_docs_named_in_code_exist(self) -> None:
        """Every docs page a code file names exists."""
        names = [n for n in tracked() if n != "os/tests/docs/test_doc_links.py"]
        self.assertEqual(code_doc_ref_errors(ROOT, names), [])

    def test_installer_links_reach_pages_of_the_site(self) -> None:
        """The installer's links reach pages of the site.

        The installer is served from the site, so its links follow the site's layout
        (docs/owners/install.md is owners/install/), which test_relative_links does not read.
        """
        dead = [f"{name}: {error}" for name in ("installer/index.html", "installer/install.sh",
                                                "installer/install.ps1")
                for error in site_link_errors(ROOT / "docs", name, (ROOT / "docs" / name).read_text())]
        self.assertEqual(dead, [])

    def test_site_links_follow_the_site_layout(self) -> None:
        """Site links resolve as the site lays out pages, missing pages and anchors reported."""
        docs = ROOT / "docs"
        page = 'href="../owners/install/#going-back-to-bose" data-href="../install/ ../assets/logo.svg"'
        self.assertEqual(site_link_errors(docs, "installer/index.html", page), ["missing ../install/"])
        self.assertEqual(site_link_errors(docs, "installer/index.html", 'href="../owners/install/#nowhere"'),
                         ["missing anchor ../owners/install/#nowhere"])
        self.assertEqual(site_link_errors(docs, "installer/install.sh",
                                          "say https://stein155.github.io/soundtap/installer/versions.json"), [])


class DocumentationChecksTest(unittest.TestCase):
    """The checkers above on small hand-made inputs."""

    def test_heading_formats_duplicates_and_fenced_examples(self) -> None:
        """Anchors follow GitHub for markup, duplicates, setext and HTML ids, and skip fences."""
        text = """# A `key` & [link](file.md)
# A `key` & [link](file.md)
## Café / USB
Setext heading
--------------
<a id="explicit"></a>
~~~sh
# Not a heading
~~~
````md
```
# Still fenced
````
"""
        self.assertEqual(anchors(text), {"a-key--link", "a-key--link-1", "café--usb",
                                         "setext-heading", "explicit"})

    def test_missing_file_and_anchor_are_distinct(self) -> None:
        """A missing file and a missing anchor are reported apart; remote and fenced links are not."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "other.md").write_text("# Other\n")
            path = root / "page.md"
            path.write_text("""# Here
[valid](other.md#other) [local](#here)
[bad](other.md#gone) [absent](missing.md#gone)
[remote](https://example.org/#anything)
```md
[example](missing-example.md#anything)
```
""")
            self.assertEqual(link_errors(path), ["3: missing anchor other.md#gone",
                                                  "3: missing file missing.md#gone"])

    def test_docs_named_in_code(self) -> None:
        """Missing docs pages named in code files are reported; other files are not read."""
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "docs/wla").mkdir(parents=True)
            (root / "docs/wla/buttons.md").write_text("# Buttons\n")
            (root / "a.go").write_text("// See docs/wla/buttons.md and ../../docs/wla/buttons.md#keys.\n"
                                       "// Gone: docs/archive/old.md\n")
            (root / "b.c").write_text("/* docs/st20/missing.md */\n")
            (root / "notes.txt").write_text("docs/not-code.md\n")
            self.assertEqual(code_doc_ref_errors(root, ["a.go", "b.c", "notes.txt"]),
                             ["a.go:2: docs/archive/old.md", "b.c:1: docs/st20/missing.md"])

    def test_evidence_is_per_model_and_missing_runs_are_not_hidden(self) -> None:
        """Evidence must name each Observed model, and a table without the column fails."""
        header = "| Feature | ST10 | ST20 | WLA | ST300 | Evidence by model |\n"
        row = "| IR | Observed | Observed | Built | No | {} |\n"
        self.assertEqual(evidence_errors(header + row.format(
            "ST10: [run](ir.md#run); ST20: Missing run")), [])
        for evidence in ("", "ST10: [run](ir.md#run)", "ST10: passed; ST20: Missing run"):
            with self.subTest(evidence=evidence):
                self.assertTrue(evidence_errors(header + row.format(evidence)))
        self.assertTrue(evidence_errors(""))
        self.assertTrue(evidence_errors("| Feature | ST10 |\n| IR | Observed |\n"))


if __name__ == "__main__":
    unittest.main()
