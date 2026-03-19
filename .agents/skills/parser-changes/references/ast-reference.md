# AST Node Reference

Complete reference for gbash AST node types. Read this when you need to understand node structure for parsing or walking the AST.

## Core Interfaces

### Node
Base interface for all AST nodes:
```go
type Node interface {
    Pos() Pos  // Position of first character
    End() Pos  // Position after last character
}
```

### Command
Interface for all command types (simple commands, compound commands, function declarations):
```go
type Command interface {
    Node
    commandNode()
}
```

Implementations: `*CallExpr`, `*IfClause`, `*WhileClause`, `*ForClause`, `*CaseClause`, `*Block`, `*Subshell`, `*BinaryCmd`, `*FuncDecl`, `*ArithmCmd`, `*TestClause`, `*DeclClause`, `*LetClause`, `*TimeClause`, `*CoprocClause`, `*TestDecl`

### WordPart
Interface for components that can appear inside a Word:
```go
type WordPart interface {
    Node
    wordPartNode()
}
```

Implementations: `*Lit`, `*SglQuoted`, `*DblQuoted`, `*ParamExp`, `*CmdSubst`, `*ArithmExp`, `*ProcSubst`, `*ExtGlob`, `*BraceExp`

### ArithmExpr
Interface for arithmetic expression nodes:
```go
type ArithmExpr interface {
    Node
    arithmExprNode()
}
```

Implementations: `*BinaryArithm`, `*UnaryArithm`, `*ParenArithm`, `*FlagsArithm`, `*Word`

### TestExpr
Interface for test expression nodes (used by `[ ]` test commands):
```go
type TestExpr interface {
    Node
    testExprNode()
}
```

Implementations: `*BinaryTest`, `*UnaryTest`, `*ParenTest`, `*Word`

### CondExpr
Interface for conditional expression nodes inside `[[ ]]`:
```go
type CondExpr interface {
    Node
    condExprNode()
}
```

Implementations: `*CondBinary`, `*CondUnary`, `*CondParen`, `*CondWord`, `*CondVarRef`, `*CondPattern`, `*CondRegex`

## Top-Level Nodes

### File
```go
type File struct {
    Name  string     // Source file name
    Stmts []*Stmt    // Statements in the file
    Last  []Comment  // Trailing comments
}
```

### Stmt (Statement)
Wrapper for a command with metadata:
```go
type Stmt struct {
    Comments   []Comment
    Cmd        Command     // The actual command (can be nil for redirects-only)
    Position   Pos
    Semicolon  Pos         // Position of ';', '&', or '|&'
    Negated    bool        // ! stmt
    Background bool        // stmt &
    Coprocess  bool        // |&
    Disown     bool        // &| or &!
    Redirs     []*Redirect // stmt >a <b
}
```

### VarRef
Canonical reference to a shell variable or element:
```go
type VarRef struct {
    Name  *Lit
    Index *Subscript // nil, [i], ["k"], [@], [*]
}
```

Used by:
- `Assign.Ref`
- nameref resolution helpers in `expand` and `interp`
- dynamic lvalue consumers like `printf -v`, `test -v`, and `[[ -v ]]`

There is also a dedicated parser entrypoint for this shape:
```go
func (p *Parser) VarRef(r io.Reader) (*VarRef, error)
```

### Subscript
Bracketed selector shared by variable refs, parameter expansions, and array literals:
```go
type SubscriptKind uint8

const (
    SubscriptExpr SubscriptKind = iota
    SubscriptAt
    SubscriptStar
)

type Subscript struct {
    Left, Right Pos
    Kind        SubscriptKind
    Expr        ArithmExpr
}
```

Important distinction:
- `Kind == SubscriptExpr` means the selector is represented by `Expr`
- `Kind == SubscriptAt` means `[@]`
- `Kind == SubscriptStar` means `[*]`

`[@]` and `[*]` are distinguished at parse time via `Kind`, not inferred from a generic word literal.

## Command Nodes

### CallExpr (Simple Command)
```go
type CallExpr struct {
    Assigns []*Assign  // VAR=val before command
    Args    []*Word    // Command and arguments
}
```
If `Args` is empty, `Assigns` apply to shell environment. Otherwise, they're command-local.

### BinaryCmd
```go
type BinaryCmd struct {
    OpPos Pos
    Op    BinCmdOperator  // &&, ||, |, |&
    X, Y  *Stmt
}
```

### IfClause
```go
type IfClause struct {
    Position Pos      // "if", "elif", or "else"
    ThenPos  Pos      // "then" position
    FiPos    Pos      // "fi" position
    Cond     []*Stmt
    CondLast []Comment
    Then     []*Stmt
    ThenLast []Comment
    Else     *IfClause  // elif or else (recursive)
    Last     []Comment
}
```

### WhileClause
```go
type WhileClause struct {
    WhilePos, DoPos, DonePos Pos
    Until                    bool  // true for "until", false for "while"
    Cond     []*Stmt
    CondLast []Comment
    Do       []*Stmt
    DoLast   []Comment
}
```

