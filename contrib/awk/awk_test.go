package awk

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

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

func TestAWKDisablesExec(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "awk 'BEGIN { system(\"echo nope\") }'\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "NoExec") && !strings.Contains(result.Stderr, "can't") {
		t.Fatalf("Stderr = %q, want sandbox execution denial", result.Stderr)
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

func TestAWKBlocksNonAllowlistedGetlineReads(t *testing.T) {
	t.Parallel()

	result := mustExecAWK(t, "printf 'a\\n' > /data/one.txt\nawk 'BEGIN { getline line < \"/etc/passwd\"; print line }' /data/one.txt\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "can't read from file due to NoFileReads") {
		t.Fatalf("Stderr = %q, want file-read denial", result.Stderr)
	}
}

func TestLoadAWKInputsSkipsStdinWhenNamedFilesProvided(t *testing.T) {
	t.Parallel()

	mem := gbfs.NewMemory()
	file, err := mem.OpenFile(context.Background(), "/data/one.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := file.Write([]byte("a\n")); err != nil {
		_ = file.Close()
		t.Fatalf("Write() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	stdin := &unexpectedStdinReader{}
	inv := commands.NewInvocation(&commands.InvocationOptions{
		Cwd:        "/",
		Stdin:      stdin,
		FileSystem: mem,
		Policy:     policy.NewStatic(&policy.Config{}),
	})

	inputs, err := loadAWKInputs(context.Background(), inv, []string{"/data/one.txt"})
	if err != nil {
		t.Fatalf("loadAWKInputs() error = %v", err)
	}
	if stdin.reads != 0 {
		t.Fatalf("stdin reads = %d, want 0", stdin.reads)
	}
	if got := string(inputs.Stdin); got != "" {
		t.Fatalf("stdin = %q, want empty", got)
	}
	if got := string(inputs.Files["/data/one.txt"]); got != "a\n" {
		t.Fatalf("file contents = %q, want %q", got, "a\n")
	}
}

func TestLoadAWKInputsReadsStdinWhenOnlyArgAssignmentsRemain(t *testing.T) {
	t.Parallel()

	inv := commands.NewInvocation(&commands.InvocationOptions{
		Cwd:        "/",
		Stdin:      strings.NewReader("stdin-data"),
		FileSystem: gbfs.NewMemory(),
		Policy:     policy.NewStatic(&policy.Config{}),
	})

	inputs, err := loadAWKInputs(context.Background(), inv, []string{"prefix=1"})
	if err != nil {
		t.Fatalf("loadAWKInputs() error = %v", err)
	}
	if got := string(inputs.Stdin); got != "stdin-data" {
		t.Fatalf("stdin = %q, want %q", got, "stdin-data")
	}
	if len(inputs.Files) != 0 {
		t.Fatalf("files = %d, want 0", len(inputs.Files))
	}
}

type unexpectedStdinReader struct {
	reads int
}

func (r *unexpectedStdinReader) Read(_ []byte) (int, error) {
	r.reads++
	return 0, errors.New("unexpected stdin read")
}
