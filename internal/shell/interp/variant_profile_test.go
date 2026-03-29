package interp

import (
	"context"
	"slices"
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
	"github.com/ewhauser/gbash/shellvariant"
)

func TestRunnerParserLangUsesShellVariantProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		variant shellvariant.ShellVariant
		want    syntax.LangVariant
	}{
		{variant: shellvariant.Bash, want: syntax.LangBash},
		{variant: shellvariant.SH, want: syntax.LangPOSIX},
		{variant: shellvariant.Mksh, want: syntax.LangMirBSDKorn},
		{variant: shellvariant.Zsh, want: syntax.LangZsh},
		{variant: shellvariant.Bats, want: syntax.LangBats},
	}

	for _, tc := range tests {
		t.Run(string(tc.variant), func(t *testing.T) {
			t.Parallel()

			runner, err := NewRunner(&RunnerConfig{
				Dir:          "/tmp",
				ShellVariant: tc.variant,
			})
			if err != nil {
				t.Fatalf("NewRunner() error = %v", err)
			}
			if got := runner.parserLangVariant(); got != tc.want {
				t.Fatalf("parserLangVariant() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunnerCompletionBackendRespectsShellVariantBuiltins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		variant       shellvariant.ShellVariant
		wantShopt     bool
		wantDirs      bool
		wantMapfile   bool
		wantShoptOpts bool
	}{
		{
			name:          "sh",
			variant:       shellvariant.SH,
			wantShopt:     false,
			wantDirs:      false,
			wantMapfile:   false,
			wantShoptOpts: false,
		},
		{
			name:          "mksh",
			variant:       shellvariant.Mksh,
			wantShopt:     false,
			wantDirs:      false,
			wantMapfile:   false,
			wantShoptOpts: false,
		},
		{
			name:          "zsh",
			variant:       shellvariant.Zsh,
			wantShopt:     false,
			wantDirs:      true,
			wantMapfile:   false,
			wantShoptOpts: false,
		},
		{
			name:          "bats",
			variant:       shellvariant.Bats,
			wantShopt:     true,
			wantDirs:      false,
			wantMapfile:   true,
			wantShoptOpts: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runner, err := NewRunner(&RunnerConfig{
				Dir:          "/tmp",
				ShellVariant: tc.variant,
			})
			if err != nil {
				t.Fatalf("NewRunner() error = %v", err)
			}
			backend := newRunnerCompletionBackend(context.Background(), runner, nil)

			builtins := backend.EnabledBuiltinNames("")
			if got := slices.Contains(builtins, "shopt"); got != tc.wantShopt {
				t.Fatalf("EnabledBuiltinNames contains shopt = %v, want %v", got, tc.wantShopt)
			}
			if got := slices.Contains(builtins, "dirs"); got != tc.wantDirs {
				t.Fatalf("EnabledBuiltinNames contains dirs = %v, want %v", got, tc.wantDirs)
			}
			if got := slices.Contains(builtins, "mapfile"); got != tc.wantMapfile {
				t.Fatalf("EnabledBuiltinNames contains mapfile = %v, want %v", got, tc.wantMapfile)
			}
			if got := len(backend.ShoptNames("")) > 0; got != tc.wantShoptOpts {
				t.Fatalf("ShoptNames non-empty = %v, want %v", got, tc.wantShoptOpts)
			}
		})
	}
}
