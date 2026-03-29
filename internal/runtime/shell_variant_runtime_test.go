package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shellvariant"
)

func TestSessionExecNonBashVariantsLeaveBashNamespaceUnset(t *testing.T) {
	t.Parallel()

	script := strings.Join([]string{
		"f() {",
		`  printf 'stack:%s:%s:%s\n' "${BASH_SOURCE+set}" "${BASH_LINENO+set}" "${FUNCNAME+set}"`,
		"}",
		`printf 'base:%s:%s:%s:%s:%s\n' "${BASH_VERSION+set}" "${BASHPID+set}" "${BASHOPTS+set}" "${BASH_EXECUTION_STRING+set}" "${BASH_REMATCH+set}"`,
		"f",
		"",
	}, "\n")

	for _, variant := range []shellvariant.ShellVariant{shellvariant.SH, shellvariant.Mksh, shellvariant.Zsh} {
		t.Run(string(variant), func(t *testing.T) {
			t.Parallel()

			session := newSession(t, &Config{})
			result, err := session.Exec(context.Background(), &ExecutionRequest{
				ShellVariant: variant,
				Script:       script,
			})
			if err != nil {
				t.Fatalf("Exec() error = %v", err)
			}
			if got, want := result.Stdout, "base:::::\nstack:::\n"; got != want {
				t.Fatalf("Stdout = %q, want %q", got, want)
			}
			if got := result.Stderr; got != "" {
				t.Fatalf("Stderr = %q, want empty", got)
			}
		})
	}
}

func TestSessionExecBatsVariantKeepsBashNamespace(t *testing.T) {
	t.Parallel()

	session := newSession(t, &Config{})
	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Name:         "inline.bats",
		ShellVariant: shellvariant.Bats,
		Script: strings.Join([]string{
			"f() {",
			`  printf 'stack:%s:%s:%s\n' "${BASH_SOURCE+set}" "${BASH_LINENO+set}" "${FUNCNAME+set}"`,
			"}",
			"[[ foo =~ (foo) ]]",
			`printf 'base:%s:%s:%s:%s:%s\n' "${BASH_VERSION+set}" "${BASHPID+set}" "${BASHOPTS+set}" "${BASH_EXECUTION_STRING+set}" "${BASH_REMATCH+set}"`,
			"f",
			"",
		}, "\n"),
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got, want := result.Stdout, "base:set:set:set:set:set\nstack:set:set:set\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestSessionExecShellVariantGatesBashOnlyBuiltins(t *testing.T) {
	t.Parallel()

	script := strings.Join([]string{
		`type caller >/dev/null 2>&1; printf 'caller=%d\n' "$?"`,
		`type shopt >/dev/null 2>&1; printf 'shopt=%d\n' "$?"`,
		`type compgen >/dev/null 2>&1; printf 'compgen=%d\n' "$?"`,
		`type complete >/dev/null 2>&1; printf 'complete=%d\n' "$?"`,
		`type compopt >/dev/null 2>&1; printf 'compopt=%d\n' "$?"`,
		`type mapfile >/dev/null 2>&1; printf 'mapfile=%d\n' "$?"`,
		`type readarray >/dev/null 2>&1; printf 'readarray=%d\n' "$?"`,
		`help shopt >/dev/null 2>&1; printf 'help_shopt=%d\n' "$?"`,
		"printf -- '---\\n'",
		"enable -a",
		"",
	}, "\n")

	tests := []struct {
		name            string
		variant         shellvariant.ShellVariant
		wantStatusBlock string
		wantEnable      []string
		omitEnable      []string
	}{
		{
			name:    "sh",
			variant: shellvariant.SH,
			wantStatusBlock: strings.Join([]string{
				"caller=1",
				"shopt=1",
				"compgen=1",
				"complete=1",
				"compopt=1",
				"mapfile=1",
				"readarray=1",
				"help_shopt=1",
				"---",
				"",
			}, "\n"),
			omitEnable: []string{"enable caller\n", "enable shopt\n", "enable compgen\n", "enable complete\n", "enable compopt\n", "enable mapfile\n", "enable readarray\n"},
		},
		{
			name:    "mksh",
			variant: shellvariant.Mksh,
			wantStatusBlock: strings.Join([]string{
				"caller=1",
				"shopt=1",
				"compgen=1",
				"complete=1",
				"compopt=1",
				"mapfile=1",
				"readarray=1",
				"help_shopt=1",
				"---",
				"",
			}, "\n"),
			omitEnable: []string{"enable caller\n", "enable shopt\n", "enable compgen\n", "enable complete\n", "enable compopt\n", "enable mapfile\n", "enable readarray\n"},
		},
		{
			name:    "zsh",
			variant: shellvariant.Zsh,
			wantStatusBlock: strings.Join([]string{
				"caller=1",
				"shopt=1",
				"compgen=1",
				"complete=1",
				"compopt=1",
				"mapfile=1",
				"readarray=1",
				"help_shopt=1",
				"---",
				"",
			}, "\n"),
			omitEnable: []string{"enable caller\n", "enable shopt\n", "enable compgen\n", "enable complete\n", "enable compopt\n", "enable mapfile\n", "enable readarray\n"},
		},
		{
			name:    "bats",
			variant: shellvariant.Bats,
			wantStatusBlock: strings.Join([]string{
				"caller=0",
				"shopt=0",
				"compgen=0",
				"complete=0",
				"compopt=0",
				"mapfile=0",
				"readarray=0",
				"help_shopt=0",
				"---",
				"",
			}, "\n"),
			wantEnable: []string{"enable caller\n", "enable shopt\n", "enable compgen\n", "enable complete\n", "enable compopt\n", "enable mapfile\n", "enable readarray\n"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			session := newSession(t, &Config{})
			result, err := session.Exec(context.Background(), &ExecutionRequest{
				ShellVariant: tc.variant,
				Script:       script,
			})
			if err != nil {
				t.Fatalf("Exec() error = %v", err)
			}
			if !strings.HasPrefix(result.Stdout, tc.wantStatusBlock) {
				t.Fatalf("Stdout prefix = %q, want prefix %q", result.Stdout, tc.wantStatusBlock)
			}
			for _, want := range tc.wantEnable {
				if !strings.Contains(result.Stdout, want) {
					t.Fatalf("Stdout missing %q: %q", want, result.Stdout)
				}
			}
			for _, omit := range tc.omitEnable {
				if strings.Contains(result.Stdout, omit) {
					t.Fatalf("Stdout unexpectedly contains %q: %q", omit, result.Stdout)
				}
			}
			if got := result.Stderr; got != "" {
				t.Fatalf("Stderr = %q, want empty", got)
			}
		})
	}
}

