// Package shell provides the low-level shell engine contracts and mvdan/sh
// integration used by gbash.
//
// This is a supported public extension package for callers that need to
// provide an alternative shell engine implementation. Most embedders should
// construct and run sandboxes through the root
// `github.com/ewhauser/gbash` package.
package shell
