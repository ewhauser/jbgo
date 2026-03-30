# AST Roadmap

## Summary

The current shell AST is not missing many top-level Bash constructs. `shell/syntax/nodes.go` already models most compound commands we care about: simple calls, `if`, `while`, `for`, `case`, blocks, subshells, functions, arithmetic commands, `[[ ]]`, declaration clauses, `let`, `time`, process substitution, arrays, and `coproc`.

The bigger issue is lower in the tree:

- too many Bash-specific contexts are flattened into generic `Word`, `ArithmExpr`, or "naked assignment" shapes
- several features are reparsed or reinterpreted later in `expand` or `interp`
- the AST often loses context that Bash uses to decide whether something is an arithmetic subscript, associative key, regex operand, declaration operand, alias-expanded token, or heredoc delimiter

This shows up in the conformance manifest as broad xfails around:

- aliases
- arithmetic contexts
- indexed and associative arrays
- assignment builtins and compound assignments
- `[[ ... ]]`, `=~`, and `-v`
- extglob and pattern-sensitive contexts
- heredocs

Not every xfail is an AST problem. But the AST and parser shape are currently a real bottleneck for closing the Bash conformance gap.

## Existing Coverage

High-level command coverage is mostly present today:

- `CallExpr`
- `IfClause`
- `WhileClause`
- `ForClause`
- `CaseClause`
- `Block`
- `Subshell`
- `BinaryCmd`
- `FuncDecl`
- `ArithmCmd`
- `TestClause`
- `DeclClause`
- `LetClause`
- `TimeClause`
- `CoprocClause`
- `ArrayExpr`
- `ProcSubst`

That means the roadmap should focus less on adding more top-level command nodes and more on improving the fidelity of lower-level syntax and operand modeling.

## Checklist

- [x] P0: Land `VarRef` / `Subscript` foundations for assignment targets and variable references
- [x] P0: Refactor `DeclClause` arguments into typed declaration operands
- [x] P0: Add a dedicated conditional AST for `[[ ... ]]` operands and operators
- [x] P1: Introduce a first-class pattern AST shared by extglob, `case`, `[[ == ]]`, and parameter pattern operators
- [x] P1: Add dedicated heredoc delimiter metadata instead of treating delimiters as generic words
- [x] P1: Move alias expansion earlier and preserve alias provenance in parse results
- [x] P1: Make compound array assignment semantics explicit in the AST
- [x] P2: Promote brace expansion from a post-parse rewrite to a stable syntax node
- [x] P2: Restrict function bodies to compound commands in the AST and validation layer
- [ ] P2: Revisit whether a standalone `LValue` node is still worth the churn now that `Assign.Ref` and `Append` are explicit

## Priority Order

Recommended implementation order:

1. `[[ ... ]]` conditional operands
2. pattern AST
3. heredoc delimiter metadata
4. alias-aware parse/provenance
5. compound assignment cleanup
6. brace expansion cleanup
7. function-body tightening (done)
8. standalone `LValue` re-evaluation only if write-target-specific metadata grows

This order should unlock the largest amount of conformance work without forcing repeated AST churn.

## Detailed Items

### 1. First-class `LValue` / `VarRef`

Status: landed `VarRef` foundation, standalone `LValue` deferred

#### Problem

Bash has a real concept of variable references and assignment targets that can appear in multiple contexts:

- plain scalar assignment
- indexed array element assignment
- associative array element assignment
- nameref targets
- `printf -v` targets
- `test -v` / `[[ -v ]]` targets
- indirect references like `${!ref}`

Today those cases are spread across parsing and runtime code, and many of them are reconstructed from strings later.

#### Current implementation signals

- `Assign` now uses `Ref *VarRef` plus `Append bool`
- `VarRef` is a first-class node reused by assignment parsing, runtime var-ref parsing, and nameref resolution
- `printf -v`, `test -v`, `[[ -v ]]`, and nameref code paths now share the same `VarRef` parser instead of ad hoc string splitting

#### Conformance signals

Relevant test families:

