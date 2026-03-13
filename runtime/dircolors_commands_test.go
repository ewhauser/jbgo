package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestDircolorsParsesStdinAndEscapesShellOutput(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'owt 40;33\n' | dircolors -b -\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "LS_COLORS='tw=40;33:';\nexport LS_COLORS\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestDircolorsSupportsShellDetectionDatabaseAndDisplayModes(t *testing.T) {
	rt := newRuntime(t, &Config{})

	t.Run("shell detection", func(t *testing.T) {
		result, err := rt.Run(context.Background(), &ExecutionRequest{
			Script: "dircolors\n",
			Env:    map[string]string{"SHELL": "/bin/tcsh"},
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
		}
		if !strings.HasPrefix(result.Stdout, "setenv LS_COLORS 'rs=0:") {
			t.Fatalf("Stdout = %q, want csh-formatted LS_COLORS output", result.Stdout)
		}
	})

	t.Run("print database", func(t *testing.T) {
		result, err := rt.Run(context.Background(), &ExecutionRequest{
			Script: "dircolors --print-database\n",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
		}
		for _, want := range []string{
			"# Configuration file for dircolors",
			"COLORTERM ?*",
			"TERM screen*",
			"DIR 01;34",
			".tar 01;31",
		} {
			if !strings.Contains(result.Stdout, want) {
				t.Fatalf("Stdout missing %q", want)
			}
		}
	})

	t.Run("print ls colors", func(t *testing.T) {
		result, err := rt.Run(context.Background(), &ExecutionRequest{
			Script: "dircolors --print-ls-colors | head -n 2\n",
		})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
		}
		if got, want := result.Stdout, "\x1b[0mrs\t0\x1b[0m\n\x1b[01;34mdi\t01;34\x1b[0m\n"; got != want {
			t.Fatalf("Stdout = %q, want %q", got, want)
		}
	})
}

func TestDircolorsTermAndColortermFiltering(t *testing.T) {
	rt := newRuntime(t, &Config{})

	termMatch, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'TERM [!a]_negation\n.term_matching 00;38;5;61\n' | dircolors -b -\n",
		Env:    map[string]string{"TERM": "b_negation"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if termMatch.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", termMatch.ExitCode, termMatch.Stderr)
	}
	if got, want := termMatch.Stdout, "LS_COLORS='*.term_matching=00;38;5;61:';\nexport LS_COLORS\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	colortermMiss, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'COLORTERM ?*\nowt 40;33\n' | dircolors -b -\n",
		Env:    map[string]string{"COLORTERM": ""},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if colortermMiss.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", colortermMiss.ExitCode, colortermMiss.Stderr)
	}
	if got, want := colortermMiss.Stdout, "LS_COLORS='';\nexport LS_COLORS\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDircolorsHelpVersionAndErrors(t *testing.T) {
	rt := newRuntime(t, &Config{})

	tests := []struct {
		name       string
		script     string
		env        map[string]string
		wantCode   int
		wantOut    string
		wantPrefix string
		wantStderr string
	}{
		{
			name:       "help",
			script:     "dircolors --help\n",
			wantCode:   0,
			wantPrefix: "Output commands to set the LS_COLORS environment variable.\n\nUsage: dircolors [OPTION]... [FILE]\n",
		},
		{
			name:     "version",
			script:   "dircolors --version\n",
			wantCode: 0,
			wantOut:  "dircolors (gbash)\n",
		},
		{
			name:       "invalid long option",
			script:     "dircolors --definitely-invalid\n",
			wantCode:   1,
			wantStderr: "dircolors: unrecognized option '--definitely-invalid'\nTry 'dircolors --help' for more information.\n",
		},
		{
			name:       "invalid short option",
			script:     "dircolors -x\n",
			wantCode:   1,
			wantStderr: "dircolors: invalid option -- 'x'\nTry 'dircolors --help' for more information.\n",
		},
		{
			name:       "exclusive options",
			script:     "dircolors -bp\n",
			wantCode:   1,
			wantStderr: "dircolors: options to output shell code and options to print other output are mutually exclusive\n",
		},
		{
			name:       "no shell env",
			script:     "dircolors\n",
			env:        map[string]string{"SHELL": ""},
			wantCode:   1,
			wantStderr: "dircolors: no SHELL environment variable, and no shell type option given\n",
		},
		{
			name:       "extra operand",
			script:     "dircolors -c file1 file2\n",
			wantCode:   1,
			wantStderr: "dircolors: extra operand 'file2'\n",
		},
		{
			name:       "directory operand",
			script:     "mkdir -p /tmp/dir\ndircolors -c /tmp/dir\n",
			wantCode:   2,
			wantStderr: "dircolors: expected file, got directory '/tmp/dir'\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := rt.Run(context.Background(), &ExecutionRequest{
				Script: tc.script,
				Env:    tc.env,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.ExitCode != tc.wantCode {
				t.Fatalf("ExitCode = %d, want %d; stderr=%q", result.ExitCode, tc.wantCode, result.Stderr)
			}
			if tc.wantPrefix != "" {
				if got := result.Stdout; !strings.HasPrefix(got, tc.wantPrefix) {
					t.Fatalf("Stdout = %q, want prefix %q", got, tc.wantPrefix)
				}
			} else if got := result.Stdout; got != tc.wantOut {
				t.Fatalf("Stdout = %q, want %q", got, tc.wantOut)
			}
			if got := result.Stderr; got != tc.wantStderr {
				t.Fatalf("Stderr = %q, want %q", got, tc.wantStderr)
			}
		})
	}
}
