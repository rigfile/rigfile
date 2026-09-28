"""Checks that a release looks complete: a version, a changelog entry for it, and a dist/ folder."""
import pathlib
import sys


def main() -> int:
    root = pathlib.Path(".")
    problems = []
    version_file = root / "VERSION"
    version = version_file.read_text().strip() if version_file.exists() else ""
    if not version:
        problems.append("no VERSION file")
    changelog = root / "CHANGELOG.md"
    if not changelog.exists() or version not in changelog.read_text():
        problems.append("CHANGELOG.md has no entry for " + (version or "the version"))
    if not (root / "dist").is_dir():
        problems.append("no dist/ folder")
    for p in problems:
        print("release check:", p)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
