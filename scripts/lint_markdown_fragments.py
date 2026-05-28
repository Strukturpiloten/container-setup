#!/usr/bin/env python3

from __future__ import annotations

import argparse
import html
import re
import sys
import unicodedata
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import unquote, urlparse


HEADING_RE = re.compile(r"^(#{1,6})[ \t]+(.+?)(?:[ \t]+#+)?[ \t]*$")
LINK_RE = re.compile(r"(?<!!)\[[^\]]+\]\(([^)]+)\)")


@dataclass(frozen=True)
class FragmentError:
    path: Path
    line: int
    column: int
    href: str
    target: Path


def strip_inline_markup(text: str) -> str:
    text = html.unescape(text)
    text = re.sub(r"!\[([^\]]*)\]\([^)]+\)", r"\1", text)
    text = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", text)
    text = re.sub(r"`([^`]*)`", r"\1", text)
    text = re.sub(r"<[^>]+>", "", text)
    text = text.replace("*", "")
    text = text.replace("_", "")
    return text


def github_slug(text: str) -> str:
    text = strip_inline_markup(text)
    normalized = unicodedata.normalize("NFKD", text)
    parts: list[str] = []
    last_was_hyphen = False

    for index, char in enumerate(normalized.lower()):
        if unicodedata.category(char).startswith("M"):
            continue
        if char.isalnum():
            parts.append(char)
            last_was_hyphen = False
            continue
        if char in {" ", "-"}:
            if parts and not last_was_hyphen:
                parts.append("-")
                last_was_hyphen = True
            continue
        if unicodedata.category(char).startswith("S"):
            next_char = normalized[index + 1] if index + 1 < len(normalized) else ""
            if next_char == "\ufe0f":
                continue
            if parts and not last_was_hyphen:
                parts.append("-")
                last_was_hyphen = True

    slug = "".join(parts).lstrip("-")
    if text.rstrip().endswith("\ufe0f"):
        return f"{slug}\ufe0f"
    return slug


def collect_anchors(path: Path) -> set[str]:
    anchors: set[str] = set()
    occurrences: dict[str, int] = {}
    in_fence = False
    fence_marker = ""

    for line in path.read_text(encoding="utf-8").splitlines():
        stripped = line.lstrip()
        if stripped.startswith(("```", "~~~")):
            marker = stripped[:3]
            if not in_fence:
                in_fence = True
                fence_marker = marker
            elif marker == fence_marker:
                in_fence = False
                fence_marker = ""
            continue
        if in_fence:
            continue

        match = HEADING_RE.match(line)
        if not match:
            continue

        slug = github_slug(match.group(2).strip())
        if not slug:
            continue

        count = occurrences.get(slug, 0)
        anchor = slug if count == 0 else f"{slug}-{count}"
        occurrences[slug] = count + 1
        anchors.add(anchor)

    return anchors


def extract_href(raw_target: str) -> str:
    target = raw_target.strip()
    if target.startswith("<") and ">" in target:
        return target[1 : target.index(">")]
    if " " in target:
        return target.split(" ", 1)[0]
    return target


def iter_fragment_errors(repo_root: Path, paths: list[Path]) -> list[FragmentError]:
    anchors_by_path: dict[Path, set[str]] = {}
    errors: list[FragmentError] = []

    for path in paths:
        text = path.read_text(encoding="utf-8")
        in_fence = False
        fence_marker = ""

        for line_number, line in enumerate(text.splitlines(), start=1):
            stripped = line.lstrip()
            if stripped.startswith(("```", "~~~")):
                marker = stripped[:3]
                if not in_fence:
                    in_fence = True
                    fence_marker = marker
                elif marker == fence_marker:
                    in_fence = False
                    fence_marker = ""
                continue
            if in_fence:
                continue

            for match in LINK_RE.finditer(line):
                href = extract_href(match.group(1))
                if "#" not in href:
                    continue

                link_target, fragment = href.split("#", 1)
                if not fragment:
                    continue

                parsed = urlparse(link_target)
                if (
                    parsed.scheme
                    or href.startswith("mailto:")
                    or href.startswith("tel:")
                ):
                    continue

                if link_target:
                    target_path = (path.parent / unquote(link_target)).resolve()
                else:
                    target_path = path

                if (
                    target_path.suffix.lower() not in {".md", ".markdown"}
                    and target_path != path
                ):
                    continue
                if not target_path.exists():
                    continue

                anchors = anchors_by_path.setdefault(
                    target_path, collect_anchors(target_path)
                )
                if unquote(fragment) in anchors:
                    continue

                errors.append(
                    FragmentError(
                        path=path,
                        line=line_number,
                        column=match.start(1) + 1,
                        href=href,
                        target=target_path,
                    )
                )

    return errors


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Validate Markdown link fragments against headings."
    )
    parser.add_argument("paths", nargs="+", help="Markdown files to inspect")
    args = parser.parse_args()

    repo_root = Path.cwd().resolve()
    paths = [(repo_root / path).resolve() for path in args.paths]
    errors = iter_fragment_errors(repo_root, paths)

    if not errors:
        return 0

    for error in errors:
        path_display = error.path.relative_to(repo_root)
        target_display = error.target.relative_to(repo_root)
        print(
            f"{path_display}:{error.line}:{error.column}: MD051/link-fragments "
            f"Link fragment '{error.href}' does not match a heading in {target_display}.",
            file=sys.stderr,
        )

    return 1


if __name__ == "__main__":
    sys.exit(main())
