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
	nixJQEnv             = "GBASH_CONFORMANCE_JQ"
	pinnedNixJQVersion   = "1.8.1"
	pinnedNixJQSubstring = "jq-" + pinnedNixJQVersion
)

var errNixJQUnset = errors.New(nixJQEnv + " is not set")

// RequireNixJQ returns the pinned jq oracle configured for the test suite,
// failing the test when it is unavailable or misconfigured.
func RequireNixJQ(tb testing.TB) string {
	tb.Helper()

	path, firstLine, err := resolveNixJQ(tb.Context())
	if err != nil {
		tb.Fatalf("%v\n\n%s", err, nixJQInstructions())
	}
	tb.Logf("jq oracle: %s (%s)", firstLine, path)
	return path
}

// RequireNixJQOrSkip returns the pinned jq oracle configured for the test
// suite, skipping the test when it is unavailable. If the env var is set but
// points at the wrong jq, the test fails so misconfiguration is surfaced
// immediately.
func RequireNixJQOrSkip(tb testing.TB) string {
	tb.Helper()

	path, firstLine, err := resolveNixJQ(tb.Context())
	if err != nil {
		if errors.Is(err, errNixJQUnset) {
			tb.Skipf("%v\n\n%s", err, nixJQInstructions())
		}
		tb.Fatalf("%v\n\n%s", err, nixJQInstructions())
	}
	tb.Logf("jq oracle: %s (%s)", firstLine, path)
	return path
}

func resolveNixJQ(ctx context.Context) (path, firstLine string, err error) {
	path = strings.TrimSpace(os.Getenv(nixJQEnv)) //nolint:forbidigo // Tests explicitly read the oracle jq path from the host env.
	if path == "" {
		return "", "", errNixJQUnset
	}

	out, err := exec.CommandContext(ctx, path, "--version").Output() //nolint:forbidigo // Tests validate the configured external jq oracle before use.
	if err != nil {
		return "", "", fmt.Errorf("failed to get jq version from %s: %w", path, err)
	}

	firstLine, _, _ = strings.Cut(string(out), "\n")
	if !strings.Contains(firstLine, pinnedNixJQSubstring) {
		return "", "", fmt.Errorf(
			"tests require jq %s (pinned via Nix), got: %s",
			pinnedNixJQVersion,
			firstLine,
		)
	}

	return path, firstLine, nil
}

func nixJQInstructions() string {
	return "From the repo root, set GBASH_CONFORMANCE_JQ to the pinned Nix jq:\n" +
		"  export GBASH_CONFORMANCE_JQ=$(./scripts/ensure-jq.sh)"
}
