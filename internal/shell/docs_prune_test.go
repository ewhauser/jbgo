package shell

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDocsDoNotReferenceRemovedShellSeams(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	paths := []string{
		"README.md",
		"SPEC.md",
		"THREAT_MODEL.md",
		"api.go",
	}
	if err := filepath.WalkDir(filepath.Join(root, "website", "content"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if ext := filepath.Ext(path); ext == ".md" || ext == ".mdx" {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			paths = append(paths, rel)
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir(website/content) error = %v", err)
	}

	banned := []string{
		"mvdan/sh-backed shell engine",
		"default mvdan/sh shell engine",
		"internal/shell/mvdan.go",
		"interp.VirtualConfig",
		"interp.GBashConfig",
		"forked runner",
		"delegated to [mvdan/sh",
		"delegated to `mvdan/sh",
		"mvdan/sh execution layer",
	}

	for _, rel := range paths {
		contents, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", rel, err)
		}
		text := string(contents)
		for _, needle := range banned {
			if strings.Contains(text, needle) {
				t.Fatalf("%s still contains stale shell reference %q", rel, needle)
			}
		}
	}
}

func repoRoot(t testing.TB) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
