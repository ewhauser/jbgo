package builtins

import (
	"context"
	stdfs "io/fs"
	"strings"
	"testing"
	"time"
)

type lsTestFileInfo struct {
	name    string
	size    int64
	mode    stdfs.FileMode
	modTime time.Time
	sys     any
}

func (fi lsTestFileInfo) Name() string         { return fi.name }
func (fi lsTestFileInfo) Size() int64          { return fi.size }
func (fi lsTestFileInfo) Mode() stdfs.FileMode { return fi.mode }
func (fi lsTestFileInfo) ModTime() time.Time   { return fi.modTime }
func (fi lsTestFileInfo) IsDir() bool          { return fi.mode.IsDir() }
func (fi lsTestFileInfo) Sys() any             { return fi.sys }

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

func TestParseLSTimeModeUsesConfiguredSelection(t *testing.T) {
	t.Parallel()

	spec := NewLS().Spec()
	tests := []struct {
		name string
		arg  string
		want lsTimeMode
	}{
		{name: "access", arg: "--time=atime", want: lsTimeAccess},
		{name: "change", arg: "--time=ctime", want: lsTimeChange},
		{name: "birth", arg: "--time=birth", want: lsTimeBirth},
		{name: "modification", arg: "--time=mtime", want: lsTimeModification},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inv := &Invocation{Args: []string{tt.arg}}
			matches, _, err := ParseCommandSpec(inv, &spec)
			if err != nil {
				t.Fatalf("ParseCommandSpec() error = %v", err)
			}

			got, err := parseLSTimeMode(inv, matches)
			if err != nil {
				t.Fatalf("parseLSTimeMode() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseLSTimeMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLSSelectedTimeUsesConfiguredMode(t *testing.T) {
	t.Parallel()

	modTime := time.Date(2025, time.January, 5, 6, 7, 8, 0, time.UTC)
	accessTime := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	changeTime := time.Date(2024, time.January, 3, 4, 5, 6, 0, time.UTC)
	birthTime := time.Date(2024, time.January, 4, 5, 6, 7, 0, time.UTC)
	info := lsTestFileInfo{
		name:    "file",
		mode:    0o644,
		modTime: modTime,
		sys: struct {
			Atime         int64
			AtimeNsec     int64
			Ctime         int64
			CtimeNsec     int64
			Birthtime     int64
			BirthtimeNsec int64
		}{
			Atime:         accessTime.Unix(),
			AtimeNsec:     int64(accessTime.Nanosecond()),
			Ctime:         changeTime.Unix(),
			CtimeNsec:     int64(changeTime.Nanosecond()),
			Birthtime:     birthTime.Unix(),
			BirthtimeNsec: int64(birthTime.Nanosecond()),
		},
	}

	tests := []struct {
		name string
		mode lsTimeMode
		want time.Time
	}{
		{name: "modification", mode: lsTimeModification, want: modTime},
		{name: "access", mode: lsTimeAccess, want: accessTime},
		{name: "change", mode: lsTimeChange, want: changeTime},
		{name: "birth", mode: lsTimeBirth, want: birthTime},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := lsSelectedTime(info, &lsOptions{timeMode: tt.mode}); !got.Equal(tt.want) {
				t.Fatalf("lsSelectedTime() = %v, want %v", got, tt.want)
			}
		})
	}

	fallback := lsSelectedTime(lsTestFileInfo{name: "file", mode: 0o644, modTime: modTime}, &lsOptions{timeMode: lsTimeBirth})
	if !fallback.Equal(modTime) {
		t.Fatalf("lsSelectedTime() fallback = %v, want %v", fallback, modTime)
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
