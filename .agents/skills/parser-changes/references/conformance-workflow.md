# Conformance Testing Workflow

Detailed guide for working with gbash conformance tests.

## Test Infrastructure Overview

```
internal/conformance/
├── oils/                    # Test corpus (from OILS project)
│   ├── arith.test.sh
│   ├── var-sub.test.sh
│   └── ...
├── bin/                     # Helper scripts for tests
├── fixtures/                # Test fixtures (files, directories)
├── manifest.json            # Skip/xfail configuration
├── parser.go               # Test file parser
├── runner.go               # Test execution engine
└── suites_test.go          # Go test entry point
```

## Running Tests

### Environment Variable

Tests require `GBASH_RUN_CONFORMANCE=1`. The Makefile sets this automatically.

### Full Suite
```bash
make conformance-test
```

### Single Test File
```bash
make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/arith.test.sh'
```

### Single Test Case
Test path format: `TestConformance/{suite}/{file}/{case_name}`

Case names have spaces replaced with underscores:
```bash
# For case "#### Add one to var"
make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/arith.test.sh/Add_one_to_var'
```

### Regex Matching
```bash
# All cases containing "array"
make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/array.test.sh/.*array.*'

# All arith-related files
make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/arith.*'
```

### Verbose Output
Add `-v` for more detail:
```bash
GBASH_RUN_CONFORMANCE=1 go test ./internal/conformance -run 'TestConformance/bash/oils/arith.test.sh/Add_one_to_var' -v
```

## Test File Format

### Case Structure
```bash
#### Case Name
# Comments about the test
code_to_run
## stdout: expected stdout
## stderr: expected stderr
## status: expected exit code
```

### Annotations

**Expected output:**
- `## stdout: text` - Expected stdout (single line)
- `## stdout-json: "text\n"` - Expected stdout (JSON-encoded)
- `## stderr: text` - Expected stderr
- `## status: N` - Expected exit status

**Shell-specific behavior:**
- `## N-I bash` - Not Implemented in bash (skips for bash oracle)
- `## OK bash/zsh` - Alternative expected behavior
- `## BUG bash` - Known bash bug, alternative expectation

### Example
```bash
#### Constant with quotes like '1'
echo $(('1' + 2))
## status: 0
## N-I bash/zsh status: 1
## N-I dash status: 2
```

This test expects:
- Default: status 0
- bash, zsh: status 1 (not implemented)
- dash: status 2

## Manifest Configuration

`manifest.json` structure:
```json
{
  "suites": {
    "bash": {
      "entries": {
        "oils/file.test.sh": {
          "mode": "skip|xfail",
          "reason": "explanation"
        },
        "oils/file.test.sh::Case Name": {
          "mode": "xfail",
          "reason": "specific case reason"
        }
      }
    },
    "posix": {
      "entries": { ... }
    }
  }
}
```

### Entry Modes

- **skip**: Don't run the test at all
- **xfail**: Expect failure (test runs, failure is OK)

### Entry Scopes

1. **File-level**: Applies to all cases in the file
   ```json
   "oils/arith.test.sh": { "mode": "xfail", "reason": "..." }
   ```

2. **Case-level**: Applies to specific case (overrides file-level)
   ```json
   "oils/arith.test.sh::Constant with quotes like '1'": { "mode": "xfail", "reason": "..." }
   ```

### Good Reasons

Write specific, actionable reasons:

Bad:
```json
"reason": "doesn't work"
```

Good:
```json
"reason": "gbash arithmetic evaluates single-quoted strings as 0 instead of erroring"
```

Better (links to issue):
```json
"reason": "gbash does not error on single-quoted strings in arithmetic (#123)"
```

## Workflow: Fixing a Conformance Issue

### 1. Identify the Failing Test

Run the test to see the diff:
```bash
make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/arith.test.sh/Your_Case'
```

Output shows:
```
--- FAIL: TestConformance/bash/oils/arith.test.sh/Your_Case
    gbash:
        stdout: "5"
        status: 0
    bash:
        stdout: ""
        stderr: "bash: ((: '1': syntax error..."
        status: 1
```