- `oils/array-assoc.test.sh`
- `oils/array-assign.test.sh`
- `oils/assign.test.sh`
- `oils/nameref.test.sh`
- `oils/builtin-printf.test.sh`
- `oils/dbracket.test.sh`

Concrete examples:

- `declare -n ref='A[$key]'`
- `printf -v 'assoc[$key]' '/%s/' val3`
- `test -v 'assoc[$key]'`
- `[[ -v assoc[$key] ]]`

#### Proposed AST change

Keep `VarRef` as the shared reference node. Defer a separate `LValue` wrapper unless we need write-target-specific metadata beyond:

- assignment append mode
- reference selectors
- write-context validation rules that cannot live on `Assign`

#### Why this matters

The important part was making references typed. A standalone `LValue` node is now a cleanup/refinement task, not the main conformance unlock.

### 2. Typed subscript AST

Status: landed

#### Problem

Bash has context-sensitive subscript behavior:

- indexed arrays use arithmetic semantics
- associative arrays use string-key semantics
- `@` and `*` selectors have special meaning
- `-v` tests have their own validation rules
- nested assignments in arithmetic-looking subscripts can have side effects

The current AST cannot preserve those distinctions.

#### Current implementation signals

- `VarRef.Index`, `ParamExp.Index`, and `ArrayElem.Index` now use `*Subscript`
- `Subscript.Kind` distinguishes generic expression selectors from `[@]` and `[*]`
- `Subscript.Mode` now carries indexed vs associative interpretation for generic selectors
- `VarRef.Context` now preserves `-v`-specific interpretation through parse and nameref resolution
- `expand.ResolveRef` is the single place that resolves `SubscriptAuto` after nameref following

#### Conformance signals

Relevant examples:

- `oils/array-assoc.test.sh`: associative keys that look arithmetic but are really strings
- `oils/array-assign.test.sh`: nested assignment side effects like `a[a[0]=1]=X`
- `oils/array-literal.test.sh`: indexed vs associative semantics inside compound assignments

#### Proposed AST change

Done. The shipped shape builds on the current `Subscript` node instead of replacing it:

- `Subscript.Mode` distinguishes `Auto`, indexed, and associative interpretation for generic selectors
- `VarRef.Context` distinguishes ordinary refs from `-v` refs
- declaration parsing and compound-array execution stamp explicit selector mode as soon as array mode is known
- nameref resolution preserves `VarRef.Context` and resolves `SubscriptAuto` once, centrally

Future subscript work, if needed, is narrower:

- side-effectful arithmetic subscripts
- slice-specific structure where generic `Expr` is still too coarse

#### Why this matters

Without this, array semantics continue to leak into runtime heuristics and re-parsing, which makes both conformance and diagnostics worse.

### 3. Typed declaration operands

Status: landed

#### Problem

Declaration builtins used to mix together:

- flags
- query modes like `-p` and `-f`
- bare names
- normal assignments
- dynamically-expanded declaration words

That is not a clean AST model. It is a parser convenience that pushes complexity into the interpreter.

#### Current implementation signals

- `DeclClause` now stores `Operands []DeclOperand`
- the parser now emits `DeclFlag`, `DeclName`, `DeclAssign`, and `DeclDynamicWord`
- dynamic declaration fields are reparsed through a syntax-level declaration-operand parser instead of `flattenAssigns` / `reparseCompoundAssign`
- declaration builtins are still detected from `CallExpr` in interp, but the normalization now preserves typed operands

#### Conformance signals

Relevant families:

- `oils/assign.test.sh`
- `oils/assign-extended.test.sh`
- `oils/assign-dialects.test.sh`
- `oils/builtin-meta-assign.test.sh`
- `oils/array-literal.test.sh`

Concrete examples:

- aliased `export` / `readonly`
- quoted dynamic declaration operands
- `declare -a 'var=(1 2 3)'`
- `declare -n ref='A[$key]'`

#### Proposed AST change

Done. The shipped shape is:

- `DeclFlag`
- `DeclName`
- `DeclAssign`
- `DeclDynamicWord`

#### Why this matters

Declaration builtins are a major Bash-specific semantic hotspot. They no longer depend on fake declaration-shaped assignments or compound-assignment reparsing.

