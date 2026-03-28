# AgentFS-Backed Filesystem

This example uses the upstream [AgentFS Go SDK](https://github.com/tursodatabase/agentfs/tree/main/sdk/go) as the persistent backing store for a `gbash` sandbox filesystem.

It shows the external-integration path for durable `gbash` sessions: `gbash.CustomFileSystem(...)` is wired to an adapter that translates `gbash/fs.FileSystem` calls onto AgentFS's SQLite-backed filesystem API.

Each invocation creates a fresh `gbash` session, but the sandbox filesystem state is stored in the AgentFS database passed with `--db`, so files created in one run are visible in later runs.

This example is intentionally filesystem-only. It does not use AgentFS KV or tool-call tracking APIs.

## Run

Pass the backing database file and either a script flag or stdin:

```bash
go run ./examples/agentfs-backed-fs --db /tmp/gbash-agentfs.db --script "printf 'hello from agentfs\n' > /tmp/hello.txt"
```

To start a real interactive REPL against one persistent AgentFS-backed sandbox session:

```bash
go run ./examples/agentfs-backed-fs --db /tmp/gbash-agentfs.db --repl
```

Inside the REPL, filesystem state, `PWD`, and exported environment variables persist across commands until you exit.

From the `examples/` module, you can also use the bundled Make target:

```bash
cd examples
make run-agentfs-backed-fs AGENTFS_FS_DB=/tmp/gbash-agentfs.db AGENTFS_FS_SCRIPT="printf 'hello from agentfs\n' > /tmp/hello.txt"
```

Then run a second script against the same backing database:

```bash
go run ./examples/agentfs-backed-fs --db /tmp/gbash-agentfs.db --script "cat /tmp/hello.txt"
```

You can also pipe the script on stdin:

```bash
cat <<'EOF' | go run ./examples/agentfs-backed-fs --db /tmp/gbash-agentfs.db
pwd
ls /tmp
cat /tmp/hello.txt
EOF
```

Optional flags:

```bash
go run ./examples/agentfs-backed-fs --db /tmp/gbash-agentfs.db --workdir /home/agent/project
```

## Notes

- The AgentFS database file is a host-side backing store for the example backend. It is not mounted inside the sandbox automatically.
- The example keeps behavior deterministic by requiring an explicit `--db` path instead of defaulting to `~/.agentfs`.
- The upstream AgentFS project is beta software; this example demonstrates integration shape rather than making stronger durability or compatibility claims than the upstream SDK does.
