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
	nixCurlEnv             = "GBASH_CONFORMANCE_CURL"
	pinnedNixCurlVersion   = "8.18.0"
	pinnedNixCurlSubstring = "curl " + pinnedNixCurlVersion
)

var errNixCurlUnset = errors.New(nixCurlEnv + " is not set")

// RequireNixCurl returns the pinned curl oracle configured for the test suite,
// failing the test when it is unavailable or misconfigured.
func RequireNixCurl(tb testing.TB) string {
	tb.Helper()

	path, firstLine, err := resolveNixCurl(tb.Context())
	if err != nil {
		tb.Fatalf("%v\n\n%s", err, nixCurlInstructions())
	}
	tb.Logf("curl oracle: %s (%s)", firstLine, path)
	return path
}

// RequireNixCurlOrSkip returns the pinned curl oracle configured for the test
// suite, skipping the test when it is unavailable. If the env var is set but
// points at the wrong curl, the test fails so misconfiguration is surfaced
// immediately.
func RequireNixCurlOrSkip(tb testing.TB) string {
	tb.Helper()

	path, firstLine, err := resolveNixCurl(tb.Context())
	if err != nil {
		if errors.Is(err, errNixCurlUnset) {
			tb.Skipf("%v\n\n%s", err, nixCurlInstructions())
		}
		tb.Fatalf("%v\n\n%s", err, nixCurlInstructions())
	}
	tb.Logf("curl oracle: %s (%s)", firstLine, path)
	return path
}

func resolveNixCurl(ctx context.Context) (path, firstLine string, err error) {
	path = strings.TrimSpace(os.Getenv(nixCurlEnv)) //nolint:forbidigo // Tests explicitly read the oracle curl path from the host env.
	if path == "" {
		return "", "", errNixCurlUnset
	}

	out, err := exec.CommandContext(ctx, path, "--version").Output() //nolint:forbidigo // Tests validate the configured external curl oracle before use.
	if err != nil {
		return "", "", fmt.Errorf("failed to get curl version from %s: %w", path, err)
	}

	firstLine, _, _ = strings.Cut(string(out), "\n")
	if !strings.Contains(firstLine, pinnedNixCurlSubstring) {
		return "", "", fmt.Errorf(
			"tests require curl %s (pinned via Nix), got: %s",
			pinnedNixCurlVersion,
			firstLine,
		)
	}

	return path, firstLine, nil
}

func nixCurlInstructions() string {
	return "From the repo root, set GBASH_CONFORMANCE_CURL to the pinned Nix curl:\n" +
		"  export GBASH_CONFORMANCE_CURL=$(./scripts/ensure-curl.sh)"
}
