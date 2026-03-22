# contrib/bashtool

`contrib/bashtool` provides a reusable LLM-facing bash tool contract on top of `gbash`.

It is a public helper module, not a sandbox command bundle:

- not part of `gbash.DefaultRegistry()`
- not registered by `contrib/extras`
- intended for embedders that want a `bash` function/tool definition with gbash-backed execution

## What It Includes

- provider-neutral tool metadata via `ToolDefinition()`
- input and output schemas
- upstream-style `SystemPrompt()` and `Help()` surfaces
- request parsing for `commands` plus the `script` alias
- evaluator-style `FormatToolResult()` rendering
- one-shot `Execute()` backed by a fresh `gbash` runtime
- optional config-level prompt appends via `Config.SystemPromptAppend`

Defaults stay gbash-native:

- tool name: `bash`
- home directory: `/home/agent`
- hostname: `gbash`
- command profile: plain `gbash`

Use `CommandProfileExtras` when you want the stable extras registry (`awk`, `html-to-markdown`, `jq`, `sqlite3`, `yq`) reflected in the prompt/help/execute surface.

## Quick Start

```go
package main

import (
	"context"
	"fmt"

	"github.com/ewhauser/gbash/contrib/bashtool"
)

func main() {
	tool := bashtool.New(bashtool.Config{
		Profile:            bashtool.CommandProfileExtras,
		SystemPromptAppend: "Always prefer jq for JSON reshaping when available.",
	})

	resp := tool.Execute(context.Background(), bashtool.Request{
		Commands: `printf '{"name":"alice"}' | jq -r '.name'`,
	})
	fmt.Print(resp.Stdout)
}
```

## Relationship To `examples/gbash-eval`

`examples/gbash-eval` uses this module for bash tool metadata, prompt generation, request parsing, and tool-result formatting, while keeping its own persistent session harness for multi-turn filesystem persistence.

## Attribution

This package is a gbash-owned Go port of the upstream `bashkit` Bash tool contract from [`everruns/bashkit`](https://github.com/everruns/bashkit), adapted under Apache-2.0 for gbash-specific defaults and execution plumbing.
