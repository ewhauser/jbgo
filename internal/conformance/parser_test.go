package conformance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSpecFileParsesMetadataAndCases(t *testing.T) {
	t.Parallel()

	specFile, err := ParseSpecFile("oils/example.test.sh", ""+
		"# comment\n"+
		"## compare_shells: bash\n"+
		"\n"+
		"#### simple case\n"+
		"# preserved\n"+
		"echo hi\n"+
		"## stdout: hi\n"+
		"\n"+
		"#### block expectations\n"+
		"echo one\n"+
		"## STDOUT:\n"+
		"one\n"+
		"## END\n"+
		"# still preserved\n")
	if err != nil {
		t.Fatalf("ParseSpecFile() error = %v", err)
	}
	if got := specFile.Metadata["compare_shells"]; got != "bash" {
		t.Fatalf("compare_shells = %q, want bash", got)
	}
	if len(specFile.Cases) != 2 {
		t.Fatalf("len(Cases) = %d, want 2", len(specFile.Cases))
	}
	if got, want := specFile.Cases[0].Script, "# preserved\necho hi\n\n"; got != want {
		t.Fatalf("case 0 script = %q, want %q", got, want)
	}
	if got, want := specFile.Cases[1].Script, "echo one\n# still preserved\n"; got != want {
		t.Fatalf("case 1 script = %q, want %q", got, want)
	}
}

func TestParseSpecFileCapturesAnnotatedOracleOverrides(t *testing.T) {
	t.Parallel()

	specFile, err := ParseSpecFile("oils/override.test.sh", ""+
		"#### pipeline\n"+
		"echo hi\n"+
		"## STDOUT:\n"+
		"expected\n"+
		"## END\n"+
		"## BUG bash STDOUT:\n"+
		"hello\n"+
		"## END\n"+
		"## OK bash status: 0\n"+
		"echo bye\n")
	if err != nil {
		t.Fatalf("ParseSpecFile() error = %v", err)
	}
	if len(specFile.Cases) != 1 {
		t.Fatalf("len(Cases) = %d, want 1", len(specFile.Cases))
	}
	if got, want := specFile.Cases[0].Script, "echo hi\necho bye\n"; got != want {
		t.Fatalf("script = %q, want %q", got, want)
	}
	if specFile.Cases[0].Expectation.Stdout == nil || *specFile.Cases[0].Expectation.Stdout != "expected\n" {
		t.Fatalf("expected stdout = %#v, want expected\\n", specFile.Cases[0].Expectation.Stdout)
	}
	override := specFile.Cases[0].OracleOverrides[OracleBash]
	if override.Kind != OracleOverrideBug {
		t.Fatalf("bash override kind = %q, want BUG", override.Kind)
	}
	if override.Stdout == nil || *override.Stdout != "hello\n" {
		t.Fatalf("bash stdout override = %#v, want hello\\n", override.Stdout)
	}
	if override.Status == nil || *override.Status != 0 {
		t.Fatalf("bash status override = %#v, want 0", override.Status)
	}
}

func TestParseSpecFileIgnoresUnreachableLinesAfterExit(t *testing.T) {
	t.Parallel()

	specFile, err := ParseSpecFile("oils/exit.test.sh", ""+
		"#### stop\n"+
		"echo before\n"+
		"exit\n"+
		"echo after\n"+
		"## N-I bash STDOUT:\n"+
		"after\n"+
		"## END\n")
	if err != nil {
		t.Fatalf("ParseSpecFile() error = %v", err)
	}
	if got, want := specFile.Cases[0].Script, "echo before\nexit\n"; got != want {
		t.Fatalf("script = %q, want %q", got, want)
	}
	if _, ok := specFile.Cases[0].OracleOverrides[OracleBash]; ok {
		t.Fatalf("unexpected oracle override after exit")
	}
}

func TestLoadSpecFilesFiltersNamedSpecs(t *testing.T) {
	t.Parallel()

	specDir := t.TempDir()
	for _, specName := range []string{"one.test.sh", "two.test.sh"} {
		path := filepath.Join(specDir, specName)
		if err := os.WriteFile(path, []byte("#### case\ntrue\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}

	specFiles, err := LoadSpecFiles(specDir, []string{"two.test.sh"})
	if err != nil {
		t.Fatalf("LoadSpecFiles() error = %v", err)
	}
	if len(specFiles) != 1 {
		t.Fatalf("len(specFiles) = %d, want 1", len(specFiles))
	}
	if got, want := specFiles[0].Path, filepath.ToSlash(filepath.Join(filepath.Base(specDir), "two.test.sh")); got != want {
		t.Fatalf("specFiles[0].Path = %q, want %q", got, want)
	}
}

func TestLoadSpecFilesSkipsExcludedSpecs(t *testing.T) {
	t.Parallel()

	specDir := t.TempDir()
	files := map[string]string{
		"one.test.sh":                 "#### case\ntrue\n",
		"zsh-idioms.test.sh":          "#### case\nfalse\n",
		"toysh-posix.test.sh":         "#### case\nfalse\n",
		"ysh-builtin-private.test.sh": "#### case\nfalse\n",
	}
	for name, contents := range files {
		path := filepath.Join(specDir, name)
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}

	specFiles, err := LoadSpecFiles(specDir, nil)
	if err != nil {
		t.Fatalf("LoadSpecFiles() error = %v", err)
	}
	if len(specFiles) != 1 {
		t.Fatalf("len(specFiles) = %d, want 1", len(specFiles))
	}
	if got, want := specFiles[0].Path, filepath.ToSlash(filepath.Join(filepath.Base(specDir), "one.test.sh")); got != want {
		t.Fatalf("specFiles[0].Path = %q, want %q", got, want)
	}
}
