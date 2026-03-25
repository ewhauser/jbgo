//go:build linux

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunReadWriteRootTouchDashUsesInheritedStdoutPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "stdout-target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", target, err)
	}
	old := time.Date(2001, time.February, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(target, old, old); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", target, err)
	}

	stdoutFile, err := os.Open(target)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", target, err)
	}
	t.Cleanup(func() {
		if closeErr := stdoutFile.Close(); closeErr != nil {
			t.Fatalf("Close(%q) error = %v", target, closeErr)
		}
	})

	var stderr strings.Builder
	exitCode, err := run(
		context.Background(),
		Config{Name: "gbash", SystemTempRoots: func() []string { return []string{os.TempDir()} }},
		[]string{"--readwrite-root", root, "-c", "touch -"},
		strings.NewReader(""),
		stdoutFile,
		&stderr,
		false,
	)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", target, err)
	}
	if !info.ModTime().After(old) {
		t.Fatalf("ModTime(%q) = %v, want after %v", target, info.ModTime(), old)
	}
}
