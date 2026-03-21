---
name: conformance-prompt
description: Generate scoped prompts for an AI agent to fix conformance xfail entries in gbash's manifest.json. Use this skill whenever the user asks to "write a prompt for", "generate a prompt for", "let's do X next", or "draft a prompt" in the context of conformance test categories or xfail fixes. Also trigger when the user names a conformance category (e.g. "variables", "parsing", "builtins") and wants to produce work items for an AI to tackle.
---

# Conformance Prompt Generator

This skill produces focused, well-scoped prompts that an AI agent can use to fix batches of conformance xfail entries in `internal/conformance/manifest.json`. The prompts are designed to be self-contained — an agent receiving one should have everything it needs to find the tests, understand expected behavior, locate the relevant code, fix the issues, and clean up the manifest.

## Workflow

### 1. Read the current manifest

Use the bundled helper script to query xfails instead of reading the manifest JSON by hand:

```sh
# Summary table of xfail counts by file (sorted descending)
python3 .claude/skills/conformance-prompt/scripts/xfails.py

# List xfails for a specific file (substring match)
python3 .claude/skills/conformance-prompt/scripts/xfails.py alias

# Total count only
python3 .claude/skills/conformance-prompt/scripts/xfails.py --total

# Summary excluding specific files (use stem without .test.sh)
python3 .claude/skills/conformance-prompt/scripts/xfails.py --exclude builtin-trap builtin-trap-bash builtin-trap-err
```

Always run this fresh rather than relying on stale counts from earlier in the conversation.

### 2. Identify the target category

The user will name a category (e.g. "case statements", "builtins I/O", "variable expansion"). Match it to the relevant test files. If the user is vague, list the current xfail counts by file to help them pick.

If the user asks for a summary table first, produce one grouped by category with xfail counts sorted descending before generating any prompts.

### 3. List the xfails

For each target test file, list every xfail entry with its test name and reason. Read the actual conformance test file(s) (under `internal/conformance/oils/` or `internal/conformance/testdata/oils/`) to understand what each test expects — this context is essential for writing good root-cause hints.

### 4. Auto-split into batches

The ideal batch size is **5-10 xfails per prompt**. If a category has more than 10 xfails, split it into coherent sub-batches based on shared root causes or thematic grouping. Analyze test names, reasons, and expected behavior to find natural clusters.

Examples of good splits:
- By sub-feature: `$_` tracking (9 cases) vs built-in variables like `$RANDOM`, `$LINENO` (13 cases)
- By root cause: "od formatting" (9 echo cases sharing one cause) vs "typed-arg error" (1 unrelated echo case)
- By test file when files are small: `empty-bodies.test.sh` (2) + `func-parsing.test.sh` (2) bundled together

Present the proposed split to the user before generating prompts, so they can adjust.

### 5. Generate the prompt

Each prompt follows this structure:

```
**Prompt [N]: close `<test-file>` [subset description] xfails ([count] cases)**

[One sentence describing what this batch covers.]

[Fix the N xfail entries in `internal/conformance/manifest.json` from/whose keys begin with `oils/<file>`.]

The conformance test file is at `internal/conformance/[oils or testdata/oils]/<file>`. Run with:

\```sh
make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/<file>'
\```

The [N] cases [to fix / are]:

**[Sub-group heading if applicable] ([count]):**
1. `<test name>` -- [what this test checks and what the expected behavior is]
2. ...

[Root cause analysis paragraph: where in the codebase the issue likely lives,
what code paths are involved, and a theory for why it fails. Reference specific
directories like `internal/shell/syntax/`, `internal/shell/expand/`,
`internal/shell/interp/`, or `internal/builtins/` as appropriate.]

Fix each case, remove its xfail entry from `manifest.json`, and verify with
`make conformance-test`. Run `make lint` before finishing.

[Scope guard if this is a sub-batch: "Do NOT touch the remaining N xfails in
<file> -- only these N." or "Do NOT touch any other xfails -- only these N."]
```

### Writing good root-cause hints

Root-cause hints are the most valuable part of the prompt — they save the agent from aimless exploration. To write them well:

- Read the test file to understand expected stdout, stderr, and exit codes
- **Compare gbash vs nix bash** for each xfail case. Run both and show the concrete diff. Use the pinned nix bash (`$(./scripts/ensure-bash.sh)`) not the system bash, since the conformance suite compares against that specific version. Example:
  ```sh
  NIX_BASH=$(./scripts/ensure-bash.sh)
  go run ./cmd/gbash -c 'echo test' 2>&1
  $NIX_BASH -c 'echo test' 2>&1
  ```
- Note patterns: do all failing tests share a code path (e.g. all involve `${var@op}`)? Do the reasons mention a specific gap?
- Reference the architecture layers: parser issues are in `internal/shell/syntax/`, expansion in `internal/shell/expand/`, runtime behavior in `internal/shell/interp/`, builtin commands in `internal/builtins/`
- Be specific: "the parser's `((` / `$((` lookahead" is better than "the parser"
- Suggest a theory: "the fix likely involves backtracking when the initial arith parse fails" gives the agent a starting hypothesis to validate or discard

### 6. Present the summary table

After all prompts, include a summary table:

```
| Prompt | Scope | xfails |
|---|---|---:|
| 1 | description | N |
| 2 | description | N |
| | **Total** | **N** |
```

## Key paths

- Manifest: `internal/conformance/manifest.json`
- Xfail query script: `.claude/skills/conformance-prompt/scripts/xfails.py`
- Test files: `internal/conformance/oils/` and `internal/conformance/testdata/oils/`
- Run one test: `make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/<file>'`
- Lint: `make lint`
- Nix bash: `$(./scripts/ensure-bash.sh)` -- use this pinned bash 5.3 for comparisons, not the system bash

## Things to avoid

- Don't generate prompts without reading the actual test files — generic prompts without root-cause hints are significantly less useful
- Don't put more than ~10 xfails in a single prompt — agents lose focus on larger batches
- Don't mix unrelated root causes in the same prompt — a prompt about `od` formatting shouldn't also include a parser error case just because they're in the same test file
- Don't forget scope guards — every sub-batch prompt should explicitly say what NOT to touch