### 4. Dedicated conditional AST for `[[ ... ]]`

Status: landed

#### Problem

The old conditional tree was:

- `BinaryTest`
- `UnaryTest`
- `ParenTest`
- `Word`

That is too generic for Bash conditionals, especially for:

- parser-owned `=~` operands
- `[[ -v ref[subscript] ]]`
- token-specific syntax errors
- parse distinctions between regex operators and ordinary shell operators

#### Current implementation signals

- `TestClause` now routes `[[ =~ ]]` through a parser-owned RHS scanner instead of flipping the lexer into a regex mode
- `CondRegex` still wraps `*Word`, so quoted and unquoted pieces are preserved without adding a regex-specific AST
- runtime still expands with `expand.Regexp` and compiles with Go `regexp`, with conditional-expression diagnostics owned in `interp`

#### Conformance signals

Relevant manifest entries:

- `bool-parse.test.sh::Not allowed: [[ ) ]] and [[ ( ]]`
- several `regex.test.sh` case-level xfails for bash diagnostic parity

Relevant files:

- `oils/bool-parse.test.sh`
- `oils/dbracket.test.sh`
- `oils/regex.test.sh`

#### Proposed AST change

Introduce typed conditional operands and condition nodes, for example:

- `CondWord`
- `CondVarRef`
- `CondPattern`
- `CondRegex`
- `CondBinary`
- `CondUnary`

In particular, `[[ -v ... ]]` should not take a generic `Word` operand. It should take a variable-reference-shaped node.

For `=~`, the RHS should preserve whether pieces were quoted or unquoted so runtime can apply Bash regex semantics more accurately. That does not require a custom regex engine; it only requires syntax to preserve the operand boundary and shell quoting/expansion structure.

#### Why this matters

`[[ ... ]]` is one of the clearest places where generic shell words are not sufficient as a semantic representation.

### 5. Shared pattern AST

Status: landed

#### Problem

Pattern-sensitive contexts are split across:

- extglob nodes
- plain literal words
- parameter pattern operations
- `case` pattern words
- `[[ == ]]` matching

The AST does not have a unified representation of Bash pattern language.

The old `ExtGlob` shape (`Op + Pattern *Lit`) was not enough for nested, adjacent, or quote-sensitive structure.

#### Landed AST shape

- `Pattern` and `PatternPart` model shell patterns directly
- `PatternPart` reuses shell word parts where possible and adds dedicated wildcard nodes:
  - `PatternAny`
  - `PatternSingle`
  - `PatternCharClass`
  - `PatternGroup`
  - `ExtGlob`
- `ExtGlob` now stores `Patterns []*Pattern`, which preserves nested and adjacent structure
- `PatternGroup` models bare grouped alternation such as `(foo|bar)` in zsh pattern contexts and bash-style recovery parses
- `CaseItem.Patterns` now uses `[]*Pattern`
- `CondPattern` now wraps `Pattern *Pattern`
- parameter pattern operators now use `Replace.Orig *Pattern` and `Expansion.Pattern *Pattern`
- `expand` and `interp` now render these pattern nodes from the shared AST before matching

#### Conformance signals

Relevant families:

- `oils/extglob-match.test.sh`
- `oils/extglob-files.test.sh`
- `oils/case_.test.sh`
- `oils/var-op-patsub.test.sh`
- `oils/var-op-strip.test.sh`

Concrete examples:

- adjacent extglobs
- nested extglobs
- no brace expansion inside `[[ == ]]`
- extglob in `case`

#### Implementation notes

The landed pattern tree represents:

- literals
- wildcards
- character classes
- bare grouped alternation via `PatternGroup.Patterns`
- extglob operators
- extglob alternation via `ExtGlob.Patterns`
- concatenation via `Pattern.Parts`
- quote boundaries where relevant

It is now reused in:

- `case` arms
- `[[ == ]]` and `[[ != ]]`
- parameter trim and substitution ops

Pathname globbing still consumes rendered pattern strings in `expand`; it does not yet have a dedicated top-level pattern operand node.

#### Why this matters

A shared pattern AST would reduce duplicated pattern handling and improve consistency across Bash contexts that use related but not identical matching rules.

