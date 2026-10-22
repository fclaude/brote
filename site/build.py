#!/usr/bin/env python3
"""Assemble site/content from the repository's own documentation.

docs/*.md and README.md stay the canonical text (Go tests read docs/scheme.md,
and GitHub shows the README), so the site is built from them and nothing under
site/content is checked in. Run it from anywhere, then `zola build` in site/:

    python3 site/build.py && (cd site && zola build && zola check --skip-external-links)

The script is deterministic: the same markdown gives the same files.
"""
import pathlib
import re
import shutil

SITE = pathlib.Path(__file__).resolve().parent
ROOT = SITE.parent
REPO = "https://github.com/fclaude/brote/blob/main/"

# source, target in content/, label in the navigation, weight
PAGES = [
    ("README.md", "_index.md", "Home", 1),
    ("docs/tour.md", "tour.md", "Tour", 2),
    ("docs/stdlib.md", "stdlib.md", "Library", 3),
    ("docs/command.md", "command.md", "Command", 4),
    ("docs/scheme.md", "scheme.md", "Reference", 5),
    ("docs/embedding.md", "embedding.md", "Embedding", 6),
]
TARGETS = {pathlib.PurePosixPath(src).name: dst for src, dst, _, _ in PAGES}
TARGETS["README.md"] = "_index.md"

LINK = re.compile(r"(\]\()([^)\s]+)(\))")


def rewrite(target, source):
    """A link written for a file in the repository, as a link on the site."""
    if re.match(r"[a-z]+:", target) or target.startswith("#"):
        return target
    path, _, frag = target.partition("#")
    frag = "#" + frag if frag else ""
    name = pathlib.PurePosixPath(path).name
    if name in TARGETS and path.endswith(".md"):
        return "@/" + TARGETS[name] + frag
    resolved = (pathlib.PurePosixPath(source).parent / path)
    parts = []
    for part in resolved.parts:
        if part == "..":
            parts.pop()
        elif part != ".":
            parts.append(part)
    return REPO + "/".join(parts) + frag


def convert(src, dst, nav, weight):
    text = (ROOT / src).read_text(encoding="utf-8")
    lines = text.split("\n")
    title = None
    body = []
    for line in lines:
        if title is None and line.startswith("# "):
            title = line[2:].strip()
            continue
        if line.strip() in ("<!-- run -->", "<!-- run-shell -->") or line.startswith("[![") and src == "README.md":
            continue
        body.append(line)
    text = "\n".join(body).lstrip("\n")
    text = LINK.sub(lambda m: m.group(1) + rewrite(m.group(2), src) + m.group(3), text)
    # <https://...> autolinks are fine as they are.
    front = ["+++", 'title = "%s"' % title.replace('"', '\\"'), "weight = %d" % weight]
    if dst == "_index.md":
        front += ['sort_by = "weight"', 'template = "index.html"']
    front += ["", "[extra]", 'nav = "%s"' % nav, "+++", ""]
    out = SITE / "content" / dst
    out.write_text("\n".join(front) + text, encoding="utf-8")


def main():
    content = SITE / "content"
    if content.exists():
        shutil.rmtree(content)
    content.mkdir()
    for src, dst, nav, weight in PAGES:
        convert(src, dst, nav, weight)


if __name__ == "__main__":
    main()
