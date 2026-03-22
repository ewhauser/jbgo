// Package host defines gbash's public host adapter boundary.
//
// Most embedders do not need to import this package. The default gbash runtime
// uses an internal virtual host adapter that preserves the project’s sandbox
// defaults and historical behavior.
//
// Import this package when you want to override the shell-visible host view
// that gbash projects into a runtime via gbash.Config.Host or
// gbash.WithHost(...). A host adapter controls:
//
//   - host-derived base environment defaults such as HOME, PATH, or USER
//   - the logical platform identity surfaced by OSTYPE, uname, hostname, help,
//     env-name matching, and executable lookup behavior
//   - the initial PID, PPID, and process-group metadata seen by the shell
//   - the pipe primitive used by pipelines and process substitution
//
// The host boundary is intentionally narrower than “the whole operating
// system”. Filesystem selection, sandbox policy, network access, clocks, and
// randomness remain outside this package and are still configured elsewhere in
// gbash.
//
// The only public concrete adapter in v1 is [NewSystem], which reflects the
// current process and OS. The default virtual adapter remains internal and is
// not part of the public API.
package host
