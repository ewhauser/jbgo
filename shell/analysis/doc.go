// Package analysis exposes read-only semantic observation hooks for gbash
// shell execution.
//
// The package is intended for external analysis and static-tooling consumers
// that need shell execution events without depending on interpreter internals.
// Observers receive immutable context snapshots and concrete event values for
// statement, command, scope, variable, and option activity.
package analysis
