package builtins_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestNumfmtSupportsScalingAndUnitSizes(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "numfmt 0.1000 10.00\n" +
			"numfmt --from=si 1K 1.1M 0.1G\n" +
			"numfmt --to=iec-i 1024 1153434 107374182\n" +
			"numfmt --from-unit=512 4\n" +
			"numfmt --to-unit=512 2048\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "0.1000\n10.00\n1000\n1100000\n100000000\n1.0Ki\n1.2Mi\n103Mi\n2048\n4\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestNumfmtSupportsHeaderDelimiterFieldsPaddingAndSuffixes(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'head1\\nhead2\\n1K\\n1.1M\\n' | numfmt --header=2 --from=si\n" +
			"printf '1000|2000\\n' | numfmt -d '|' --padding=5 --field=- --to=si\n" +
			"printf '1000 2000 3000\\n' | numfmt --suffix=TEST --field=2\n" +
			"numfmt --suffix=b --unit-separator=' ' --to=si 2000b 500\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "head1\nhead2\n1000\n1100000\n 1.0k| 2.0k\n1000 2000TEST 3000\n2.0 kb\n500b\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestNumfmtSupportsFormatAndRoundingModes(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "numfmt --format=%.1f --round=down 0.99 1 1.01\n" +
			"numfmt --format=%06f --padding=8 1234\n" +
			"numfmt --to-unit=1024 --round=nearest 6000 6000.0 6000.00\n" +
			"numfmt --round=f --to=si -- 9001 -9001\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "0.9\n1.0\n1.0\n  001234\n6\n5.9\n5.86\n9.1k\n-9.1k\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestNumfmtInvalidModesAndArgumentErrors(t *testing.T) {
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name     string
		script   string
		exitCode int
		stdout   string
		stderr   string
		contains string
	}{
		{
			name:     "warn",
			script:   "printf '4Q' | numfmt --invalid=warn\n",
			exitCode: 0,
			stdout:   "4Q",
			stderr:   "numfmt: rejecting suffix in input: '4Q' (consider using --from)\n",
		},
		{
			name:     "fail",
			script:   "printf '4Q' | numfmt --invalid=fail\n",
			exitCode: 2,
			stdout:   "4Q",
			stderr:   "numfmt: rejecting suffix in input: '4Q' (consider using --from)\n",
		},
		{
			name:     "abort",
			script:   "printf '4Q' | numfmt --invalid=abort\n",
			exitCode: 2,
			stdout:   "",
			stderr:   "numfmt: rejecting suffix in input: '4Q' (consider using --from)\n",
		},
		{
			name:     "invalid header",
			script:   "numfmt --header=0\n",
			exitCode: 1,
			stderr:   "numfmt: invalid header value '0'\n",
		},
		{
			name:     "invalid format",
			script:   "numfmt --format='%f %f' 1\n",
			exitCode: 1,
			contains: "format '%f %f' has too many % directives",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := rt.Run(context.Background(), &ExecutionRequest{Script: tc.script})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != tc.exitCode {
				t.Fatalf("ExitCode = %d, want %d; stdout=%q stderr=%q", result.ExitCode, tc.exitCode, result.Stdout, result.Stderr)
			}
			if tc.stdout != "" || result.Stdout != "" {
				if result.Stdout != tc.stdout {
					t.Fatalf("Stdout = %q, want %q", result.Stdout, tc.stdout)
				}
			}
			if tc.stderr != "" || tc.contains == "" {
				if result.Stderr != tc.stderr {
					t.Fatalf("Stderr = %q, want %q", result.Stderr, tc.stderr)
				}
			}
			if tc.contains != "" && !strings.Contains(result.Stderr, tc.contains) {
				t.Fatalf("Stderr = %q, want to contain %q", result.Stderr, tc.contains)
			}
		})
	}
}

func TestNumfmtSupportsZeroTerminatedInputAndEmbeddedNewlines(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "(printf '1000\\000'; printf '2000\\000') | numfmt -z --to=si\n" +
			"(printf '1K\\n2K\\000'; printf '3K\\n4K\\000') | numfmt -z --from=si --field=-\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := []byte("1.0k\x002.0k\x001000 2000\x003000 4000\x00")
	if got := []byte(result.Stdout); !bytes.Equal(got, want) {
		t.Fatalf("Stdout bytes = %v, want %v", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestNumfmtSupportsEmptyDelimiterAndNullByteHandling(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "(printf '1000\\000\\n'; printf '2000\\000') | numfmt\n" +
			"printf '1  K\\n2  M\\n' | numfmt -d '' --from=si --unit-separator='  '\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	want := "1000\n20001000\n2000000\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestNumfmtDebugWarnings(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "numfmt --debug 4096\n" +
			"numfmt --debug --header --to=iec 4096\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	if got, want := result.Stdout, "4096\n4.0K\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "numfmt: no conversion option specified\nnumfmt: --header ignored with command-line input\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}
