"""Compares the Python blocks of a README with the example files.

Usage: python readme_check.py <folder with README.md and examples/>

Each block is a whole example file, with a marker on the line above it:

    <!-- examples/first_message.py -->
    ```python

The check fails for a block with no marker, a block with other text than its file, and an example
with no block or with more than one. All other blocks have the language bash or text.
"""

import re
import sys
from pathlib import Path

# The start of a block, also in a list or a quote.
FENCE = re.compile(r"[\s>]*(?:(?:[-*+]|\d+[.)])\s+)?(?P<mark>`{3,}|~{3,})(?P<label>.*)")
MARKER = re.compile(r"<!-- (examples/\w+\.py) -->")


def check(folder: Path) -> list[str]:
    """Give the problems of a README."""
    problems: list[str] = []
    shown: list[str] = []
    # The empty first line makes an index of the list a line number.
    lines = ["", *(folder / "README.md").read_text().splitlines()]
    number = 0
    while (number := number + 1) < len(lines):
        fence = FENCE.fullmatch(lines[number])
        if fence is None:
            continue
        start, mark = number, fence["mark"]
        while (number := number + 1) < len(lines) and lines[number].strip(" >") != mark:
            pass
        if number == len(lines):
            problems.append(f"README.md line {start}: the block has no end.")
            continue
        marker = MARKER.fullmatch(lines[start - 1])
        if lines[start] == "```python" and marker is not None:
            name = marker[1]
            shown.append(name)
            block = lines[start + 1 : number]
            if not (folder / name).exists():
                problems.append(f"{name} does not exist.")
            elif (folder / name).read_text() != "\n".join(block) + "\n":
                problems.append(f"{name} is not the same as its block in README.md.")
        elif fence["label"].strip().lower().startswith("py"):
            problems.append(f"README.md line {start}: a Python block has no marker.")
        elif mark != "```" or fence["label"] not in ("bash", "text"):
            problems.append(f"README.md line {start}: use ```bash, ```text or ```python.")
    for path in sorted((folder / "examples").iterdir()):
        name = f"examples/{path.name}"
        if shown.count(name) == 0:
            problems.append(f"{name} has no block in README.md.")
        if shown.count(name) > 1:
            problems.append(f"{name} has {shown.count(name)} blocks in README.md.")
    return problems


if __name__ == "__main__":
    found = check(Path(sys.argv[1]))
    print("\n".join(found) or "README.md shows each example.")
    sys.exit(1 if found else 0)
