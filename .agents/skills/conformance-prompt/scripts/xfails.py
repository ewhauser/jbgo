#!/usr/bin/env python3
"""Query xfail entries from the conformance manifest.

Usage:
    python xfails.py                          # summary table of xfail counts by file
    python xfails.py <file>                   # list xfails for a specific test file
    python xfails.py --total                  # just print total count
    python xfails.py --exclude <file> [...]   # summary excluding listed files

Examples:
    python xfails.py builtin-trap             # matches builtin-trap*.test.sh
    python xfails.py --exclude builtin-trap builtin-trap-bash builtin-trap-err
"""

import json
import sys
from pathlib import Path

MANIFEST = Path(__file__).resolve().parents[4] / "internal" / "conformance" / "manifest.json"


def load_xfails():
    with open(MANIFEST) as f:
        data = json.load(f)
    entries = data.get("suites", {}).get("bash", {}).get("entries", {})
    xfails = {}
    for key, val in entries.items():
        if val.get("mode") != "xfail":
            continue
        parts = key.split("::", 1)
        file_part = parts[0]
        file_name = file_part.removeprefix("oils/")
        test_name = parts[1] if len(parts) > 1 else ""
        goos = val.get("goos", [])
        reason = val.get("reason", "")
        xfails.setdefault(file_name, []).append({
            "key": key,
            "test": test_name,
            "reason": reason,
            "goos": goos,
        })
    return xfails


def print_summary(xfails, exclude=None):
    exclude = set(exclude or [])
    total = 0
    for file_name, ents in sorted(xfails.items(), key=lambda x: -len(x[1])):
        stem = file_name.removesuffix(".test.sh")
        if stem in exclude or file_name in exclude:
            continue
        print(f"{len(ents):3d}  {file_name}")
        total += len(ents)
    print(f"---")
    print(f"{total:3d}  TOTAL")


def print_file_xfails(xfails, pattern):
    matches = {f: e for f, e in xfails.items() if pattern in f}
    if not matches:
        print(f"No xfails matching '{pattern}'", file=sys.stderr)
        sys.exit(1)
    for file_name, ents in sorted(matches.items()):
        print(f"=== {file_name} ({len(ents)}) ===")
        for entry in ents:
            goos_str = f"  [goos: {', '.join(entry['goos'])}]" if entry["goos"] else ""
            print(f"  {entry['test']}{goos_str}")
            if entry["reason"] != "conformance: gbash behavior differs from bash":
                print(f"    reason: {entry['reason']}")
        print()


def main():
    args = sys.argv[1:]
    xfails = load_xfails()

    if not args:
        print_summary(xfails)
        return

    if args[0] == "--total":
        print(sum(len(v) for v in xfails.values()))
        return

    if args[0] == "--exclude":
        print_summary(xfails, exclude=args[1:])
        return

    print_file_xfails(xfails, args[0])


if __name__ == "__main__":
    main()
