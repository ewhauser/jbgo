---
name: parser-changes
description: Guide for making parser, AST, and shell expansion changes in gbash. Use when modifying internal/shell/syntax, internal/shell/expand, or internal/shell/interp to fix conformance issues, add shell features, or correct parsing behavior. Trigger on phrases like "parser change", "conformance fix", "AST", "shell syntax", "arithmetic expression", "word expansion", or any work touching the internal/shell/ tree.
---

# Parser Changes in gbash

This skill guides you through making changes to gbash's shell parsing, AST, and expansion layers. The goal is to avoid hacky workarounds and build proper understanding of where changes belong.

## Architecture Mental Model

The shell core has three layers that process shell code in sequence:

```
Script text -> [syntax] -> AST -> [expand] -> Values -> [interp] -> Execution
               (parsing)         (expansion)            (running)
```

**syntax** (`internal/shell/syntax/`): Parses shell text into an AST. Handles lexing, tokenization, and building node trees. This is where you add new syntax constructs or fix parsing bugs.

**expand** (`internal/shell/expand/`): Evaluates parameter expansion, command substitution, arithmetic, globbing, and word splitting. This is where you fix how `${var}`, `$(cmd)`, `$((expr))`, and patterns behave.

**interp** (`internal/shell/interp/`): Executes the AST by walking nodes, managing shell state, and dispatching commands. This is where control flow, redirects, and command execution live.

## Decision Tree: Where Does My Change Go?

Ask these questions in order:

1. **Is the shell rejecting valid syntax or accepting invalid syntax?**
   - YES -> Change goes in `syntax/parser.go` or `syntax/lexer.go`
   - Example: bash accepts `((x='1'))` but gbash doesn't parse it

2. **Is the syntax parsing correctly but expanding to wrong values?**
   - YES -> Change goes in `expand/`
   - Example: `${var:0:3}` returns wrong substring, `$((1+2))` returns wrong result

3. **Is expansion correct but execution behavior is wrong?**
   - YES -> Change goes in `interp/runner.go`
   - Example: exit codes wrong, redirects not working, control flow broken

4. **Is the error message format different from bash?**
   - Often lives in `expand/` for arithmetic errors or `interp/` for command errors
   - Check where the error originates and match bash's exact format

## Key AST Node Types

Read `internal/shell/syntax/nodes.go` for complete definitions. Here's the hierarchy:

```
Node (base interface)
├── File           # Top-level: contains Stmts
├── Stmt           # Statement wrapper: holds Command + Redirs + flags
├── VarRef         # Canonical variable or element reference
├── Subscript      # Bracketed selector like [i], [@], [*]
├── DeclOperand    # Typed declaration operand
│   ├── DeclFlag
│   ├── DeclName
│   ├── DeclAssign
│   └── DeclDynamicWord
├── Command (interface)
│   ├── CallExpr   # Simple command: cmd arg1 arg2
│   ├── BinaryCmd  # cmd1 && cmd2, cmd1 || cmd2, cmd1 | cmd2
│   ├── IfClause, WhileClause, ForClause, CaseClause
│   ├── Block      # { stmts; }
│   ├── Subshell   # ( stmts )
│   ├── ArithmCmd  # (( expr ))
│   ├── TestClause # [[ expr ]]
│   └── FuncDecl, DeclClause, LetClause, etc.
├── Word           # A shell word, contains WordParts
├── WordPart (interface)
│   ├── Lit        # Literal string
│   ├── SglQuoted  # 'text' or $'text'
│   ├── DblQuoted  # "text with $expansions"
│   ├── ParamExp   # ${var}, ${var:-default}, etc.
│   ├── CmdSubst   # $(cmd) or `cmd`
│   ├── ArithmExp  # $((expr))
│   └── ProcSubst, ExtGlob, BraceExp
└── ArithmExpr (interface)
    ├── Word           # Variable or literal in arithmetic
    ├── BinaryArithm   # x + y, x = y, x ? y : z
    ├── UnaryArithm    # ++x, x--, !x, ~x
    └── ParenArithm    # (expr)
```

Two recent AST shifts matter for parser work:

- `Assign` no longer stores `Name` and `Index` separately. Assignment targets now flow through `Assign.Ref *VarRef`.
- Bracketed selectors are no longer just raw `ArithmExpr`s. `VarRef.Index`, `ParamExp.Index`, and `ArrayElem.Index` now use `*Subscript`, with `Kind` distinguishing generic expression subscripts from `[@]` and `[*]`.
- `DeclClause` no longer uses `Args []*Assign`. Declaration builtins now carry `Operands []DeclOperand`, and `Assign.Naked` is gone.

When you touch variable references, array indexing, declaration builtins, namerefs, `printf -v`, `test -v`, `${var[...]}`, or compound array literals, you should expect follow-on edits in all three layers:

- `syntax`: `nodes.go`, `parser.go`, `walk.go`, `printer.go`, `simplify.go`, typedjson, and filetests
- `expand`: `param.go` and any helpers that interpret references or selectors
- `interp`: `runner.go`, `varref.go`, `vars.go`, and builtin/test consumers

For declaration-specific work, the intended flow is now:

- parse source operands directly into `DeclFlag`, `DeclName`, `DeclAssign`, or `DeclDynamicWord`
- when a dynamic declaration word expands into runtime fields, reclassify each field through the syntax-level declaration operand parser
- do not reconstruct declaration semantics with fake `Assign`s or `strings.Cut(..., \"=\")`-style splitting

