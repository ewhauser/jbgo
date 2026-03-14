// Package network provides the sandboxed HTTP client used by gbash.
//
// This is a supported public extension package for callers that need to
// customize the HTTP transport, resolver, or request policy used by curl
// inside the sandbox. Most embedders should prefer the root
// `github.com/ewhauser/gbash` package and configure network access through its
// higher-level options.
package network
