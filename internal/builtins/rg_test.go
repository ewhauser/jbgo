package builtins_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/policy"
)

func TestRGBasicSearchMatchesUpstreamDefaults(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'Hello\\nhello\\n' > a.txt\nprintf 'hello\\n' > b.txt\nrg hello\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a.txt:1:Hello\na.txt:2:hello\nb.txt:1:hello\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRGSingleExplicitFileSuppressesPrefixesByDefault(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'needle\\n' > file.txt\nrg needle file.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "needle\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRGSupportsSmartCaseAndCaseFlags(t *testing.T) {
	session := newSession(t, &Config{})
	mustExecSession(t, session, "printf 'Hello\\nhello\\n' > file.txt\n")

	upper := mustExecSession(t, session, "rg Hello file.txt\n")
	if got, want := upper.Stdout, "Hello\n"; got != want {
		t.Fatalf("uppercase smart-case stdout = %q, want %q", got, want)
	}

	ignoreCase := mustExecSession(t, session, "rg -i Hello file.txt\n")
	if got, want := ignoreCase.Stdout, "Hello\nhello\n"; got != want {
		t.Fatalf("-i stdout = %q, want %q", got, want)
	}

	caseSensitive := mustExecSession(t, session, "rg -s hello file.txt\n")
	if got, want := caseSensitive.Stdout, "hello\n"; got != want {
		t.Fatalf("-s stdout = %q, want %q", got, want)
	}
}

func TestRGHiddenIgnoreAndUnrestrictedModes(t *testing.T) {
	session := newSession(t, &Config{})
	mustExecSession(t, session, ""+
		"printf '*.log\\n' > .gitignore\n"+
		"printf 'needle\\n' > app.ts\n"+
		"printf 'needle\\n' > debug.log\n"+
		"printf 'needle\\n' > .hidden.txt\n")

	defaultSearch := mustExecSession(t, session, "rg needle\n")
	if got, want := defaultSearch.Stdout, "app.ts:1:needle\n"; got != want {
		t.Fatalf("default stdout = %q, want %q", got, want)
	}

	noIgnore := mustExecSession(t, session, "rg --no-ignore needle\n")
	if got, want := noIgnore.Stdout, "app.ts:1:needle\ndebug.log:1:needle\n"; got != want {
		t.Fatalf("--no-ignore stdout = %q, want %q", got, want)
	}

	unrestricted := mustExecSession(t, session, "rg -uu needle\n")
	if got, want := unrestricted.Stdout, ".hidden.txt:1:needle\napp.ts:1:needle\ndebug.log:1:needle\n"; got != want {
		t.Fatalf("-uu stdout = %q, want %q", got, want)
	}
}

func TestRGSupportsGlobFilteringAndFilesMode(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"printf 'foo\\n' > app.ts\n" +
			"printf 'foo\\n' > test.ts\n" +
			"printf 'foo\\n' > app.js\n" +
			"rg -g '*.ts' -g '!test.ts' foo\n" +
			"printf '%s' $?\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "app.ts:1:foo\n0"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	files, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"printf 'foo\\n' > a.txt\n" +
			"printf 'foo\\n' > b.md\n" +
			"rg --files -g '*.txt'\n",
	})
	if err != nil {
		t.Fatalf("Run(files) error = %v", err)
	}
	if got, want := files.Stdout, "a.txt\n"; got != want {
		t.Fatalf("--files stdout = %q, want %q", got, want)
	}
}