## Conformance Testing

Conformance tests compare gbash against pinned bash. The test corpus lives in `internal/conformance/oils/`.

### Running Tests

**Full suite** (requires `GBASH_RUN_CONFORMANCE=1`):
```bash
make conformance-test
```

**Single test file:**
```bash
make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/arith.test.sh'
```

**Single test case within a file:**
```bash
# Format: TestConformance/{suite}/{file}/{case_name}
# Spaces in case names become underscores, :: becomes /
make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/arith.test.sh/Add_one_to_var'
```

**Use regex for partial matches:**
```bash
# Run all cases containing "arith" in arith.test.sh
make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/arith.test.sh/.*arith.*'
```

### Test File Format

Test files use `#### Case Name` markers to separate cases:

```bash
#### Add one to var
i=1
echo $(($i+1))
## stdout: 2

#### $ is optional
i=1
echo $((i+1))
## stdout: 2
```

Annotations like `## stdout:`, `## status:` control expected output.

### Manifest Structure

`internal/conformance/manifest.json` tracks known differences:

```json
{
  "suites": {
    "bash": {
      "entries": {
        "oils/arith.test.sh": {
          "mode": "xfail",
          "reason": "known gbash divergence"
        },
        "oils/arith.test.sh::Constant with quotes like '1'": {
          "mode": "xfail",
          "reason": "specific case reason"
        }
      }
    }
  }
}
```

- **File-level entries**: `"oils/file.test.sh"`
- **Case-level entries**: `"oils/file.test.sh::Case Name"`
- **Modes**: `skip` (don't run), `xfail` (expect failure)

### Workflow for Fixing a Conformance Failure

1. **Isolate the failing case:**
   ```bash
   make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/arith.test.sh/Your_Case_Name'
   ```

2. **See what bash does:**
   ```bash
   bash -c 'your_test_code_here'
   echo "exit: $?"
   ```

3. **Identify the layer** (parse, expand, or interp) using the decision tree above

4. **Write a unit test** in the appropriate package (`expand/arith_test.go`, etc.)

5. **Fix the issue** with minimal changes

6. **Update manifest.json:**
   - Remove xfail if behavior now matches bash
   - Update reason if behavior differs acceptably
   - Add case-level entry if only one case in a file is fixed

## Common Change Patterns

### Pattern 1: Bash rejects something gbash accepts

Recent example: bash rejects single-quoted strings in arithmetic like `((x='1'))`.

**Approach:**
1. Identify where the construct is evaluated (here: `expand/arith.go`)
2. Add detection logic for the invalid construct
3. Return a proper error type with bash-compatible message format
4. Update interp if the error needs context-aware formatting

```go
// In expand/arith.go - detect invalid construct
func hasSingleQuote(word *syntax.Word) *syntax.SglQuoted {
    for _, part := range word.Parts {
        if sq, ok := part.(*syntax.SglQuoted); ok {
            return sq
        }
    }
    return nil
}

// Return structured error
type ArithmSyntaxError struct {
    Expr  string
    Token string
}
```

### Pattern 2: Error message format differs from bash

Bash has specific error message formats. Match them exactly:
- Arithmetic: `((: expr: message`
- Command not found: `bash: cmd: command not found`
- Syntax errors: `bash: line N: message`

Check bash's actual output, then replicate the format.

### Pattern 3: Adding a new AST node field

1. Add field to struct in `syntax/nodes.go`
2. Update `Pos()` and `End()` methods if needed
3. Update `syntax/walk.go` to traverse the new field
4. Update `syntax/printer.go` if it affects printed output
5. Update parser in `syntax/parser.go` to populate the field

### Pattern 4: Fixing expansion behavior

1. Write a failing test in `expand/*_test.go`
2. Find the relevant expansion function in `expand/expand.go` or `expand/param.go`
3. Trace through with the AST node type
4. Fix the logic, matching bash behavior

### Pattern 5: Ref or subscript changes

If the change touches variable references or bracketed selectors:

1. Update the source-of-truth nodes in `syntax/nodes.go`
2. Update both parser entry points:
   - normal script parsing in `syntax/parser.go`
   - the dedicated `Parser.VarRef` entrypoint if ref syntax changed
3. Update printer, walker, simplify, typedjson, and filetests together
4. Migrate `expand` and `interp` off any raw-string or raw-`ArithmExpr` assumptions
5. Add parser tests for selector shape, especially `[@]`, `[*]`, and any malformed trailing-junk cases

## Reference Files

When you need deeper detail:

- **AST details**: Read `references/ast-reference.md` for complete node documentation
- **Conformance workflow**: Read `references/conformance-workflow.md` for test patterns
- **Token types**: Read `internal/shell/syntax/tokens.go` for all operators

## Anti-patterns to Avoid

1. **Don't hack around parsing issues in interp** - If syntax is wrong, fix syntax/
2. **Don't stringify and reparse** - Work with AST nodes directly
3. **Don't ignore error message format** - Bash's formats are specific, match them
4. **Don't skip conformance tests** - Add xfail with reason if truly different, but investigate first
5. **Don't add special cases in CallExpr** - Use proper AST node types for constructs

## Before Making Changes

1. Read the relevant test file in `internal/conformance/oils/` to understand expected behavior
2. Check `manifest.json` for known differences or skips
3. Run `bash -c 'your_test_code'` to see actual bash behavior
4. Write a unit test first
5. Make the minimal change needed
