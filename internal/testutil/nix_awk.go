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
	nixAWKEnv             = "GBASH_CONFORMANCE_AWK"
	pinnedNixAWKVersion   = "5.3.2"
	pinnedNixAWKSubstring = "GNU Awk " + pinnedNixAWKVersion
)

var errNixAWKUnset = errors.New(nixAWKEnv + " is not set")

// RequireNixAWK returns the pinned awk oracle configured for the test suite,
// failing the test when it is unavailable or misconfigured.
func RequireNixAWK(tb testing.TB) string {
	tb.Helper()

	path, firstLine, err := resolveNixAWK(tb.Context())
	if err != nil {
		tb.Fatalf("%v\n\n%s", err, nixAWKInstructions())
	}
	tb.Logf("awk oracle: %s (%s)", firstLine, path)
	return path
}

// RequireNixAWKOrSkip returns the pinned awk oracle configured for the test
// suite, skipping the test when it is unavailable. If the env var is set but
// points at the wrong awk, the test fails so misconfiguration is surfaced
// immediately.
func RequireNixAWKOrSkip(tb testing.TB) string {
	tb.Helper()

	path, firstLine, err := resolveNixAWK(tb.Context())
	if err != nil {
		if errors.Is(err, errNixAWKUnset) {
			tb.Skipf("%v\n\n%s", err, nixAWKInstructions())
		}
		tb.Fatalf("%v\n\n%s", err, nixAWKInstructions())
	}
	tb.Logf("awk oracle: %s (%s)", firstLine, path)
	return path
}

func resolveNixAWK(ctx context.Context) (path, firstLine string, err error) {
	path = strings.TrimSpace(os.Getenv(nixAWKEnv)) //nolint:forbidigo // Tests explicitly read the oracle awk path from the host env.
	if path == "" {
		return "", "", errNixAWKUnset
	}

	out, err := exec.CommandContext(ctx, path, "--version").Output() //nolint:forbidigo // Tests validate the configured external awk oracle before use.
	if err != nil {
		return "", "", fmt.Errorf("failed to get awk version from %s: %w", path, err)
	}

	firstLine, _, _ = strings.Cut(string(out), "\n")
	if !strings.Contains(firstLine, pinnedNixAWKSubstring) {
		return "", "", fmt.Errorf(
			"tests require awk %s (pinned via Nix), got: %s",
			pinnedNixAWKVersion,
			firstLine,
		)
	}

	return path, firstLine, nil
}

func nixAWKInstructions() string {
	return "From the repo root, set GBASH_CONFORMANCE_AWK to the pinned Nix awk:\n" +
		"  export GBASH_CONFORMANCE_AWK=$(./scripts/ensure-awk.sh)"
}
