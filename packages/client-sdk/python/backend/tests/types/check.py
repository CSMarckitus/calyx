"""Checks the type samples with mypy and pyright, both in strict mode.

Usage: python check.py <folder with good.py and bad.py>

Each wrong line of bad.py ends with a comment like this one:

    # expect: mypy=call-arg pyright=reportCallIssue

A tool can have more than one code on a line: `mypy=arg-type,call-arg`. The check fails when a
tool misses an expected error, gives an error on another line, or gives any error for good.py.
"""

import json
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

EXPECT = re.compile(r"# expect:(?P<tools>( (mypy|pyright)=[\w,-]+)+)\s*$")
Errors = set[tuple[int, str]]


def expected(source: Path) -> dict[str, Errors]:
    """Read the expected errors of each tool from the comments of a file."""
    found: dict[str, Errors] = {"mypy": set(), "pyright": set()}
    for number, line in enumerate(source.read_text().splitlines(), start=1):
        if "# expect:" not in line:
            continue
        match = EXPECT.search(line)
        if match is None:
            sys.exit(f"{source.name}:{number}: the expect comment has a wrong format.")
        for pair in match["tools"].split():
            tool, codes = pair.split("=")
            found[tool].update((number, code) for code in codes.split(","))
    return found


def run_mypy(folder: Path, names: list[str]) -> dict[str, Errors]:
    """Give the errors of mypy for each file."""
    done = subprocess.run(
        ["mypy", "--strict", "--python-version", "3.12", "--output", "json", *names],
        cwd=folder,
        capture_output=True,
        text=True,
    )
    found: dict[str, Errors] = {name: set() for name in names}
    for text in done.stdout.splitlines():
        item = json.loads(text)
        if item["severity"] == "error":
            found[item["file"]].add((item["line"], item["code"]))
    return found


def run_pyright(folder: Path, names: list[str]) -> dict[str, Errors]:
    """Give the errors of pyright for each file."""
    (folder / "pyrightconfig.json").write_text(
        json.dumps({"typeCheckingMode": "strict", "pythonVersion": "3.12"})
    )
    done = subprocess.run(
        ["pyright", "--outputjson", *names], cwd=folder, capture_output=True, text=True
    )
    found: dict[str, Errors] = {name: set() for name in names}
    for item in json.loads(done.stdout)["generalDiagnostics"]:
        if item["severity"] == "error":
            # Pyright counts lines from 0. An error with no rule has the code "none".
            found[Path(item["file"]).name].add(
                (item["range"]["start"]["line"] + 1, item.get("rule", "none"))
            )
    return found


def main(samples: Path) -> int:
    """Run both tools on a copy of the samples. The copy keeps the repository free of caches."""
    names = ["good.py", "bad.py"]
    failed = False
    with tempfile.TemporaryDirectory() as temporary:
        folder = Path(temporary)
        for name in names:
            shutil.copy(samples / name, folder / name)
        results = {"mypy": run_mypy(folder, names), "pyright": run_pyright(folder, names)}
        wanted = expected(samples / "bad.py")
        for tool, found in results.items():
            for name, want in (("good.py", set[tuple[int, str]]()), ("bad.py", wanted[tool])):
                for line, code in sorted(want - found[name]):
                    print(f"{tool}: {name}:{line}: expected the error {code}, found none.")
                    failed = True
                for line, code in sorted(found[name] - want):
                    print(f"{tool}: {name}:{line}: unexpected error {code}.")
                    failed = True
    if not failed:
        print("Type samples: every expected error occurs, and no other error occurs.")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(Path(sys.argv[1])))
