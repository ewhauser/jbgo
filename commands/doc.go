// Package commands provides the stable command authoring and registry API for
// gbash.
//
// Most embedders should construct and run sandboxes through the root
// `github.com/ewhauser/gbash` package and only import commands when they need
// to:
//
//   - implement custom commands
//   - customize a command registry
//   - reuse gbash's command-spec parsing and invocation helpers
//
// The package currently also contains builtin command implementations, but the
// long-term extension contract is the command API itself rather than this being
// the permanent home of every builtin implementation.
package commands