### 6. Heredoc delimiter metadata

Status: landed

#### Problem

`Redirect` stores heredoc delimiters as generic `Word`. Bash cares about details that generic words do not surface cleanly:

- whether the delimiter was quoted
- whether the delimiter came from adjacent quoted pieces
- whether expansions were syntactically present but should be treated literally
- whether body expansion is enabled

#### Current implementation signals

- `Redirect.HdocDelim *HeredocDelim` now replaces `Redirect.Word` for `<<` and `<<-`
- `HeredocDelim` preserves original delimiter parts alongside cooked delimiter text
- parser stores whether quoting was present and whether body expansion remains enabled
- printer, walker, and typed JSON treat heredoc delimiters as first-class syntax nodes

#### Conformance signals

Relevant cases:

- `oils/here-doc.test.sh`
- case-specific manifest entries for heredoc delimiter behavior

Concrete examples:

- `cat <<${a}`
- `cat <<'EOF'"2"`
- malformed pipeline/heredoc interactions

#### Shipped AST change

`Redirect` now carries a dedicated `HeredocDelim` node for `<<` and `<<-` redirects. The node keeps:

- original delimiter parts for round-tripping and diagnostics
- cooked delimiter text after quote removal
- whether any quoting or escaping was present
- whether the body should be parsed with expansion enabled

The body remains a word-like tree, but the delimiter is no longer modeled as an ordinary shell word.

#### Why this matters

Heredocs are one of the most mode-sensitive pieces of shell syntax. Preserving delimiter intent explicitly should improve both conformance and diagnostics.

### 7. Alias-aware parse/provenance

Status: landed

#### Problem

Alias expansion currently happens in the interpreter by rewriting `CallExpr.Args` at runtime. That cannot fully match Bash because alias expansion affects parsing and assignment-word recognition.

#### Current implementation signals

- `syntax.Parser` now accepts an alias resolver via `ExpandAliases(...)` and expands unquoted command-position words before command parsing commits to grammar
- `syntax.File` and `syntax.Word` preserve alias provenance through `AliasExpansions []*AliasExpansion`
- the interpreter now stores alias values as raw shell source and feeds live alias state back into parser construction instead of reparsing alias values as argv at runtime
- top-level `shell.Run`, `source`, and `eval` now execute input as complete parsed chunks so `shopt -s expand_aliases` and alias definitions affect later commands in the same script

#### Conformance signals

Relevant families:

- `oils/alias.test.sh`
- alias/assignment interaction in `oils/assign.test.sh`

Concrete examples:

- alias with trailing space causing expansion of the next word
- aliased `export` / `readonly`
- same-line definition/use edge cases

#### Proposed change

- parse alias text as shell source early enough for aliases to introduce syntax like `{`, `(`, loops, pipelines, `&&`, and assignment-aware declaration builtins
- preserve per-word alias provenance so later passes can tell which tokens came from aliases
- move top-level execution from whole-script one-shot parsing to complete-command chunking so parser-visible shell state can evolve during the run

#### Why this matters

Alias semantics are sensitive to parse phase boundaries. Modeling them later in `interp` is structurally at odds with Bash behavior.

### 8. Explicit compound assignment semantics

Status: landed

#### Problem

`ArrayExpr` and `ArrayElem` are close, but they still leave too much to runtime inference:

- indexed vs associative mode
- sequential insertion semantics
- `[k]=v` vs implicit-value entries
- append-to-element behavior
- evaluation ordering

#### Landed AST shape

- `ArrayExpr.Mode` now distinguishes `Inherit`, `Indexed`, and `Associative`
- `ArrayElem.Kind` now distinguishes sequential, keyed, and keyed-append entries
- `DeclClause` parsing stamps explicit array mode on later operands after `-a` / `-A`
- `DeclDynamicWord` reparsing applies the same left-to-right declaration-mode stamping before execution

#### Conformance signals

Relevant families:

- `oils/array-literal.test.sh`
- `oils/array-assoc.test.sh`
- `oils/array-assign.test.sh`
- `oils/array-sparse.test.sh`

#### Landed execution model

