# contrib/python

`contrib/python` provides sandboxed `python` and `python3` commands for gbash,
backed by [`github.com/ewhauser/gomonty`](https://github.com/ewhauser/gomonty).

## Status

- opt-in through `github.com/ewhauser/gbash/contrib/python`
- bundled by `contrib/extras`
- not part of `gbash.DefaultRegistry()`
- cgo-backed through `gomonty`

## Supported Command Surface

The v1 command intentionally stays small:

- `python -c 'code'`
- `python script.py`
- `python` with source from stdin
- `--help`
- `--version`

Unsupported flags such as `-m` and extra script arguments are rejected with a
usage error.

Builtin `print(...)` writes to gbash stdout. Upstream Monty still rejects
`print(..., file=...)`, so redirecting builtin `print` to arbitrary streams is
not supported.

## Sandbox Rules

- filesystem access flows through `commands.Invocation.FS`
- environment access flows through `commands.Invocation.Env`
- relative paths resolve from the gbash working directory
- print output goes to gbash stdout
- command execution never escapes to the host OS

`gomonty` is cgo-backed and requires a supported target archive. When the
native bindings are unavailable, the command exits non-zero with the bundled
stub diagnostic from `gomonty`.

## Registering The Commands

```go
registry := gbash.DefaultRegistry()
if err := python.Register(registry); err != nil {
    return err
}
```

`Register` installs both `python` and `python3`.
