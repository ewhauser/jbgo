#!/usr/bin/env python3
"""
Diff commands and flags between vercel-labs/just-bash (TypeScript upstream)
and gbash (Go port).

Outputs a structured markdown section suitable for appending to TODO.md.

Usage:
    python diff_commands.py <upstream_repo_path> <go_repo_path> [--update-todo]

When --update-todo is passed the script rewrites the ## Command Parity section
in TODO.md (creating it if absent).  Without the flag it prints to stdout.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
from dataclasses import dataclass, field
from pathlib import Path


# ---------------------------------------------------------------------------
# Data model
# ---------------------------------------------------------------------------

@dataclass
class CommandInfo:
    name: str
    flags: list[str] = field(default_factory=list)  # e.g. ["-i", "--ignore-case", "-r", ...]


# ---------------------------------------------------------------------------
# Upstream (TypeScript) extraction
# ---------------------------------------------------------------------------

# Matches help option lines like:
#   "-i, --ignore-case        ignore case distinctions"
#   "-m NUM, --max-count=NUM  stop after NUM matches"
#   "    --include=GLOB       search only files matching GLOB"
_OPT_RE = re.compile(
    r"""
    ^\s*"                       # opening quote + optional leading whitespace
    (                           # capture group: the flags portion
        -[^\s,]+                # first flag token
        (?:\s*,\s*-[^\s,=]+)?  # optional ", --long-form"
    )
    """,
    re.VERBOSE,
)

# Matches the help object start: const <name>Help = {
_HELP_OBJ_RE = re.compile(r"const\s+(\w+)Help\s*[:=]")

# Matches command names registered in the registry
_REGISTRY_NAME_RE = re.compile(r'name:\s*"([^"]+)"')

# Match individual flag tokens like -i, --ignore-case, -R
_FLAG_TOKEN_RE = re.compile(r"--?[a-zA-Z0-9][-a-zA-Z0-9]*")


def _extract_flags_from_help_lines(lines: list[str]) -> list[str]:
    """Pull unique flag tokens from the options array lines of a help object."""
    flags: list[str] = []
    seen: set[str] = set()
    for line in lines:
        m = _OPT_RE.match(line)
        if not m:
            continue
        for tok in _FLAG_TOKEN_RE.findall(m.group(1)):
            if tok == "--help":
                continue
            if tok not in seen:
                seen.add(tok)
                flags.append(tok)
    return flags


def parse_upstream_commands(repo: Path) -> dict[str, CommandInfo]:
    """Return {name: CommandInfo} for every command in the upstream registry."""
    commands: dict[str, CommandInfo] = {}

    # 1. Get all registered command names from registry.ts
    registry_path = repo / "src" / "commands" / "registry.ts"
    if registry_path.exists():
        text = registry_path.read_text()
        for m in _REGISTRY_NAME_RE.finditer(text):
            name = m.group(1)
            commands[name] = CommandInfo(name=name)

    # 2. Walk command directories and extract help objects
    cmds_dir = repo / "src" / "commands"
    if not cmds_dir.is_dir():
        return commands

    for ts_file in sorted(cmds_dir.rglob("*.ts")):
        text = ts_file.read_text()

        # Find help objects
        for m in _HELP_OBJ_RE.finditer(text):
            # Find the options array that follows
            start = m.end()
            options_match = re.search(r"options:\s*\[", text[start:start + 500])
            if not options_match:
                continue

            arr_start = start + options_match.end()
            # Collect lines until closing ]
            arr_end = text.find("]", arr_start)
            if arr_end < 0:
                continue
            option_lines = text[arr_start:arr_end].strip().split("\n")

            flags = _extract_flags_from_help_lines(option_lines)
            # Determine which command this help object belongs to
            help_name = m.group(1)
            # Normalize: e.g. "grepHelp" -> "grep", "lsHelp" -> "ls"
            # Some may be like "base64Help" -> "base64"
            cmd_name = help_name.replace("Help", "").replace("_", "-")

            # Map known help name variations
            name_map = {
                "htmlToMarkdown": "html-to-markdown",
                "md5sum": "md5sum",
                "sha1sum": "sha1sum",
                "sha256sum": "sha256sum",
            }
            cmd_name = name_map.get(help_name.replace("Help", ""), cmd_name)

            if cmd_name in commands:
                commands[cmd_name].flags = flags
            else:
                commands[cmd_name] = CommandInfo(name=cmd_name, flags=flags)

    return commands


# ---------------------------------------------------------------------------
# Go-side extraction
# ---------------------------------------------------------------------------

# Matches flag checks in argument parsing code
# Patterns like: case "-i", "--ignore-case":
#                arg == "-i" || arg == "--ignore-case"
#                "-i", "--ignore-case":
_GO_FLAG_RE = re.compile(r'(?:case\s+|==\s*|")(--?[a-zA-Z0-9][-a-zA-Z0-9]*)"')

# Matches help text flag lines like:
#   -L, --location         follow redirects
_GO_HELP_FLAG_RE = re.compile(r"^\s*(-[a-zA-Z0-9],\s+--[a-zA-Z0-9-]+|--[a-zA-Z0-9-]+|-[a-zA-Z0-9])")


def _extract_go_flags_from_file(filepath: Path) -> list[str]:
    """Extract flags from a Go command implementation file."""
    text = filepath.read_text()
    flags: list[str] = []
    seen: set[str] = set()

    # Method 1: Extract from help text constants
    # Look for const ...HelpText = `...` blocks
    for m in re.finditer(r'(?:HelpText|helpText|Help)\s*(?:=|:=)\s*`([^`]+)`', text):
        help_text = m.group(1)
        for line in help_text.split("\n"):
            for tok in _FLAG_TOKEN_RE.findall(line):
                if tok in ("--help", "--version"):
                    continue
                if tok not in seen:
                    seen.add(tok)
                    flags.append(tok)

    # Method 2: Extract from argument parsing switch/case statements
    # Look for parseXxxArgs functions or Run method arg parsing
    for m in _GO_FLAG_RE.finditer(text):
        tok = m.group(1)
        if tok in ("--help", "--version", "--"):
            continue
        if tok not in seen:
            seen.add(tok)
            flags.append(tok)

    return flags


def parse_go_commands(repo: Path) -> dict[str, CommandInfo]:
    """Return {name: CommandInfo} for every command in the Go registry."""
    cmds_dir = repo / "commands"
    if not cmds_dir.is_dir():
        return {}

    # Step 1: Collect ALL flags from ALL non-test Go files, indexed by filename.
    # Also build a map of which files call functions defined in other files
    # (e.g., head.go calls parseHeadTailArgs from head_tail.go).
    file_flags: dict[str, list[str]] = {}
    all_files: dict[str, str] = {}  # filename -> content

    for go_file in sorted(cmds_dir.glob("*.go")):
        if go_file.name.endswith("_test.go"):
            continue
        text = go_file.read_text()
        all_files[go_file.name] = text
        file_flags[go_file.name] = _extract_go_flags_from_file(go_file)

    # Step 2: Build cross-file call graph.  If head.go calls parseHeadTailArgs,
    # and that function is defined in head_tail.go, then head gets head_tail's flags too.
    func_to_file: dict[str, str] = {}  # funcName -> filename
    for fname, text in all_files.items():
        for m in re.finditer(r"^func\s+(\w+)\(", text, re.MULTILINE):
            func_to_file[m.group(1)] = fname

    file_deps: dict[str, set[str]] = {}  # filename -> set of filenames it calls into
    for fname, text in all_files.items():
        deps: set[str] = set()
        for m in re.finditer(r"\b(parse\w+Args|read\w+)\(", text):
            called = m.group(1)
            if called in func_to_file and func_to_file[called] != fname:
                deps.add(func_to_file[called])
        file_deps[fname] = deps

    # Step 3: Find command names.  Two patterns:
    #   a) return "cmd"  (literal string)
    #   b) return c.name  (dynamic, set in constructor)
    cmd_to_files: dict[str, list[str]] = {}  # command name -> list of source files

    for fname, text in all_files.items():
        # Pattern a: literal name
        for m in re.finditer(
            r'func\s+\([^)]+\)\s+Name\(\)\s+string\s*\{\s*return\s+"([^"]+)"',
            text,
        ):
            cmd_name = m.group(1)
            cmd_to_files.setdefault(cmd_name, []).append(fname)

        # Pattern b: dynamic name via field — find constructors that set the name
        for m in re.finditer(
            r'func\s+New\w+\([^)]*\)\s+\*\w+\s*\{[^}]*name:\s*"([^"]+)"',
            text,
            re.DOTALL,
        ):
            cmd_name = m.group(1)
            cmd_to_files.setdefault(cmd_name, []).append(fname)

    # Step 4: Assemble flags per command, including transitive deps
    result: dict[str, CommandInfo] = {}
    for cmd_name, fnames in cmd_to_files.items():
        seen: set[str] = set()
        flags: list[str] = []
        # Collect from all source files and their deps
        all_fnames = set(fnames)
        for fn in fnames:
            all_fnames |= file_deps.get(fn, set())
        for fn in sorted(all_fnames):
            for f in file_flags.get(fn, []):
                if f not in seen:
                    seen.add(f)
                    flags.append(f)
        result[cmd_name] = CommandInfo(name=cmd_name, flags=flags)

    return result


# ---------------------------------------------------------------------------
# Diff logic
# ---------------------------------------------------------------------------

@dataclass
class CommandDiff:
    missing_commands: list[str]          # in upstream but not in Go
    missing_flags: dict[str, list[str]]  # cmd -> flags in upstream but not Go
    extra_commands: list[str]            # in Go but not upstream (informational)


def diff_commands(upstream: dict[str, CommandInfo], go: dict[str, CommandInfo]) -> CommandDiff:
    upstream_names = set(upstream.keys())
    go_names = set(go.keys())

    missing_commands = sorted(upstream_names - go_names)
    extra_commands = sorted(go_names - upstream_names)

    missing_flags: dict[str, list[str]] = {}
    for name in sorted(upstream_names & go_names):
        up_flags = set(upstream[name].flags)
        go_flags = set(go[name].flags)
        missing = sorted(up_flags - go_flags)
        if missing:
            missing_flags[name] = missing

    return CommandDiff(
        missing_commands=missing_commands,
        missing_flags=missing_flags,
        extra_commands=extra_commands,
    )


# ---------------------------------------------------------------------------
# Markdown generation
# ---------------------------------------------------------------------------

def generate_markdown(diff: CommandDiff, upstream: dict[str, CommandInfo]) -> str:
    lines: list[str] = []
    lines.append("## Command Parity")
    lines.append("")
    lines.append("Gap analysis vs [vercel-labs/just-bash](https://github.com/vercel-labs/just-bash).")
    lines.append("Auto-generated — re-run the upstream-diff skill to refresh.")
    lines.append("")

    if diff.missing_commands:
        lines.append("### Missing Commands")
        lines.append("")
        for cmd in diff.missing_commands:
            up = upstream.get(cmd)
            flag_hint = ""
            if up and up.flags:
                flag_hint = f" (upstream flags: `{'`, `'.join(up.flags[:8])}`"
                if len(up.flags) > 8:
                    flag_hint += ", ..."
                flag_hint += ")"
            lines.append(f"- [ ] `{cmd}`{flag_hint}")
        lines.append("")

    if diff.missing_flags:
        lines.append("### Missing Flags")
        lines.append("")
        lines.append("Commands that exist in both repos but have flags in upstream not yet ported.")
        lines.append("")
        for cmd, flags in sorted(diff.missing_flags.items()):
            lines.append(f"#### `{cmd}`")
            lines.append("")
            for flag in flags:
                lines.append(f"- [ ] `{flag}`")
            lines.append("")

    if not diff.missing_commands and not diff.missing_flags:
        lines.append("Full parity achieved — no missing commands or flags detected.")
        lines.append("")

    return "\n".join(lines)


# ---------------------------------------------------------------------------
# TODO.md update
# ---------------------------------------------------------------------------

def update_todo(todo_path: Path, new_section: str) -> None:
    """Replace or append the ## Command Parity section in TODO.md."""
    if todo_path.exists():
        text = todo_path.read_text()
    else:
        text = "# TODO\n\n"

    # Find existing section
    pattern = re.compile(
        r"(^## Command Parity\s*$.*?)(?=^## |\Z)",
        re.MULTILINE | re.DOTALL,
    )
    m = pattern.search(text)
    if m:
        text = text[:m.start()] + new_section + "\n" + text[m.end():]
    else:
        # Append before ## Intentional Non-Goals if it exists, otherwise at end
        non_goals = text.find("## Intentional Non-Goals")
        if non_goals >= 0:
            text = text[:non_goals] + new_section + "\n" + text[non_goals:]
        else:
            text = text.rstrip() + "\n\n" + new_section + "\n"

    todo_path.write_text(text)


