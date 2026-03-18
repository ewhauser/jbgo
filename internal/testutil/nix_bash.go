package testutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	nixBashEnv             = "GBASH_CONFORMANCE_BASH"
	pinnedNixBashVersion   = "5.3.9"
	pinnedNixBashSubstring = "version " + pinnedNixBashVersion
)

var errNixBashUnset = errors.New(nixBashEnv + " is not set")

// RequireNixBash returns the pinned bash oracle configured for the test suite,
// failing the test when it is unavailable or misconfigured.
func RequireNixBash(tb testing.TB) string {
	tb.Helper()

	path, firstLine, err := resolveNixBash(tb.Context())
	if err != nil {
		tb.Fatalf("%v\n\n%s", err, nixBashInstructions())
	}
	tb.Logf("bash oracle: %s (%s)", firstLine, path)
	return path
}

// RequireNixBashOrSkip returns the pinned bash oracle configured for
// the test suite, skipping the test when it is unavailable. If the env var is
// set but points at the wrong bash, the test fails so misconfiguration is
// surfaced immediately.
func RequireNixBashOrSkip(tb testing.TB) string {
	tb.Helper()

	path, firstLine, err := resolveNixBash(tb.Context())
	if err != nil {
		if errors.Is(err, errNixBashUnset) {
			tb.Skipf("%v\n\n%s", err, nixBashInstructions())
		}
		tb.Fatalf("%v\n\n%s", err, nixBashInstructions())
	}
	tb.Logf("bash oracle: %s (%s)", firstLine, path)
	return path
}

func resolveNixBash(ctx context.Context) (path, firstLine string, err error) {
	path = strings.TrimSpace(os.Getenv(nixBashEnv)) //nolint:forbidigo // Tests explicitly read the oracle bash path from the host env.
	if path == "" {
		return "", "", errNixBashUnset
	}

	out, err := exec.CommandContext(ctx, path, "--version").Output() //nolint:forbidigo // Tests validate the configured external bash oracle before use.
	if err != nil {
		return "", "", fmt.Errorf("failed to get bash version from %s: %w", path, err)
	}

	firstLine, _, _ = strings.Cut(string(out), "\n")
	if !strings.Contains(firstLine, pinnedNixBashSubstring) {
		return "", "", fmt.Errorf(
			"tests require bash %s (pinned via Nix), got: %s",
			pinnedNixBashVersion,
			firstLine,
		)
	}

	return path, firstLine, nil
}

func nixBashInstructions() string {
	return "From the repo root, set GBASH_CONFORMANCE_BASH to the pinned Nix bash:\n" +
		"  export GBASH_CONFORMANCE_BASH=$(./scripts/ensure-bash.sh)"
}