### ForClause
```go
type ForClause struct {
    ForPos, DoPos, DonePos Pos
    Select                 bool  // true for "select", false for "for"
    Braces                 bool  // deprecated { } form
    Loop                   Loop  // WordIter or CStyleLoop
    Do     []*Stmt
    DoLast []Comment
}
```

### Loop Types
```go
// for x in a b c
type WordIter struct {
    Name  *Lit
    InPos Pos      // "in" position (invalid if missing)
    Items []*Word
}

// for ((i=0; i<10; i++))
type CStyleLoop struct {
    Lparen, Rparen Pos
    Init, Cond, Post ArithmExpr  // Each can be nil
}
```

### CaseClause
```go
type CaseClause struct {
    Case, In, Esac Pos
    Braces         bool  // deprecated { } form
    Word           *Word
    Items          []*CaseItem
    Last           []Comment
}

type CaseItem struct {
    Op       CaseOperator  // ;;, ;&, ;;&, ;|
    OpPos    Pos
    Comments []Comment
    Patterns []*Word
    Stmts    []*Stmt
    Last     []Comment
}
```

### Block and Subshell
```go
type Block struct {    // { stmts; }
    Lbrace, Rbrace Pos
    Stmts []*Stmt
    Last  []Comment
}

type Subshell struct {  // ( stmts )
    Lparen, Rparen Pos
    Stmts []*Stmt
    Last  []Comment
}
```

### ArithmCmd
```go
type ArithmCmd struct {  // (( expr ))
    Left, Right Pos
    Unsigned    bool  // ((# expr))
    X           ArithmExpr
}
```

### TestClause
```go
type TestClause struct {  // [[ expr ]]
    Left, Right Pos
    X           CondExpr
}
```

`CondExpr` types provide typed operand wrappers that distinguish patterns, regexes, and variable references at parse time.

### FuncDecl
```go
type FuncDecl struct {
    Position Pos
    RsrvWord bool   // "function f" style
    Parens   bool   // has () parentheses
    Name     *Lit   // Function name
    Names    []*Lit // Multiple names
    Body     *Stmt
}
```

### DeclClause
```go
type DeclClause struct {
    Variant *Lit      // "declare", "local", "export", "readonly", "typeset", "nameref"
    Args    []*Assign // Mix of assignments and options
}
```

## Word and WordPart Nodes

### Word
```go
type Word struct {
    Parts []WordPart
}

// Helper method
func (w *Word) Lit() string  // Returns literal string if all parts are *Lit
```

### Lit (Literal)
```go
type Lit struct {
    ValuePos, ValueEnd Pos
    Value              string
}
```

### SglQuoted
```go
type SglQuoted struct {
    Left, Right Pos
    Dollar      bool   // $'' (ANSI-C quotes)
    Value       string
}
```

### DblQuoted
```go
type DblQuoted struct {
    Left, Right Pos
    Dollar      bool        // $"" form
    Parts       []WordPart  // Can contain expansions
}
```

### ParamExp (Parameter Expansion)
```go
type ParamExp struct {
    Dollar, Rbrace Pos
    Short          bool  // $a vs ${a}

    // Prefix operators (only one set)
    Excl   bool  // ${!a}
    Length bool  // ${#a}
    Width  bool  // ${%a}
    IsSet  bool  // ${+a}

    Param       *Lit
    NestedParam WordPart     // nested expansion
    Index       *Subscript   // ${a[i]}, ${a[@]}, ${a[*]}

    // Expansion operations (only one set)
    Slice     *Slice          // ${a:x:y}
    Repl      *Replace        // ${a/x/y}
    Names     ParNamesOperator // ${!prefix*}
    Exp       *Expansion      // ${a:-b}, ${a#b}, etc.
    Modifiers []*Lit          // ${a:h2}
}

type Slice struct {
    Offset, Length ArithmExpr
}

type Replace struct {
    All        bool
    Orig, With *Word
}

type Expansion struct {
    Op   ParExpOperator  // +, :+, -, :-, ?, :?, =, :=, %, %%, #, ##, etc.
    Word *Word
}
```

Notes:
- `ParamExp.Index.Expr` may still be an arithmetic-looking `*Word`, a string-like word, or a zsh-specific expression such as a comma slice.
- `ParamExp.Index.Kind` tells you whether the selector was a generic expression, `[@]`, or `[*]`.

### CmdSubst
```go
type CmdSubst struct {
    Left, Right Pos
    Stmts       []*Stmt
    Last        []Comment
    Backquotes  bool  // deprecated `cmd` form
    TempFile    bool  // ${ cmd;}
    ReplyVar    bool  // ${|cmd;}
}
```

### ArithmExp
```go
type ArithmExp struct {  // $((expr)) or $[expr]
    Left, Right Pos
    Bracket     bool  // deprecated $[expr] form
    Unsigned    bool  // mksh $((# expr))
    X           ArithmExpr
}
```