# ---------------------------------------------------------------------------
# Clone helper
# ---------------------------------------------------------------------------

def clone_upstream(dest: Path) -> Path:
    """Clone vercel-labs/just-bash to a temp directory, return the path."""
    if dest.exists() and (dest / "src" / "commands").is_dir():
        # Already cloned, just pull
        subprocess.run(
            ["git", "-C", str(dest), "pull", "--ff-only"],
            capture_output=True,
        )
        return dest

    subprocess.run(
        [
            "git", "clone", "--depth", "1",
            "https://github.com/vercel-labs/just-bash.git",
            str(dest),
        ],
        check=True,
        env={**os.environ, "GIT_LFS_SKIP_SMUDGE": "1"},
    )
    return dest


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    parser = argparse.ArgumentParser(description="Diff just-bash upstream vs Go port")
    parser.add_argument("upstream", type=Path, help="Path to vercel-labs/just-bash checkout")
    parser.add_argument("go_repo", type=Path, help="Path to gbash repo")
    parser.add_argument("--update-todo", action="store_true",
                        help="Write results into TODO.md instead of stdout")
    parser.add_argument("--json", action="store_true",
                        help="Output raw JSON diff data")
    args = parser.parse_args()

    upstream = parse_upstream_commands(args.upstream)
    go = parse_go_commands(args.go_repo)

    result = diff_commands(upstream, go)

    if args.json:
        data = {
            "missing_commands": result.missing_commands,
            "missing_flags": result.missing_flags,
            "extra_commands": result.extra_commands,
        }
        print(json.dumps(data, indent=2))
        return

    md = generate_markdown(result, upstream)

    if args.update_todo:
        todo_path = args.go_repo / "TODO.md"
        update_todo(todo_path, md)
        print(f"Updated {todo_path}")
    else:
        print(md)


if __name__ == "__main__":
    main()
