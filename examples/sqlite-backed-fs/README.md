# SQLite-Backed Filesystem

This example implements a custom `gbfs.FileSystem` on top of a host SQLite database file and wires it into `gbash.CustomFileSystem(...)`.

Each invocation creates a fresh sandbox session, but the sandbox filesystem state is stored in the SQLite database passed with `--db`, so files created in one run are visible in later runs.

## Run

Pass the backing database file and either a script flag or stdin:

```bash
go run ./examples/sqlite-backed-fs --db /tmp/gbash-sandbox.db --script "printf 'hello from sqlite fs\n' > /tmp/hello.txt"
```

To start a real interactive REPL against one persistent SQLite-backed sandbox session:

```bash
go run ./examples/sqlite-backed-fs --db /tmp/gbash-sandbox.db --repl
```

Inside the REPL, filesystem state, `PWD`, and exported environment variables persist across commands until you exit.

From the `examples/` module, you can also use the bundled Make target:

```bash
cd examples
make run-sqlite-backed-fs SQLITE_FS_DB=/tmp/gbash-sandbox.db SQLITE_FS_SCRIPT="printf 'hello from sqlite fs\n' > /tmp/hello.txt"
```

For the interactive mode:

```bash
cd examples
make run-sqlite-backed-fs-repl SQLITE_FS_DB=/tmp/gbash-sandbox.db
```

Then run a second script against the same backing database:

```bash
go run ./examples/sqlite-backed-fs --db /tmp/gbash-sandbox.db --script "cat /tmp/hello.txt"
```

You can also pipe the script on stdin:

```bash
cat <<'EOF' | go run ./examples/sqlite-backed-fs --db /tmp/gbash-sandbox.db
pwd
ls /tmp
cat /tmp/hello.txt
EOF
```

Optional flags:

```bash
go run ./examples/sqlite-backed-fs --db /tmp/gbash-sandbox.db --workdir /home/agent/project
```

## Notes

- The SQLite database file is a host-side backing store for the example backend. It is not mounted inside the sandbox automatically.
- The example backend stores directories, regular files, symlinks, metadata, and hard-link relationships directly in SQLite tables.
- In one-shot mode, each invocation creates a fresh session, so filesystem contents persist across runs but `cwd` does not.
- In `--repl` mode, filesystem contents, `cwd`, and exported environment variables persist across entries in the same process.
