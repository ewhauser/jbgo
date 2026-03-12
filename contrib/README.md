# Contrib Commands

`contrib/` holds optional `gbash` extensions that should not inflate the core runtime module.

Rules for this directory:

- each direct child under `contrib/` is its own Go module
- contrib commands are opt-in and are not registered by `commands.DefaultRegistry()`
- contrib is where heavyweight or niche commands live so the main `github.com/ewhauser/gbash` module stays small

Today that includes:

- `contrib/sqlite3` for the optional sandboxed `sqlite3` command
