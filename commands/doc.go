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
// gbash's shipped command implementations live under internal/ and are not part
// of this package's public API.
package commands