### ProcSubst
```go
type ProcSubst struct {
    OpPos, Rparen Pos
    Op            ProcOperator  // <( or >(
    Stmts         []*Stmt
    Last          []Comment
}
```

### ExtGlob
```go
type ExtGlob struct {
    OpPos   Pos
    Op      GlobOperator  // ?(, *(, +(, @(, !(
    Pattern *Lit
}
```

### BraceExp
```go
type BraceExp struct {  // {a,b} or {1..10}
    Sequence bool  // {x..y} vs {x,y}
    Elems    []*Word
}
```

## Arithmetic Expression Nodes

### BinaryArithm
```go
type BinaryArithm struct {
    OpPos Pos
    Op    BinAritOperator  // +, -, *, /, %, **, ==, <, >, etc.
    X, Y  ArithmExpr
}
```

For ternary `a ? b : c`:
- `Op == TernQuest`
- `Y` is a `*BinaryArithm` with `Op == TernColon`

### UnaryArithm
```go
type UnaryArithm struct {
    OpPos Pos
    Op    UnAritOperator  // !, ~, ++, --, +, -
    Post  bool            // true for x++, false for ++x
    X     ArithmExpr
}
```

### ParenArithm
```go
type ParenArithm struct {
    Lparen, Rparen Pos
    X              ArithmExpr
}
```

## Test Expression Nodes

### BinaryTest
```go
type BinaryTest struct {
    OpPos Pos
    Op    BinTestOperator  // =~, -nt, -ot, -ef, -eq, -ne, etc.
    X, Y  TestExpr
}
```

### UnaryTest
```go
type UnaryTest struct {
    OpPos Pos
    Op    UnTestOperator  // -e, -f, -d, -z, -n, !, etc.
    X     TestExpr
}
```

### ParenTest
```go
type ParenTest struct {
    Lparen, Rparen Pos
    X              TestExpr
}
```

## Conditional Expression Nodes

These nodes implement the `CondExpr` interface and are used inside `[[ ... ]]` conditionals (via `TestClause.X`). They carry typed operand wrappers that distinguish patterns, regexes, and variable references at parse time.

### CondExpr
```go
type CondExpr interface {
    Node
    condExprNode()
}
```

Implementations: `*CondBinary`, `*CondUnary`, `*CondParen`, `*CondWord`, `*CondVarRef`, `*CondPattern`, `*CondRegex`

### CondBinary
```go
type CondBinary struct {
    OpPos Pos
    Op    BinTestOperator  // &&, ||, -eq, -ne, -lt, -gt, =~, ==, !=, etc.
    X, Y  CondExpr
}
```

### CondUnary
```go
type CondUnary struct {
    OpPos Pos
    Op    UnTestOperator  // -e, -f, -d, -z, -n, !, -v, -R, etc.
    X     CondExpr
}
```

### CondParen
```go
type CondParen struct {
    Lparen, Rparen Pos
    X              CondExpr
}
```

### CondWord
Generic word operand (default wrapper):
```go
type CondWord struct {
    Word *Word
}
```

### CondVarRef
Variable reference operand for `-v` and `-R` tests:
```go
type CondVarRef struct {
    Ref *VarRef
}
```

### CondPattern
Pattern-matching operand for `==`, `=`, and `!=`:
```go
type CondPattern struct {
    Word *Word
}
```

### CondRegex
Regular-expression operand for `=~`:
```go
type CondRegex struct {
    Word *Word
}
```

## Other Nodes

### Assign
```go
type Assign struct {
    Append bool
    Naked  bool
    Ref    *VarRef    // nil only for dynamic naked decl operands
    Value  *Word
    Array  *ArrayExpr
}
```

`Assign.Ref` holds assignment targets. When changing assignment target syntax, follow `Assign.Ref` through `syntax`, `expand`, and `interp`.

### ArrayExpr
```go
type ArrayExpr struct {
    Lparen, Rparen Pos
    Elems          []*ArrayElem
    Last           []Comment
}

type ArrayElem struct {
    Index    *Subscript // Can be nil
    Value    *Word      // Can be nil
    Comments []Comment
}
```

Notes:
- `ArrayElem.Index.Kind` distinguishes `[expr]` from `[@]` / `[*]`.
- Runtime still decides how to interpret `SubscriptExpr` based on context; the AST now preserves selector shape without reparsing it from a bare arithmetic node.

### Redirect
```go
type Redirect struct {
    OpPos Pos
    Op    RedirOperator  // >, >>, <, <>, etc.
    N     *Lit           // fd or {varname}
    Word  *Word          // Target
    Hdoc  *Word          // Here-doc body
}
```

## Walking the AST

Use `syntax.Walk` for traversal:
```go
syntax.Walk(file, func(node syntax.Node) bool {
    switch n := node.(type) {
    case *syntax.CallExpr:
        // Handle simple commands
    case *syntax.ParamExp:
        // Handle parameter expansions
    }
    return true  // Continue walking children
})
```

Return `false` to skip children of the current node.
