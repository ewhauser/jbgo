package awk

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash/commands"
	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

func TestAWKSupportsProgramFilesFieldSeparatorsAndVars(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "printf 'BEGIN { print prefix }\\n{ print $2 }\\n' > /tmp/prog.awk\nprintf 'a,b\\nc,d\\n' > /tmp/in.csv\nawk -F, -v prefix=rows -f /tmp/prog.awk /tmp/in.csv\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "rows\nb\nd\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKExecutesSystemInSandbox(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "awk 'BEGIN { rc = system(\"echo system-ok\"); print \"rc=\" rc }'\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "system-ok\nrc=0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKPreservesMultiFileNRFNRBoundaries(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "printf 'a\\nb\\n' > /data/one.txt\nprintf 'c\\nd\\n' > /data/two.txt\nawk 'NR==FNR { next } { print }' /data/one.txt /data/two.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "c\nd\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKSupportsCSVJoinAcrossFiles(t *testing.T) {
	t.Parallel()

	script := "" +
		"mkdir -p /data\n" +
		"printf 'department_id,department_name\\neng,Engineering\\nmkt,Marketing\\nsales,Sales\\n' > /data/departments.csv\n" +
		"printf 'name,department_id,salary\\nAlice,eng,120000\\nBob,mkt,95000\\nCarol,eng,115000\\nDave,sales,88000\\nEve,mkt,92000\\n' > /data/employees.csv\n" +
		"awk -F, 'BEGIN{OFS=\",\"} NR==FNR { if (FNR>1) dept[$1]=$2; next } FNR==1 { print \"name,department_name,salary\"; next } { print $1, dept[$2], $3 }' /data/departments.csv /data/employees.csv\n"

	result := mustExecAWK(t, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "name,department_name,salary\nAlice,Engineering,120000\nBob,Marketing,95000\nCarol,Engineering,115000\nDave,Sales,88000\nEve,Marketing,92000\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKSupportsCSVModeFlag(t *testing.T) {
	t.Parallel()

	result := runAWKCommand(t, &awkCommandOptions{
		Args:  []string{"-k", "{ print $2 }"},
		Stdin: "a,b\nc,d\n",
	})
	if result.Err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
	}
	if got, want := result.Stdout, "b\nd\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKResetsFilenameAndFNRPerInputFile(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "printf 'a\\nb\\n' > /data/one.txt\nprintf 'c\\nd\\n' > /data/two.txt\nawk '{ print FILENAME \":\" FNR \":\" $0 }' /data/one.txt /data/two.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "/data/one.txt:1:a\n/data/one.txt:2:b\n/data/two.txt:1:c\n/data/two.txt:2:d\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKReadsArbitrarySandboxFilesWithGetline(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "printf 'extra\\n' > /data/extra.txt\nawk 'BEGIN { getline line < \"/data/extra.txt\"; close(\"/data/extra.txt\"); print line }'\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "extra\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKWritesRedirectedOutputToSandboxFiles(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "awk 'BEGIN { print \"hello\" > \"/data/out.txt\"; close(\"/data/out.txt\") }'\ncat /data/out.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKSupportsPipeGetlineAndPipeOutput(t *testing.T) {
	t.Parallel()

	script := "" +
		"printf 'left\\nright\\n' > /data/in.txt\n" +
		"awk 'BEGIN { \"echo pipe-read\" | getline line; close(\"echo pipe-read\"); print line } { print $0 | \"cat > /data/piped.txt\"; close(\"cat > /data/piped.txt\") }' /data/in.txt\n" +
		"cat /data/piped.txt\n"
	result := mustExecAWK(t, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "pipe-read\nright\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKGNUVarARGIND(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "printf 'a\\n' > /data/one.txt\nprintf 'b\\n' > /data/two.txt\nawk '{ print ARGIND \":\" FILENAME \":\" $0 }' /data/one.txt /data/two.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "1:/data/one.txt:a\n2:/data/two.txt:b\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKGNUVarPROCINFOVersion(t *testing.T) {
	t.Parallel()

	result := runAWKCommand(t, &awkCommandOptions{
		Args: []string{`BEGIN { print PROCINFO["version"] }`},
	})
	if result.Err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
	}
	if got, want := result.Stdout, "5.3.2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKGNUVarIGNORECASE(t *testing.T) {
	t.Parallel()

	result := runAWKCommand(t, &awkCommandOptions{
		Args:  []string{`BEGIN { IGNORECASE = 1 } /foo/ { print $0 }`},
		Stdin: "Foo\nbar\n",
	})
	if result.Err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
	}
	if got, want := result.Stdout, "Foo\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKGNUVarFIELDWIDTHS(t *testing.T) {
	t.Parallel()

	result := runAWKCommand(t, &awkCommandOptions{
		Args:  []string{`BEGIN { FIELDWIDTHS = "3 3" } { print $1 "-" $2 "-" $3 }`},
		Stdin: "abc123xyz\n",
	})
	if result.Err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
	}
	if got, want := result.Stdout, "abc-123-\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKGNUVarFPAT(t *testing.T) {
	t.Parallel()

	result := runAWKCommand(t, &awkCommandOptions{
		Args:  []string{`BEGIN { FPAT = "[[:alpha:]]+|[0-9]+" } { print NF ":" $1 ":" $2 ":" $3 ":" $4 }`},
		Stdin: "a=1 b=22\n",
	})
	if result.Err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
	}
	if got, want := result.Stdout, "4:a:1:b:22\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKSupportsGNUTimeFunctions(t *testing.T) {
	t.Parallel()

	fixedNow := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	result := runAWKCommand(t, &awkCommandOptions{
		Args: []string{`BEGIN { print systime(); print strftime("%Y-%m-%d %H:%M:%S", 0, 1); print mktime("1970 01 02 00 00 00") }`},
		Env:  map[string]string{"TZ": "UTC"},
		Now:  fixedNow,
	})
	if result.Err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
	}
	want := "1704164645\n1970-01-01 00:00:00\n86400\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKSupportsGNUStringAndBitwiseFunctions(t *testing.T) {
	t.Parallel()

	result := runAWKCommand(t, &awkCommandOptions{
		Args: []string{`BEGIN {
			print gensub("([0-9]+)", "<\\1>", "g", "item42 batch7")
			print strtonum("0x10"), strtonum("010"), strtonum("1.5")
			print and(6, 3), or(6, 3), xor(6, 3), compl(0), lshift(3, 2), rshift(8, 2)
		}`},
	})
	if result.Err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
	}
	want := "item<42> batch<7>\n16 8 1.5\n2 7 5 9007199254740991 12 2\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKSupportsOrderedProgramSources(t *testing.T) {
	t.Parallel()

	result := runAWKCommand(t, &awkCommandOptions{
		Args: []string{
			`--source=BEGIN { print "source" }`,
			`--file=/main.awk`,
			`--include=/include.awk`,
		},
		Files: map[string]string{
			"/main.awk":    `BEGIN { print "file" }`,
			"/include.awk": `BEGIN { print "include" }`,
		},
	})
	if result.Err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
	}
	if got, want := result.Stdout, "source\nfile\ninclude\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKExecOptionDisablesArgVarAssignments(t *testing.T) {
	t.Parallel()

	result := runAWKCommand(t, &awkCommandOptions{
		Args: []string{"-E", "/prog.awk", "name=value", "/input.txt"},
		Files: map[string]string{
			"/prog.awk":  `BEGIN { print ARGV[1], ARGV[2] }`,
			"/input.txt": "x\n",
		},
	})
	if result.Err != nil {
		t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
	}
	if got, want := result.Stdout, "name=value /input.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestAWKSupportsGNUInfoAndLongOptions(t *testing.T) {
	t.Parallel()

	t.Run("help", func(t *testing.T) {
		t.Parallel()
		result := runAWKCommand(t, &awkCommandOptions{Args: []string{"--help"}})
		if result.Err != nil {
			t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
		}
		if !strings.Contains(result.Stdout, "Usage: awk") {
			t.Fatalf("Stdout = %q, want GNU help text", result.Stdout)
		}
	})

	t.Run("version", func(t *testing.T) {
		t.Parallel()
		result := runAWKCommand(t, &awkCommandOptions{Args: []string{"-W", "version"}})
		if result.Err != nil {
			t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
		}
		if !strings.HasPrefix(result.Stdout, "GNU Awk 5.3.2") {
			t.Fatalf("Stdout = %q, want GNU version text", result.Stdout)
		}
	})

	t.Run("copyright", func(t *testing.T) {
		t.Parallel()
		result := runAWKCommand(t, &awkCommandOptions{Args: []string{"-C"}})
		if result.Err != nil {
			t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
		}
		if !strings.Contains(result.Stdout, "Free Software Foundation") {
			t.Fatalf("Stdout = %q, want copyright text", result.Stdout)
		}
	})

	t.Run("long assign", func(t *testing.T) {
		t.Parallel()
		result := runAWKCommand(t, &awkCommandOptions{
			Args: []string{"--assign=prefix=hi", `BEGIN { print prefix }`},
		})
		if result.Err != nil {
			t.Fatalf("Run() error = %v; stderr=%q", result.Err, result.Stderr)
		}
		if got, want := result.Stdout, "hi\n"; got != want {
			t.Fatalf("Stdout = %q, want %q", got, want)
		}
	})

	t.Run("unsupported load", func(t *testing.T) {
		t.Parallel()
		result := runAWKCommand(t, &awkCommandOptions{Args: []string{"-l", "json"}})
		if result.ExitCode != 2 {
			t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "not supported") {
			t.Fatalf("Stderr = %q, want unsupported diagnostic", result.Stderr)
		}
	})

	t.Run("unsupported debug", func(t *testing.T) {
		t.Parallel()
		result := runAWKCommand(t, &awkCommandOptions{Args: []string{"--debug"}})
		if result.ExitCode != 2 {
			t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "not supported") {
			t.Fatalf("Stderr = %q, want unsupported diagnostic", result.Stderr)
		}
	})
}