func TestRGSupportsMaxDepth(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "" +
			"printf 'hello\\n' > level0.txt\n" +
			"mkdir -p dir1/dir2\n" +
			"printf 'hello\\n' > dir1/level1.txt\n" +
			"printf 'hello\\n' > dir1/dir2/level2.txt\n" +
			"rg --max-depth 2 hello\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "dir1/level1.txt:1:hello\nlevel0.txt:1:hello\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRGSupportsOutputModesAndContext(t *testing.T) {
	session := newSession(t, &Config{})
	mustExecSession(t, session, ""+
		"printf 'alpha\\nhello\\nbravo\\n' > a.txt\n"+
		"printf 'world\\n' > b.txt\n"+
		"printf '123 456\\n' > digits.txt\n")

	listFiles := mustExecSession(t, session, "rg -l hello\n")
	if got, want := listFiles.Stdout, "a.txt\n"; got != want {
		t.Fatalf("-l stdout = %q, want %q", got, want)
	}

	filesWithout := mustExecSession(t, session, "rg --files-without-match hello\n")
	if got, want := filesWithout.Stdout, "b.txt\ndigits.txt\n"; got != want {
		t.Fatalf("--files-without-match stdout = %q, want %q", got, want)
	}

	onlyMatching := mustExecSession(t, session, "rg -o '[0-9]+'\n")
	if got, want := onlyMatching.Stdout, "digits.txt:123\ndigits.txt:456\n"; got != want {
		t.Fatalf("-o stdout = %q, want %q", got, want)
	}

	contextSearch := mustExecSession(t, session, "rg -C1 hello\n")
	if got, want := contextSearch.Stdout, "a.txt-1-alpha\na.txt:2:hello\na.txt-3-bravo\n"; got != want {
		t.Fatalf("-C stdout = %q, want %q", got, want)
	}

	quiet := mustExecSession(t, session, "rg -q hello\n")
	if quiet.ExitCode != 0 || quiet.Stdout != "" || quiet.Stderr != "" {
		t.Fatalf("-q result = %+v, want exit 0 with empty output", quiet)
	}
}

func TestRGSupportsMatchModeFlagsAndMaxCount(t *testing.T) {
	session := newSession(t, &Config{})
	mustExecSession(t, session, ""+
		"printf 'a.c\\nabc\\n' > fixed.txt\n"+
		"printf 'foo\\nfoobar\\n' > words.txt\n"+
		"printf 'foo\\nfoo bar\\n' > lines.txt\n"+
		"printf 'hit\\nmiss\\nhit\\n' > invert.txt\n"+
		"printf 'hit\\nhit\\nhit\\n' > count.txt\n")

	fixed := mustExecSession(t, session, "rg -F 'a.c' fixed.txt\n")
	if got, want := fixed.Stdout, "a.c\n"; got != want {
		t.Fatalf("-F stdout = %q, want %q", got, want)
	}

	word := mustExecSession(t, session, "rg -w foo words.txt\n")
	if got, want := word.Stdout, "foo\n"; got != want {
		t.Fatalf("-w stdout = %q, want %q", got, want)
	}

	line := mustExecSession(t, session, "rg -x foo lines.txt\n")
	if got, want := line.Stdout, "foo\n"; got != want {
		t.Fatalf("-x stdout = %q, want %q", got, want)
	}

	invert := mustExecSession(t, session, "rg -v hit invert.txt\n")
	if got, want := invert.Stdout, "miss\n"; got != want {
		t.Fatalf("-v stdout = %q, want %q", got, want)
	}

	maxCount := mustExecSession(t, session, "rg -m1 hit count.txt\n")
	if got, want := maxCount.Stdout, "hit\n"; got != want {
		t.Fatalf("-m stdout = %q, want %q", got, want)
	}
}

func TestRGSupportsPatternFilesFilenameFlagsAndIglob(t *testing.T) {
	session := newSession(t, &Config{})
	mustExecSession(t, session, ""+
		"printf 'foo\\nbar\\n' > patterns.txt\n"+
		"printf 'foo\\nbar\\nbaz\\n' > data.txt\n"+
		"printf 'hello\\n' > APP.TS\n"+
		"printf 'hello\\n' > app.js\n")

	patternFile := mustExecSession(t, session, "rg -f patterns.txt data.txt\n")
	if got, want := patternFile.Stdout, "foo\nbar\n"; got != want {
		t.Fatalf("-f stdout = %q, want %q", got, want)
	}

	withFilename := mustExecSession(t, session, "rg -H -N hello APP.TS\n")
	if got, want := withFilename.Stdout, "APP.TS:hello\n"; got != want {
		t.Fatalf("-H -N stdout = %q, want %q", got, want)
	}

	iglob := mustExecSession(t, session, "rg --iglob '*.ts' hello\n")
	if got, want := iglob.Stdout, "APP.TS:1:hello\n"; got != want {
		t.Fatalf("--iglob stdout = %q, want %q", got, want)
	}
}

func TestRGSupportsTypeFilteringAndTypeList(t *testing.T) {
	session := newSession(t, &Config{})
	mustExecSession(t, session, ""+
		"printf 'foo\\n' > app.ts\n"+
		"printf 'foo\\n' > app.js\n")

	typeOnly := mustExecSession(t, session, "rg -t ts foo\n")
	if got, want := typeOnly.Stdout, "app.ts:1:foo\n"; got != want {
		t.Fatalf("-t stdout = %q, want %q", got, want)
	}

	typeExclude := mustExecSession(t, session, "rg -T ts foo\n")
	if got, want := typeExclude.Stdout, "app.js:1:foo\n"; got != want {
		t.Fatalf("-T stdout = %q, want %q", got, want)
	}

	typeList := mustExecSession(t, session, "rg --type-list\n")
	if typeList.ExitCode != 0 {
		t.Fatalf("--type-list exit = %d, want 0; stderr=%q", typeList.ExitCode, typeList.Stderr)
	}
	for _, want := range []string{"js: *.js", "ts: *.ts", "py: *.py"} {
		if !strings.Contains(typeList.Stdout, want) {
			t.Fatalf("--type-list stdout = %q, want substring %q", typeList.Stdout, want)
		}
	}
}

func TestRGSupportsBinaryFilesAndFollowLinks(t *testing.T) {
	session := newSession(t, &Config{})
	mustExecSession(t, session, ""+
		"printf 'hello\\000world\\n' > binary.bin\n"+
		"printf 'hello\\n' > real.txt\n")

	defaultBinary := mustExecSession(t, session, "rg hello binary.bin\n")
	if defaultBinary.ExitCode != 1 || defaultBinary.Stdout != "" {
		t.Fatalf("default binary result = %+v, want exit 1 and empty stdout", defaultBinary)
	}

	textBinary := mustExecSession(t, session, "rg -a hello binary.bin\n")
	if got, want := textBinary.Stdout, "hello\x00world\n"; got != want {
		t.Fatalf("-a stdout = %q, want %q", got, want)
	}
}

func TestRGFollowLinksHonorsFollowEnabledPolicy(t *testing.T) {
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			SymlinkMode: policy.SymlinkFollow,
		}),
	})
	mustExecSession(t, session, ""+
		"printf 'hello\\n' > real.txt\n"+
		"ln -s real.txt link.txt\n")

	defaultLinks := mustExecSession(t, session, "rg hello\n")
	if got, want := defaultLinks.Stdout, "real.txt:1:hello\n"; got != want {
		t.Fatalf("default link stdout = %q, want %q", got, want)
	}

	followLinks := mustExecSession(t, session, "rg -L hello\n")
	if got, want := followLinks.Stdout, "link.txt:1:hello\nreal.txt:1:hello\n"; got != want {
		t.Fatalf("-L stdout = %q, want %q", got, want)
	}
}
