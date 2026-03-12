package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyTreePreservesSymlinkModeAndModTime(t *testing.T) {
	sourceDir := t.TempDir()
	destDir := t.TempDir()

	binDir := filepath.Join(sourceDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(binDir) error = %v", err)
	}
	sourceFile := filepath.Join(binDir, "tool.sh")
	if err := os.WriteFile(sourceFile, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(sourceFile) error = %v", err)
	}
	wantModTime := time.Date(2024, time.March, 12, 10, 11, 12, 0, time.UTC)
	if err := os.Chtimes(sourceFile, wantModTime, wantModTime); err != nil {
		t.Fatalf("Chtimes(sourceFile) error = %v", err)
	}
	if err := os.Symlink("tool.sh", filepath.Join(binDir, "tool-link")); err != nil {
		t.Fatalf("Symlink(tool-link) error = %v", err)
	}

	if err := copyTree(sourceDir, destDir); err != nil {
		t.Fatalf("copyTree() error = %v", err)
	}

	copiedFile := filepath.Join(destDir, "bin", "tool.sh")
	info, err := os.Stat(copiedFile)
	if err != nil {
		t.Fatalf("Stat(copiedFile) error = %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("copied file mode = %v, want executable bit preserved", info.Mode())
	}
	if !info.ModTime().Equal(wantModTime) {
		t.Fatalf("copied file mod time = %v, want %v", info.ModTime(), wantModTime)
	}
	linkTarget, err := os.Readlink(filepath.Join(destDir, "bin", "tool-link"))
	if err != nil {
		t.Fatalf("Readlink(tool-link) error = %v", err)
	}
	if linkTarget != "tool.sh" {
		t.Fatalf("link target = %q, want %q", linkTarget, "tool.sh")
	}
}
