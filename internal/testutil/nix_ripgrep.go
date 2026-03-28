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
	nixRipgrepEnv             = "GBASH_CONFORMANCE_RIPGREP"
	pinnedNixRipgrepVersion   = "15.1.0"
	pinnedNixRipgrepSubstring = "ripgrep " + pinnedNixRipgrepVersion
)

var errNixRipgrepUnset = errors.New(nixRipgrepEnv + " is not set")

// RequireNixRipgrep returns the pinned ripgrep oracle configured for the test
// suite, failing the test when it is unavailable or misconfigured.
func RequireNixRipgrep(tb testing.TB) string {
	tb.Helper()

	path, firstLine, err := resolveNixRipgrep(tb.Context())
	if err != nil {
		tb.Fatalf("%v\n\n%s", err, nixRipgrepInstructions())
	}
	tb.Logf("ripgrep oracle: %s (%s)", firstLine, path)
	return path
}

func resolveNixRipgrep(ctx context.Context) (path, firstLine string, err error) {
	path = strings.TrimSpace(os.Getenv(nixRipgrepEnv)) //nolint:forbidigo // Tests explicitly read the oracle ripgrep path from the host env.
	if path == "" {
		return "", "", errNixRipgrepUnset
	}

	out, err := exec.CommandContext(ctx, path, "--version").Output() //nolint:forbidigo // Tests validate the configured external ripgrep oracle before use.
	if err != nil {
		return "", "", fmt.Errorf("failed to get ripgrep version from %s: %w", path, err)
	}

	firstLine, _, _ = strings.Cut(string(out), "\n")
	if !strings.Contains(firstLine, pinnedNixRipgrepSubstring) {
		return "", "", fmt.Errorf(
			"tests require ripgrep %s (pinned via Nix), got: %s",
			pinnedNixRipgrepVersion,
			firstLine,
		)
	}

	return path, firstLine, nil
}

func nixRipgrepInstructions() string {
	return "From the repo root, set GBASH_CONFORMANCE_RIPGREP to the pinned Nix ripgrep:\n" +
		"  export GBASH_CONFORMANCE_RIPGREP=$(./scripts/ensure-ripgrep.sh)"
}
