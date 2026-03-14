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

Versioning rules:

- the root module is tagged as `vX.Y.Z`
- each contrib module is tagged with its module path prefix, for example `contrib/jq/vX.Y.Z`
- contrib modules should require real tagged `github.com/ewhauser/gbash` versions, not `v0.0.0`
- contrib modules keep committed local `replace` directives so each module builds against the local checkout before those tags exist
- `make fix-modules MODULE_VERSION=vX.Y.Z` updates the nested module requirements and refreshes those local replaces
- the default release flow is GitHub Actions based: prepare a `release/vX.Y.Z` PR, merge it, and let publish automation create the matching root plus contrib tags
- `make tag-release RELEASE_VERSION=vX.Y.Z` remains as a local fallback for debugging or manual recovery
