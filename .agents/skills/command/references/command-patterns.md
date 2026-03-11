# Command Implementation Patterns

## File structure

Every command lives in `commands/<name>.go` with this structure:

```go
package commands

import (
    "context"
    // other imports as needed
)

type <Name> struct{}

func New<Name>() *<Name> {
    return &<Name>{}
}

func (c *<Name>) Name() string {
    return "<name>"
}

func (c *<Name>) Run(ctx context.Context, inv *Invocation) error {
    // implementation
    return nil
}

var _ Command = (*<Name>)(nil)
```

## The Invocation struct

Commands receive everything through `*Invocation`:

| Field    | Type                | Purpose |
|----------|---------------------|---------|
| Args     | `[]string`          | Command arguments (flag and positional, no command name) |
| Env      | `map[string]string` | Environment variables |
| Dir      | `string`            | Current working directory |
| Stdin    | `io.Reader`         | Standard input |
| Stdout   | `io.Writer`         | Standard output |
| Stderr   | `io.Writer`         | Standard error |
| FS       | `jbfs.FileSystem`   | Sandboxed virtual filesystem — all file access must go through this |
| Net      | `network.Client`    | Network client (nil unless network enabled) |
| Policy   | `policy.Policy`     | Execution policy for enforcement checks |
| Trace    | `trace.Recorder`    | Trace recorder for structured events |
| Exec     | `func(...)`         | Nested shell execution (rarely needed) |

## Flag parsing

Parse flags manually by consuming from the front of `inv.Args`. The project doesn't use a flag-parsing library — this keeps commands self-contained and avoids global state.

**Simple flags** (like echo's `-n`):
```go
args := inv.Args
newline := true
for len(args) > 0 && args[0] == "-n" {
    newline = false
    args = args[1:]
}
```

**Flags with values** (like touch's `-d <date>`):
```go
for len(args) > 0 && strings.HasPrefix(args[0], "-") {
    switch {
    case args[0] == "-d" || args[0] == "--date":
        if len(args) < 2 {
            return exitf(inv, 1, "touch: option requires an argument -- d")
        }
        // use args[1]
        args = args[2:]
    case strings.HasPrefix(args[0], "--date="):
        // use value after =
        args = args[1:]
    default:
        return exitf(inv, 1, "touch: unsupported flag %s", args[0])
    }
}
```

**Combined short flags** (like wc's `-lwc`):
```go
if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
    for _, flag := range arg[1:] {
        switch flag {
        case 'l':
            opts.lines = true
        // ...
        default:
            return exitf(inv, 1, "cmd: unsupported flag -%c", flag)
        }
    }
}
```

**Options struct pattern** — for commands with many flags, define a separate options struct and parser function (see `wc.go` for a clean example):
```go
type cmdOptions struct {
    verbose bool
    count   int
}

func parseCmdArgs(inv *Invocation) (cmdOptions, []string, error) {
    // returns (options, remaining positional args, error)
}
```

## Error handling

Use `exitf()` to write to stderr and return a structured exit code:
```go
return exitf(inv, 1, "cmd: missing operand")
return exitf(inv, 1, "cmd: %s: No such file or directory", name)
return exitf(inv, 2, "cmd: invalid option -- %c", flag)
```

For errors from filesystem operations, wrap in `ExitError`:
```go
return &ExitError{Code: 1, Err: err}
```

Exit code conventions:
- `0` — success
- `1` — general errors (missing files, bad args)
- `2` — usage/syntax errors
- `126` — policy denied (use `exitCodeForError(err)` which handles this)

## Filesystem access

Never access the host filesystem. Use the helpers from `command.go`:

| Helper | Purpose |
|--------|---------|
| `allowPath(ctx, inv, action, name)` | Resolve path, check policy, record trace event. Returns absolute path. |
| `openRead(ctx, inv, name)` | Allow + open for reading. Returns `(File, absPath, error)`. |
| `readDir(ctx, inv, name)` | Allow + list directory entries. |
| `statPath(ctx, inv, name)` | Allow + stat. |
| `lstatPath(ctx, inv, name)` | Allow + lstat (doesn't follow symlinks). |
| `statMaybe(ctx, inv, action, name)` | Stat that returns `(info, abs, exists, error)` — doesn't error on not-found. |

For writing files:
```go
abs, err := allowPath(ctx, inv, policy.FileActionWrite, name)
if err != nil {
    return err
}
file, err := inv.FS.OpenFile(ctx, abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
```

## Trace recording

File access trace events are recorded automatically by `allowPath()` and friends. For mutation events, use the helper:
```go
recordFileMutation(inv.Trace, "cmd-name", sourcePath, "", destPath)
```

## Stdin as input

Follow the Unix convention — if no file arguments, read from stdin:
```go
if len(files) == 0 {
    data, err := io.ReadAll(inv.Stdin)
    if err != nil {
        return &ExitError{Code: 1, Err: err}
    }
    // process data
}
```

Some commands also accept `-` as an explicit stdin marker.

## Registration

Add to `DefaultRegistry()` in `commands/registry.go`:
```go
func DefaultRegistry() *Registry {
    return NewRegistry(
        // ... existing commands ...
        New<Name>(),  // add in appropriate category group
    )
}
```
