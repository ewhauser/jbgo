package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestCutSupportsFieldRangesAndDefaultDelimiter(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo 'root:x:0:0:root:/root:/bin/bash' > /tmp/passwd.txt\n echo 'user:x:1000:1000:User:/home/user:/bin/zsh' >> /tmp/passwd.txt\n cut -d: -f1,3- /tmp/passwd.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "root:0:0:root:/root:/bin/bash\nuser:1000:1000:User:/home/user:/bin/zsh\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCutSupportsCharactersAndSuppressNoDelimiter(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo 'hello world' | cut -c1,3,5\n echo 'a:b' > /tmp/in.txt\n echo 'plain' >> /tmp/in.txt\n cut -s -d: -f2 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hlo\nb\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCutSupportsLongOnlyDelimitedFlag(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'left:right\\nplain\\n' > /tmp/in.txt\ncut --only-delimited -d: -f2 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "right\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCutRequiresFieldOrCharacterSelector(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "cut /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "must specify") {
		t.Fatalf("Stderr = %q, want selector error", result.Stderr)
	}
}

func TestSedSupportsSubstituteDeleteAndAddressPrinting(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo 'hello world' > /tmp/in.txt\n echo 'hello universe' >> /tmp/in.txt\n echo 'goodbye world' >> /tmp/in.txt\n sed 's/hello/hi/' /tmp/in.txt\n sed '/hello/d' /tmp/in.txt\n sed -n '2p' /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hi world\nhi universe\ngoodbye world\ngoodbye world\nhello universe\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSedSupportsMultipleExpressionsInPlaceAndQuit(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo '/path/to/file' > /tmp/in.txt\n echo 'keep me' >> /tmp/in.txt\n echo 'drop me' >> /tmp/in.txt\n sed -i -e 's#/path#/newpath#' -e '3d' /tmp/in.txt\n cat /tmp/in.txt\n sed '2q' /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/newpath/to/file\nkeep me\n/newpath/to/file\nkeep me\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSedSupportsRangeGlobalAndIgnoreCaseSubstitution(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo 'hello world' > /tmp/in.txt\n echo 'Hello HELLO hello' >> /tmp/in.txt\n echo 'HELLO there' >> /tmp/in.txt\n sed '2,3s/hello/hi/gi' /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello world\nhi hi hi\nhi there\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSedReturnsMissingFileError(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "sed 's/a/b/' /missing.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "/missing.txt") {
		t.Fatalf("Stderr = %q, want missing-file error", result.Stderr)
	}
}

func TestSedSupportsScriptFileFlag(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 's/foo/bar/\\n2p\\n' > /tmp/script.sed\nprintf 'foo\\nfoo\\n' > /tmp/in.txt\nsed -f /tmp/script.sed /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "bar\nbar\nbar\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
