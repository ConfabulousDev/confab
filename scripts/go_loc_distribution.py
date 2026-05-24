#!/usr/bin/env python3
"""Report LOC distribution for non-test Go files."""

from __future__ import annotations

import argparse
from pathlib import Path


DEFAULT_BUCKETS = (
    (0, 49),
    (50, 99),
    (100, 199),
    (200, 299),
    (300, 399),
    (400, 499),
    (500, 999),
    (1000, None),
)


def repo_root() -> Path:
    return Path(__file__).resolve().parents[1]


def go_files(root: Path) -> list[Path]:
    ignored_dirs = {".git", ".venv", "vendor"}
    files: list[Path] = []

    for path in root.rglob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        if any(part in ignored_dirs for part in path.relative_to(root).parts):
            continue
        files.append(path)

    return sorted(files)


def count_lines(path: Path) -> int:
    with path.open("rb") as file:
        return sum(1 for _ in file)


def percentile(sorted_counts: list[int], pct: float) -> int:
    if not sorted_counts:
        return 0
    index = int((len(sorted_counts) - 1) * pct)
    return sorted_counts[index]


def print_report(root: Path, show_files: bool, top: int) -> None:
    counts = sorted((count_lines(path), path.relative_to(root)) for path in go_files(root))
    line_counts = [count for count, _ in counts]
    total_lines = sum(line_counts)
    total_files = len(counts)

    print(f"Root: {root}")
    print("Scope: *.go excluding *_test.go")
    print()

    if not counts:
        print("No non-test Go files found.")
        return

    print("Summary")
    print(f"  files:  {total_files}")
    print(f"  lines:  {total_lines}")
    print(f"  mean:   {total_lines / total_files:.1f}")
    print(f"  min:    {line_counts[0]}")
    print(f"  p25:    {percentile(line_counts, 0.25)}")
    print(f"  median: {percentile(line_counts, 0.50)}")
    print(f"  p75:    {percentile(line_counts, 0.75)}")
    print(f"  p90:    {percentile(line_counts, 0.90)}")
    print(f"  max:    {line_counts[-1]}")
    print()

    print("Buckets")
    for low, high in DEFAULT_BUCKETS:
        bucket_counts = [
            count
            for count in line_counts
            if count >= low and (high is None or count <= high)
        ]
        label = f"{low}+" if high is None else f"{low}-{high}"
        bucket_lines = sum(bucket_counts)
        print(
            f"  {label:>7}: "
            f"{len(bucket_counts):>2} files "
            f"({len(bucket_counts) / total_files:>5.1%}), "
            f"{bucket_lines:>5} lines "
            f"({bucket_lines / total_lines:>5.1%})"
        )

    if top > 0:
        print()
        print(f"Top {min(top, total_files)} Largest")
        for count, path in reversed(counts[-top:]):
            print(f"  {count:>4} {path}")

    if show_files:
        print()
        print("All Files")
        for count, path in counts:
            print(f"  {count:>4} {path}")


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Report LOC distribution for non-test Go files."
    )
    parser.add_argument(
        "root",
        nargs="?",
        type=Path,
        default=repo_root(),
        help="Repository root to scan. Defaults to this script's repo.",
    )
    parser.add_argument(
        "--all",
        action="store_true",
        help="Print every file in ascending LOC order.",
    )
    parser.add_argument(
        "--top",
        type=int,
        default=10,
        help="Number of largest files to print. Use 0 to hide.",
    )
    args = parser.parse_args()

    print_report(args.root.resolve(), args.all, args.top)


if __name__ == "__main__":
    main()
