// Package gbash provides the primary embedding API for the gbash sandbox.
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
// Most embedders should only import the root package. Advanced integrations may
// also import the supported extension packages:
//
//   - commands for custom command authorship and registry customization
//   - fs for custom filesystem backends and factories
//   - network for sandbox HTTP client customization
//   - policy for sandbox policy implementations
//   - shell for alternative shell engine implementations
//   - trace for structured execution event consumption
//
// Packages under internal/ and other undocumented subpackages are not public
// API.
package gbash