### 2. Reproduce in Bash

```bash
# Test the exact code
bash -c "echo \$(('1' + 2))"
echo "status: $?"
```

### 3. Identify the Layer

Use the decision tree from SKILL.md:
- Parse issue? Check if `syntax.NewParser().Parse()` handles it correctly
- Expansion issue? Check `expand.Arithm()` or `expand.Literal()`
- Interp issue? Check `runner.go` execution path

### 4. Write a Unit Test

**For expand issues** (`expand/arith_test.go`):
```go
func TestArithmSingleQuoteRejection(t *testing.T) {
    tests := []struct {
        name    string
        src     string
        wantErr bool
    }{
        {"single quoted", "'1'", true},
        {"plain number", "42", false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            expr := parseArithmExpr(t, tt.src)
            _, err := Arithm(&Config{}, expr)
            if (err != nil) != tt.wantErr {
                t.Errorf("got err=%v, wantErr=%v", err, tt.wantErr)
            }
        })
    }
}
```

**For interp issues** (`runtime/runtime_test.go` or similar):
```go
func TestArithmErrorMessage(t *testing.T) {
    result := runScript(t, `((x='1'))`)
    if result.ExitCode != 1 {
        t.Errorf("expected exit 1, got %d", result.ExitCode)
    }
    if !strings.Contains(result.Stderr, "syntax error") {
        t.Errorf("expected syntax error in stderr: %s", result.Stderr)
    }
}
```

### 5. Implement the Fix

Make the minimal change needed. See patterns in SKILL.md.

### 6. Verify

Run the unit test:
```bash
go test ./internal/shell/expand -run TestArithmSingleQuote -v
```

Run the conformance test:
```bash
make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/arith.test.sh/Your_Case'
```

### 7. Update Manifest

If behavior now matches bash exactly:
- Remove the xfail entry

If behavior matches but with minor differences:
- Update the reason to explain the acceptable difference
```json
"reason": "gbash error message matches bash semantics but differs in whitespace handling"
```

If one case in a file is fixed but others aren't:
- Add a case-level entry for the remaining failures
- Remove the file-level xfail

## Common Test Categories

### Arithmetic (`arith*.test.sh`)
- Basic arithmetic: `$((1+2))`
- Variables in arithmetic: `$((x + 1))`
- Assignments: `((x = 1))`, `((x += 1))`
- Errors: division by zero, invalid operands

### Parameter Expansion (`var-*.test.sh`)
- Basic: `$var`, `${var}`
- Default values: `${var:-default}`
- String operations: `${var:0:3}`, `${var#pattern}`
- Arrays: `${arr[0]}`, `${#arr[@]}`

### Control Flow (`if_*.test.sh`, `loop.test.sh`, `case_*.test.sh`)
- If/elif/else
- While/until
- For loops (word iteration and C-style)
- Case statements

### Quoting (`quote.test.sh`)
- Single quotes: `'literal'`
- Double quotes: `"with $expansion"`
- ANSI-C quotes: `$'escape\n'`
- Backslash escaping

### Redirects (`redirect*.test.sh`)
- File redirects: `> file`, `>> file`, `< file`
- Here-docs: `<< EOF`
- Here-strings: `<<< "string"`
- Descriptor manipulation: `2>&1`

## Tips

1. **Start with the simplest failing case** - Don't try to fix everything at once

2. **Check if similar tests exist** - Search for related cases that might have the same root cause

3. **Bisect large failures** - If a whole file fails, identify which specific constructs cause issues

4. **Mind the oracle** - The "bash" suite compares against GNU bash; the "posix" suite uses `bash --posix`

5. **Check bash version** - Conformance uses a pinned bash version via Nix. Behavior may differ from your system bash.

6. **Use verbose mode** - `go test -v` shows actual vs expected output inline

7. **Read the test comments** - Many test cases have explanatory comments about edge cases