func TestSessionExecDirectoryStackBuiltinsMatchVariant(t *testing.T) {
	t.Parallel()

	script := strings.Join([]string{
		`type dirs >/dev/null 2>&1; printf 'dirs=%d\n' "$?"`,
		`type pushd >/dev/null 2>&1; printf 'pushd=%d\n' "$?"`,
		`type popd >/dev/null 2>&1; printf 'popd=%d\n' "$?"`,
		`help pushd >/dev/null 2>&1; printf 'help_pushd=%d\n' "$?"`,
		"printf -- '---\\n'",
		"enable -a",
		"",
	}, "\n")

	tests := []struct {
		name            string
		variant         shellvariant.ShellVariant
		wantStatusBlock string
		wantEnable      []string
		omitEnable      []string
	}{
		{
			name:    "sh",
			variant: shellvariant.SH,
			wantStatusBlock: strings.Join([]string{
				"dirs=1",
				"pushd=1",
				"popd=1",
				"help_pushd=1",
				"---",
				"",
			}, "\n"),
			omitEnable: []string{"enable dirs\n", "enable pushd\n", "enable popd\n"},
		},
		{
			name:    "mksh",
			variant: shellvariant.Mksh,
			wantStatusBlock: strings.Join([]string{
				"dirs=1",
				"pushd=1",
				"popd=1",
				"help_pushd=1",
				"---",
				"",
			}, "\n"),
			omitEnable: []string{"enable dirs\n", "enable pushd\n", "enable popd\n"},
		},
		{
			name:    "zsh",
			variant: shellvariant.Zsh,
			wantStatusBlock: strings.Join([]string{
				"dirs=0",
				"pushd=0",
				"popd=0",
				"help_pushd=0",
				"---",
				"",
			}, "\n"),
			wantEnable: []string{"enable dirs\n", "enable pushd\n", "enable popd\n"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			session := newSession(t, &Config{})
			result, err := session.Exec(context.Background(), &ExecutionRequest{
				ShellVariant: tc.variant,
				Script:       script,
			})
			if err != nil {
				t.Fatalf("Exec() error = %v", err)
			}
			if !strings.HasPrefix(result.Stdout, tc.wantStatusBlock) {
				t.Fatalf("Stdout prefix = %q, want prefix %q", result.Stdout, tc.wantStatusBlock)
			}
			for _, want := range tc.wantEnable {
				if !strings.Contains(result.Stdout, want) {
					t.Fatalf("Stdout missing %q: %q", want, result.Stdout)
				}
			}
			for _, omit := range tc.omitEnable {
				if strings.Contains(result.Stdout, omit) {
					t.Fatalf("Stdout unexpectedly contains %q: %q", omit, result.Stdout)
				}
			}
			if got := result.Stderr; got != "" {
				t.Fatalf("Stderr = %q, want empty", got)
			}
		})
	}
}
