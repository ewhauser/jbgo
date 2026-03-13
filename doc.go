// Package gbash provides the public embedding API for the gbash sandbox.
//
// The root package is the intended entry point for most callers. It exposes
// the runtime, session, execution request/result types, and the opinionated
// configuration helpers that cover the common embedding cases:
//
//   - create an isolated in-memory sandbox with [New]
//   - mount a real host directory into the sandbox with [WithWorkspace]
//   - enable allowlisted HTTP access for curl with [WithHTTPAccess] or
//     [WithNetwork]
//   - customize the registry, policy, engine, or filesystem with explicit
//     options when you need lower-level control
//
// More specialized packages such as commands, fs, network, policy, and shell
// remain available for advanced integrations and extension points, but most
// applications should not need to import them just to construct and run a
// sandbox.
package gbash