func TestAWKMissingFileReportsGNUStyleFailure(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "awk '{ print }' missing.txt\n")
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "missing.txt") {
		t.Fatalf("Stderr = %q, want missing input path", result.Stderr)
	}
}

func TestAWKParseErrorsUseUpstreamExitCode(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "awk 'BEGIN {'\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "parse error") {
		t.Fatalf("Stderr = %q, want parse error", result.Stderr)
	}
}

func TestLazyAWKStdinReadsOnDemand(t *testing.T) {
	t.Parallel()

	stdin := &trackingStdinReader{reader: strings.NewReader("stdin-data")}
	inv := commands.NewInvocation(&commands.InvocationOptions{
		Cwd:        "/",
		Stdin:      stdin,
		FileSystem: gbfs.NewMemory(),
		Policy:     policy.NewStatic(&policy.Config{}),
	})

	reader := newLazyAWKStdin(context.Background(), inv)
	if stdin.reads != 0 {
		t.Fatalf("stdin reads before use = %d, want 0", stdin.reads)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if got := string(data); got != "stdin-data" {
		t.Fatalf("stdin = %q, want %q", got, "stdin-data")
	}
	if stdin.reads == 0 {
		t.Fatal("stdin was not read on demand")
	}
}

type unexpectedStdinReader struct {
	reads int
}

func (r *unexpectedStdinReader) Read(_ []byte) (int, error) {
	r.reads++
	return 0, errors.New("unexpected stdin read")
}

func TestAWKMissingFileBeforeDashDoesNotReadStdin(t *testing.T) {
	t.Parallel()

	stdin := &unexpectedStdinReader{}
	inv := commands.NewInvocation(&commands.InvocationOptions{
		Args:       []string{"{ print }", "/data/missing.txt", "-"},
		Cwd:        "/",
		Stdin:      stdin,
		Stdout:     io.Discard,
		Stderr:     io.Discard,
		FileSystem: gbfs.NewMemory(),
		Policy:     policy.NewStatic(&policy.Config{}),
	})

	err := NewAWK().Run(context.Background(), inv)
	if err == nil {
		t.Fatal("Run() error = nil, want missing-file failure")
	}
	if stdin.reads != 0 {
		t.Fatalf("stdin reads = %d, want 0", stdin.reads)
	}
	if !strings.Contains(err.Error(), "/data/missing.txt") {
		t.Fatalf("error = %v, want missing input path", err)
	}
}

type trackingStdinReader struct {
	reader io.Reader
	reads  int
}

func (r *trackingStdinReader) Read(p []byte) (int, error) {
	r.reads++
	return r.reader.Read(p)
}
