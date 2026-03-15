# OAuth Network Extension Demo

This example shows the intended extension pattern for secret-bearing HTTP auth in `gbash`:

- the actual sandbox calls live in [`demo.sh`](./demo.sh)
- the embedder injects a custom `NetworkClient`
- that client looks up a bearer token from a host-side vault and adds `Authorization` before the request leaves the sandbox boundary
- the token never appears in the shell script, `curl` argv, or response body
- even if the sandbox tries to send its own `Authorization` header, the host extension overwrites it before forwarding the request

To keep the demo self-contained, the Go example starts a local HTTP server and gives the client and server the same hard-coded "vault" entry. The point is not the fake vault itself. The point is the boundary: the secret lives in host-owned Go code, not in the sandbox.

## Run

From the repository root:

```bash
go run ./examples/oauth-network-extension
```

From the `examples/` module:

```bash
cd examples
make run-oauth-network-extension
```

## What It Demonstrates

- `gbash.WithNetworkClient(...)` as the escape hatch for host-controlled HTTP behavior
- a readable shell script that performs the exact `curl` calls under test
- mapping a sandbox-visible URL like `https://crm.example.test/v1/profile` onto a real transport destination
- injecting OAuth bearer credentials from a host-side secret store
- using `TraceRaw` to prove the real secret never entered sandbox argv in the first place
- showing a second request where the sandbox sends a forged bearer header and the extension replaces it with the vault token
- returning a normal JSON API response back to `curl` without exposing the bearer token

## Why This Pattern Matters

If you pass secrets directly in shell commands, they can leak into trace argv, logs, prompts, copied scripts, and agent memory. A host-side network extension keeps the credential at the embedder boundary and lets the sandbox work with a plain request shape instead.

The second half of the demo makes one subtle point explicit: the sandbox can still attempt to send its own auth header, and that attempt is visible in raw trace argv. The protection is that the authoritative credential is selected and applied by the host extension, not trusted from sandbox input.

For a production embedding, replace the hard-coded vault with your real token source and add whatever policy you need around hosts, paths, methods, and token selection.
