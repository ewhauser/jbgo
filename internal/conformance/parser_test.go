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
	if len(specFile.CompareShells) != 1 || specFile.CompareShells[0] != OracleBash {
		t.Fatalf("CompareShells = %#v, want [bash]", specFile.CompareShells)
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

func TestParseSpecFileCanonicalizesMultiShellOverrides(t *testing.T) {
	t.Parallel()

	specFile, err := ParseSpecFile("oils/override.test.sh", ""+
		"## compare_shells: bash-4.4 dash mksh zsh-5.9\n"+
		"#### pipeline\n"+
		"echo hi\n"+
		"## OK-2 dash/mksh STDOUT:\n"+
		"portable\n"+
		"## END\n"+
		"## BUG zsh-5.9 status: 1\n")
	if err != nil {
		t.Fatalf("ParseSpecFile() error = %v", err)
	}

	if got, want := specFile.CompareShells, []OracleMode{OracleBash, OracleDash, OracleMksh, OracleZsh}; !equalOracleModes(got, want) {
		t.Fatalf("CompareShells = %#v, want %#v", got, want)
	}

	for _, mode := range []OracleMode{OracleDash, OracleMksh} {
		override, ok := specFile.Cases[0].OracleOverrides[mode]
		if !ok {
			t.Fatalf("missing override for %s", mode)
		}
		if override.Kind != OracleOverrideOK {
			t.Fatalf("%s kind = %q, want OK", mode, override.Kind)
		}
		if override.Stdout == nil || *override.Stdout != "portable\n" {
			t.Fatalf("%s stdout = %#v, want portable\\n", mode, override.Stdout)
		}
	}

	zsh := specFile.Cases[0].OracleOverrides[OracleZsh]
	if zsh.Status == nil || *zsh.Status != 1 {
		t.Fatalf("zsh status = %#v, want 1", zsh.Status)
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

	specFiles, err := LoadSpecFiles(specDir, []string{"two.test.sh"}, OracleBash)
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

func TestLoadSpecFilesFiltersByCompareShells(t *testing.T) {
	t.Parallel()

	specDir := t.TempDir()
	files := map[string]string{
		"bash.test.sh": "## compare_shells: bash-4.4\n#### case\ntrue\n",
		"dash.test.sh": "## compare_shells: dash mksh\n#### case\ntrue\n",
		"any.test.sh":  "#### case\ntrue\n",
	}
	for name, contents := range files {
		path := filepath.Join(specDir, name)
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}

	specFiles, err := LoadSpecFiles(specDir, nil, OracleDash)
	if err != nil {
		t.Fatalf("LoadSpecFiles() error = %v", err)
	}
	if len(specFiles) != 2 {
		t.Fatalf("len(specFiles) = %d, want 2", len(specFiles))
	}
	if got, want := specFiles[0].Path, filepath.ToSlash(filepath.Join(filepath.Base(specDir), "any.test.sh")); got != want {
		t.Fatalf("specFiles[0].Path = %q, want %q", got, want)
	}
	if got, want := specFiles[1].Path, filepath.ToSlash(filepath.Join(filepath.Base(specDir), "dash.test.sh")); got != want {
		t.Fatalf("specFiles[1].Path = %q, want %q", got, want)
	}
}

func equalOracleModes(got, want []OracleMode) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
