# Contrib Commands

`contrib/` holds optional `gbash` extensions that should not inflate the core runtime module.

Rules for this directory:

- each direct child under `contrib/` is its own Go module
- contrib commands are opt-in and are not registered by `gbash.DefaultRegistry()`
- contrib is where heavyweight or niche commands live so the main `github.com/ewhauser/gbash` module stays small

Today that includes:

- `contrib/awk` for the optional sandboxed `awk` command
- `contrib/extras` for a convenience helper that builds a registry with all official contrib commands enabled
- `contrib/jq` for the optional sandboxed `jq` command and its JSON/query stack
- `contrib/sqlite3` for the optional sandboxed `sqlite3` command
- `contrib/yq` for the optional sandboxed `yq` command and its YAML/query stack
