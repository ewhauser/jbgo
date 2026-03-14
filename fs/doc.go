// Package fs provides the filesystem contracts and virtual filesystem backends
// used by gbash.
//
// This is a supported public extension package for callers that need to supply
// custom filesystem implementations or factories. Most embedders should still
// prefer the root `github.com/ewhauser/gbash` package and its higher-level
// filesystem helpers.
package fs