- ordinary `a=(...)` keeps `ArrayExpr.Mode=Inherit` and resolves against declaration state or the existing variable kind
- sequential RHS values are expanded before mutating the target array
- elements are then applied in source order so later subscripts can observe earlier mutations
- associative sequential elements use key/value pairing, while keyed entries use literal-key writes or appends

#### Why this matters

Compound assignment is one of Bash's highest-density syntax/semantics areas. Making it explicit in the AST will simplify later execution logic.

### 9. Stable brace expansion node

Status: Done

`BraceExp` is now a stable parse result for ordinary shell words in Bash-like, mksh, and zsh modes. The parser builds brace structure while assembling word parts, preserving `{` and `}` positions on the node and leaving invalid or incomplete candidates as plain literal text.

Field-expanding contexts such as argv and loop word lists still expand parsed `BraceExp` nodes. Literalizing contexts such as assignments, here-doc/document rendering, redirects, arithmetic literal rendering, and parameter-operator arguments now stringify the node back to `{...}` instead of rejecting it or mutating the AST.

#### Implementation notes

- `syntax.Parse`, `WordsSeq`, and ordinary word parsing can emit `BraceExp`
- `expand.FieldsSeq` consumes parsed brace nodes directly
- non-field expansion paths preserve brace source text literally

#### Conformance signals

Relevant families:

- `oils/brace-expansion.test.sh`
- cases where brace expansion should be disabled in other contexts

#### Why this matters

Brace behavior is now visible in the AST without a post-parse rewrite, which makes parser provenance, printing, walking, typed JSON roundtrips, and downstream expansion behavior easier to reason about and debug.

### 10. Function bodies should be compound commands

Status: Done

`FuncDecl.Body` remains a `*Stmt`, but the parser now restricts function bodies to a single shell-command form with trailing redirections.

The parser now accepts:

- brace groups, subshells, loops, conditionals, `case`, `select`, `(( ))`, and `[[ ]]`
- trailing redirections after that body command

The parser now rejects:

- simple commands and quoted words as function bodies
- leading redirections before the body command
- `&&`, `||`, and pipeline operators being absorbed into the body instead of the enclosing statement

#### Conformance signals

Relevant families:

- `oils/func-parsing.test.sh`
- `oils/sh-func.test.sh`
- `oils/empty-bodies.test.sh`

This tightens correctness and reduces the number of parser states that the interpreter has to defend against.

## Implementation Notes

### Design principle

Prefer preserving Bash context in the AST over reconstructing it later from strings.

Bad signs in the current implementation:

- converting parsed words back into strings and reparsing them
- using the same node field for multiple incompatible semantic meanings
- relying on runtime heuristics to recover parse-time distinctions

### What not to over-rotate on

Do not add top-level nodes just for coverage counts. The high-level command set is already reasonably complete.

The highest-value work is:

- making variable references typed
- making subscripts typed
- making declaration operands typed
- making `[[ ... ]]` operands context-aware

### Suggested milestone breakdown

#### Milestone 1

- landed `VarRef`
- converted assignment targets to use it
- wired `test -v`, `[[ -v ]]`, `printf -v`, and nameref parsing to use it

#### Milestone 2

- landed `Subscript`
- distinguished `[@]` / `[*]` selectors
- left indexed vs associative runtime typing as follow-up work

#### Milestone 3

- landed typed declaration operands
- removed `flattenAssigns` / `reparseCompoundAssign` from declaration execution

#### Milestone 4

- add a better `[[ ... ]]` AST
- isolate regex and pattern-sensitive operands

#### Milestone 5

- unify pattern handling
- improve heredoc delimiter metadata
- revisit alias timing/provenance

## Success Criteria

The roadmap is working if we can progressively remove special cases like:

- runtime declaration-argument reparsing
- string-based reconstruction of array and nameref targets
- generic `Word` operands in `[[ -v ... ]]`
- regex operand boundary handling living in generic lexer state
- extglob nodes that immediately collapse back to strings

And if those changes let us retire xfails in the following clusters first:

- arrays
- assignments
- `[[ ... ]]`
- regex
- extglob
- heredocs
- aliases
