package testutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveNixCurl(t *testing.T) {
	t.Run("missing env", func(t *testing.T) {
		t.Setenv(nixCurlEnv, "")

		_, _, err := resolveNixCurl(context.Background())
		if !errors.Is(err, errNixCurlUnset) {
			t.Fatalf("resolveNixCurl() error = %v, want %v", err, errNixCurlUnset)
		}
	})

	t.Run("wrong version", func(t *testing.T) {
		path := writeFakeCurl(t, "curl 8.17.1 (x86_64-unknown-linux-gnu)")
		t.Setenv(nixCurlEnv, path)

		_, _, err := resolveNixCurl(context.Background())
		if err == nil {
			t.Fatal("resolveNixCurl() error = nil, want version error")
		}
		if !strings.Contains(err.Error(), "tests require curl "+pinnedNixCurlVersion) {
			t.Fatalf("resolveNixCurl() error = %v, want pinned version diagnostic", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		wantFirstLine := "curl 8.18.0 (aarch64-apple-darwin24.0) libcurl/8.18.0"
		path := writeFakeCurl(t, wantFirstLine)
		t.Setenv(nixCurlEnv, path)

		gotPath, gotFirstLine, err := resolveNixCurl(context.Background())
		if err != nil {
			t.Fatalf("resolveNixCurl() error = %v", err)
		}
		if gotPath != path {
			t.Fatalf("resolveNixCurl() path = %q, want %q", gotPath, path)
		}
		if gotFirstLine != wantFirstLine {
			t.Fatalf("resolveNixCurl() firstLine = %q, want %q", gotFirstLine, wantFirstLine)
		}
	})
}

func writeFakeCurl(t *testing.T, firstLine string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "curl")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' " + shellQuote(firstLine) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}
