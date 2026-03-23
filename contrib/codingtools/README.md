# contrib/codingtools

`contrib/codingtools` provides reusable `read`, `edit`, and `write` tool contracts on top of `github.com/ewhauser/gbash/fs.FileSystem`.

It is a public helper module, not a shell-command bundle:

- not part of `gbash.DefaultRegistry()`
- not registered by `contrib/extras`
- intended for embedders that want provider-neutral file tools over a gbash-owned filesystem abstraction

## What It Includes

- provider-neutral tool metadata via `ToolDefinition`
- request parsers for provider payloads
- structured text and image responses
- upstream-style truncation and exact-text edit semantics
- path resolution that stays agnostic to in-memory vs host-backed filesystems
- per-toolset mutation serialization for `edit` and `write`

## Quick Start

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/contrib/codingtools"
)

func main() {
	fsys := gbfs.NewMemory()
	tools := codingtools.New(codingtools.Config{
		FS:         fsys,
		WorkingDir: "/workspace",
	})

	_, _ = tools.Write(context.Background(), codingtools.WriteRequest{
		Path:    "note.txt",
		Content: "hello from codingtools\n",
	})

	resp, _ := tools.Read(context.Background(), codingtools.ReadRequest{
		Path: "note.txt",
	})

	payload, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(payload))
}
```

## Attribution

This package is a gbash-owned Go port of the `read`, `edit`, and `write`
tools from [`badlogic/pi-mono`](https://github.com/badlogic/pi-mono), adapted
to run against gbash's public filesystem interface instead of Node's host
filesystem APIs.
