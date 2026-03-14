# OpenTelemetry Session Demo

This example shows how an embedder can translate `gbash` observability callbacks into OpenTelemetry traces and log records without changing `gbash` itself.

It uses one persistent `gbash` session, runs two named `Session.Exec` calls, writes telemetry to local stdout with the OTEL stdout exporters, and keeps the example's own status lines on stderr.

## What It Demonstrates

- `gbash.WithTracing(gbash.TraceConfig{Mode: gbash.TraceRedacted, OnEvent: ...})` for structured trace callbacks
- `gbash.WithLogger(...)` for top-level lifecycle log callbacks
- mapping `exec.start` and `exec.finish` into one root span per shell execution
- attaching `command.*`, `file.*`, and `policy.*` `gbash` events as OTEL span events
- emitting every `gbash.LogEvent` as an OTEL log record
- session-level filesystem persistence by writing `/tmp/otel-session-demo.txt` in one execution and reading it in the next

The demo uses redacted tracing by default so argv-like data stays aligned with the safer mode recommended in the `gbash` docs.

## Run

From the repository root:

```bash
go run ./examples/otel
```

Telemetry is written to stdout as OTEL JSON. The example's own status lines go to stderr.

From the `examples/` module:

```bash
cd examples
make run-otel
```

## Mapping Notes

- `exec.start` starts a root span named `gbash.exec.<request-name>`
- `exec.finish` and `exec.error` set execution attributes and end that span
- `trace.Event` values become span events on the active execution span
- `gbash.LogEvent` values become OTEL log records with `session.id`, `execution.id`, `gbash.log.kind`, `workdir`, and execution outcome attributes

The example deliberately keeps command-level activity as span events instead of synthesizing child spans, because the current `gbash` trace model does not expose a stable per-command correlation ID.
