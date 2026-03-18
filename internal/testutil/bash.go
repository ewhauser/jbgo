package testutil

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	conformanceBashEnv             = "GBASH_CONFORMANCE_BASH"
	pinnedConformanceBashVersion   = "5.3.9"
	pinnedConformanceBashSubstring = "version " + pinnedConformanceBashVersion
)

var errConformanceBashUnset = errors.New(conformanceBashEnv + " is not set")

// RequireConformanceBash returns the pinned bash oracle configured for the test
// suite, failing the test when it is unavailable or misconfigured.
func RequireConformanceBash(tb testing.TB) string {
	tb.Helper()

	path, firstLine, err := resolveConformanceBash()
	if err != nil {
		tb.Fatalf("%v\n\n%s", err, conformanceBashInstructions())
	}
	tb.Logf("bash oracle: %s (%s)", firstLine, path)
	return path
}

// RequireConformanceBashOrSkip returns the pinned bash oracle configured for
// the test suite, skipping the test when it is unavailable. If the env var is
// set but points at the wrong bash, the test fails so misconfiguration is
// surfaced immediately.
func RequireConformanceBashOrSkip(tb testing.TB) string {
	tb.Helper()

	path, firstLine, err := resolveConformanceBash()
	if err != nil {
		if errors.Is(err, errConformanceBashUnset) {
			tb.Skipf("%v\n\n%s", err, conformanceBashInstructions())
		}
		tb.Fatalf("%v\n\n%s", err, conformanceBashInstructions())
	}
	tb.Logf("bash oracle: %s (%s)", firstLine, path)
	return path
}

func resolveConformanceBash() (path, firstLine string, err error) {
	path = strings.TrimSpace(os.Getenv(conformanceBashEnv)) //nolint:forbidigo // Tests explicitly read the oracle bash path from the host env.
	if path == "" {
		return "", "", errConformanceBashUnset
	}

	out, err := exec.Command(path, "--version").Output() //nolint:forbidigo // Tests validate the configured external bash oracle before use.
	if err != nil {
		return "", "", fmt.Errorf("failed to get bash version from %s: %w", path, err)
	}

	firstLine, _, _ = strings.Cut(string(out), "\n")
	if !strings.Contains(firstLine, pinnedConformanceBashSubstring) {
		return "", "", fmt.Errorf(
			"tests require bash %s (pinned via Nix), got: %s",
			pinnedConformanceBashVersion,
			firstLine,
		)
	}

	return path, firstLine, nil
}

func conformanceBashInstructions() string {
	return "From the repo root, set GBASH_CONFORMANCE_BASH to the pinned Nix bash:\n" +
		"  export GBASH_CONFORMANCE_BASH=$(./scripts/ensure-bash.sh)"
}
