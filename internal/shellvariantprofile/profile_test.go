package shellvariantprofile

import (
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
	"github.com/ewhauser/gbash/shellvariant"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		variant            shellvariant.ShellVariant
		wantVariant        shellvariant.ShellVariant
		wantLang           syntax.LangVariant
		wantBashDiag       bool
		wantLegacy         bool
		wantPosix          bool
		wantBraceExpand    bool
		wantBashNamespace  bool
		wantShopt          bool
		wantCaller         bool
		wantDirs           bool
		wantPushd          bool
		wantMapfile        bool
		wantBashVersionVar bool
	}{
		{
			name:               "auto defaults to bash",
			variant:            shellvariant.Auto,
			wantVariant:        shellvariant.Bash,
			wantLang:           syntax.LangBash,
			wantBashDiag:       true,
			wantBraceExpand:    true,
			wantShopt:          true,
			wantCaller:         true,
			wantDirs:           true,
			wantPushd:          true,
			wantMapfile:        true,
			wantBashNamespace:  true,
			wantBashVersionVar: true,
		},
		{
			name:            "sh",
			variant:         shellvariant.SH,
			wantVariant:     shellvariant.SH,
			wantLang:        syntax.LangPOSIX,
			wantPosix:       true,
			wantBraceExpand: false,
		},
		{
			name:            "mksh",
			variant:         shellvariant.Mksh,
			wantVariant:     shellvariant.Mksh,
			wantLang:        syntax.LangMirBSDKorn,
			wantBraceExpand: true,
		},
		{
			name:            "zsh",
			variant:         shellvariant.Zsh,
			wantVariant:     shellvariant.Zsh,
			wantLang:        syntax.LangZsh,
			wantBraceExpand: true,
			wantDirs:        true,
			wantPushd:       true,
		},
		{
			name:               "bats",
			variant:            shellvariant.Bats,
			wantVariant:        shellvariant.Bats,
			wantLang:           syntax.LangBats,
			wantBashDiag:       true,
			wantBraceExpand:    true,
			wantShopt:          true,
			wantCaller:         true,
			wantMapfile:        true,
			wantBashNamespace:  true,
			wantBashVersionVar: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			profile := Resolve(tc.variant)
			if got := profile.Variant; got != tc.wantVariant {
				t.Fatalf("Variant = %q, want %q", got, tc.wantVariant)
			}
			if got := profile.SyntaxLang; got != tc.wantLang {
				t.Fatalf("SyntaxLang = %v, want %v", got, tc.wantLang)
			}
			if got := profile.UsesBashDiagnostics; got != tc.wantBashDiag {
				t.Fatalf("UsesBashDiagnostics = %v, want %v", got, tc.wantBashDiag)
			}
			if got := profile.LegacyBashCompat; got != tc.wantLegacy {
				t.Fatalf("LegacyBashCompat = %v, want %v", got, tc.wantLegacy)
			}
			if got := profile.DefaultPosixMode; got != tc.wantPosix {
				t.Fatalf("DefaultPosixMode = %v, want %v", got, tc.wantPosix)
			}
			if got := profile.DefaultBraceExpand; got != tc.wantBraceExpand {
				t.Fatalf("DefaultBraceExpand = %v, want %v", got, tc.wantBraceExpand)
			}
			if got := profile.ExposesBashNamespace; got != tc.wantBashNamespace {
				t.Fatalf("ExposesBashNamespace = %v, want %v", got, tc.wantBashNamespace)
			}
			if got := profile.SupportsBuiltin("shopt"); got != tc.wantShopt {
				t.Fatalf("SupportsBuiltin(shopt) = %v, want %v", got, tc.wantShopt)
			}
			if got := profile.SupportsBuiltin("caller"); got != tc.wantCaller {
				t.Fatalf("SupportsBuiltin(caller) = %v, want %v", got, tc.wantCaller)
			}
			if got := profile.SupportsBuiltin("dirs"); got != tc.wantDirs {
				t.Fatalf("SupportsBuiltin(dirs) = %v, want %v", got, tc.wantDirs)
			}
			if got := profile.SupportsBuiltin("pushd"); got != tc.wantPushd {
				t.Fatalf("SupportsBuiltin(pushd) = %v, want %v", got, tc.wantPushd)
			}
			if got := profile.SupportsBuiltin("mapfile"); got != tc.wantMapfile {
				t.Fatalf("SupportsBuiltin(mapfile) = %v, want %v", got, tc.wantMapfile)
			}
			if got := profile.ExposesBashSpecialVar("BASH_VERSION"); got != tc.wantBashVersionVar {
				t.Fatalf("ExposesBashSpecialVar(BASH_VERSION) = %v, want %v", got, tc.wantBashVersionVar)
			}
		})
	}
}
