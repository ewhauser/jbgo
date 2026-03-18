package shell

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestNonInterpPackagesDoNotReferencePrunedInterpBoundary(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	roots := []string{
		filepath.Join(root, "internal", "shell"),
		filepath.Join(root, "internal", "runtime"),
		filepath.Join(root, "examples"),
	}
	bannedSubstrings := []string{
		"interp.HandlerCtx(",
		"ReadDirHandler2",
		"ReadDirHandlerFunc2",
		"ReadDir2",
		"VirtualPipeReader",
		"VirtualPipeWriter",
		"interp.NewExitStatus(",
		"interp.IsExitStatus(",
	}
	bannedRegexps := []*regexp.Regexp{
		regexp.MustCompile(`\.Vars\b`),
		regexp.MustCompile(`\.Funcs\b`),
	}

	for _, base := range roots {
		if err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if strings.HasPrefix(path, filepath.Join(root, "internal", "shell", "interp")) {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(contents)
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			for _, needle := range bannedSubstrings {
				if strings.Contains(text, needle) {
					t.Fatalf("%s still references pruned interp boundary %q", rel, needle)
				}
			}
			for _, re := range bannedRegexps {
				if re.MatchString(text) {
					t.Fatalf("%s still references pruned interp boundary pattern %q", rel, re.String())
				}
			}
			return nil
		}); err != nil {
			t.Fatalf("WalkDir(%s) error = %v", base, err)
		}
	}
}
