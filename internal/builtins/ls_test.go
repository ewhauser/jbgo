package builtins

import (
	"context"
	"strings"
	"testing"
)

func TestParseLSTimeStylePosixPrefixRespectsLCAllPrecedence(t *testing.T) {
	t.Parallel()

	spec := NewLS().Spec()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "default locale falls back to c locale behavior",
			env:  map[string]string{},
			want: "locale",
		},
		{
			name: "lc all overrides lc time",
			env: map[string]string{
				"LC_ALL":  "C.UTF-8",
				"LC_TIME": "POSIX",
			},
			want: "long-iso",
		},
		{
			name: "posix effective locale uses locale style",
			env: map[string]string{
				"LC_ALL":  "POSIX",
				"LC_TIME": "C.UTF-8",
			},
			want: "locale",
		},
		{
			name: "c locale uses locale style",
			env: map[string]string{
				"LC_ALL": "C",
			},
			want: "locale",
		},
		{
			name: "lang participates when lc all and lc time are unset",
			env: map[string]string{
				"LANG": "C.UTF-8",
			},
			want: "long-iso",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inv := &Invocation{
				Args: []string{"-l", "--time-style=posix-long-iso"},
				Env:  tt.env,
			}
			matches, _, err := ParseCommandSpec(inv, &spec)
			if err != nil {
				t.Fatalf("ParseCommandSpec() error = %v", err)
			}

			got, err := parseLSTimeStyle(inv, matches)
			if err != nil {
				t.Fatalf("parseLSTimeStyle() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseLSTimeStyle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseLSTimeStyleRejectsEmptyPosixSuffixOutsidePosixLocale(t *testing.T) {
	t.Parallel()

	spec := NewLS().Spec()
	inv := &Invocation{
		Args: []string{"-l", "--time-style=posix-"},
		Env: map[string]string{
			"LC_ALL": "C.UTF-8",
		},
	}

	matches, _, err := ParseCommandSpec(inv, &spec)
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}

	_, err = parseLSTimeStyle(inv, matches)
	if err == nil {
		t.Fatal("parseLSTimeStyle() error = nil, want invalid argument error")
	}
	code, ok := ExitCode(err)
	if !ok || code != 2 {
		t.Fatalf("ExitCode = %d, ok=%v; want code 2", code, ok)
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "invalid argument 'posix-' for 'time style'") {
		t.Fatalf("error = %q, want invalid time style diagnostic", got)
	}
}

//nolint:paralleltest // Mutates the package-global identity DB loader.
func TestPrimeLSIdentityDBCachesPerInvocation(t *testing.T) {
	t.Parallel()

	calls := 0

	opts := &lsOptions{
		longFormat: true,
		showOwner:  true,
		showGroup:  true,
		identityDBLoader: func(context.Context, *Invocation) *permissionIdentityDB {
			calls++
			return &permissionIdentityDB{}
		},
	}

	primeLSIdentityDB(context.Background(), &Invocation{}, opts)
	primeLSIdentityDB(context.Background(), &Invocation{}, opts)

	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1", calls)
	}
	if opts.identityDB == nil {
		t.Fatal("identityDB = nil, want cached DB")
	}
}

func TestPrimeLSIdentityDBSkipsNumericIDs(t *testing.T) {
	t.Parallel()

	calls := 0

	opts := &lsOptions{
		longFormat: true,
		showOwner:  true,
		showGroup:  true,
		numericIDs: true,
		identityDBLoader: func(context.Context, *Invocation) *permissionIdentityDB {
			calls++
			return &permissionIdentityDB{}
		},
	}

	primeLSIdentityDB(context.Background(), &Invocation{}, opts)

	if calls != 0 {
		t.Fatalf("loader calls = %d, want 0", calls)
	}
	if opts.identityDB != nil {
		t.Fatalf("identityDB = %#v, want nil", opts.identityDB)
	}
}
