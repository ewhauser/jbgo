package builtins

import (
	"bytes"
	"slices"
	"testing"
)

func TestExpandNormalizeInvocationShortcuts(t *testing.T) {
	t.Parallel()

	cmd := NewExpand()
	inv := &Invocation{Args: []string{"-2,5", "-7", "--", "-9"}}
	got := cmd.NormalizeInvocation(inv)
	want := []string{"--tabs=2", "--tabs=5", "--tabs=7", "--", "-9"}
	if got == nil || !equalStrings(got.Args, want) {
		t.Fatalf("NormalizeInvocation() args = %#v, want %#v", got.Args, want)
	}

	repeatInv := &Invocation{Args: []string{"-8,/4", "-1,+3"}}
	repeatGot := cmd.NormalizeInvocation(repeatInv)
	repeatWant := []string{"--tabs=8", "--tabs=/4", "--tabs=1", "--tabs=+3"}
	if repeatGot == nil || !equalStrings(repeatGot.Args, repeatWant) {
		t.Fatalf("NormalizeInvocation() repeat args = %#v, want %#v", repeatGot.Args, repeatWant)
	}
}

func TestParseExpandTabList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		wantMode  expandRemainingMode
		wantStops []int
		wantErr   string
	}{
		{name: "default", raw: "", wantMode: expandRemainingNone, wantStops: []int{8}},
		{name: "space separated", raw: "3 6 9", wantMode: expandRemainingNone, wantStops: []int{3, 6, 9}},
		{name: "tab separated", raw: "3\t6\t9", wantMode: expandRemainingNone, wantStops: []int{3, 6, 9}},
		{name: "slash suffix", raw: "1,/5", wantMode: expandRemainingSlash, wantStops: []int{1, 5}},
		{name: "slash repeat below last fixed", raw: "8,/4", wantMode: expandRemainingSlash, wantStops: []int{8, 4}},
		{name: "plus suffix", raw: "1,+5", wantMode: expandRemainingPlus, wantStops: []int{1, 5}},
		{name: "plus repeat below last fixed", raw: "8,+4", wantMode: expandRemainingPlus, wantStops: []int{8, 4}},
		{name: "no numbers falls back", raw: "+,/,+,/", wantMode: expandRemainingNone, wantStops: []int{8}},
		{name: "specifier not at start", raw: "1/", wantErr: "'/' specifier not at start of number: '/'"},
		{name: "specifier only on last", raw: "1,+2,3", wantErr: "'+' specifier only allowed with the last value"},
		{name: "invalid char", raw: "x", wantErr: "tab size contains invalid character(s): 'x'"},
		{name: "zero", raw: "0", wantErr: "tab size cannot be 0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotMode, gotStops, err := parseExpandTabList(tc.raw)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("parseExpandTabList(%q) error = %v, want %q", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseExpandTabList(%q) error = %v", tc.raw, err)
			}
			if gotMode != tc.wantMode {
				t.Fatalf("parseExpandTabList(%q) mode = %v, want %v", tc.raw, gotMode, tc.wantMode)
			}
			if !slices.Equal(gotStops, tc.wantStops) {
				t.Fatalf("parseExpandTabList(%q) tabstops = %#v, want %#v", tc.raw, gotStops, tc.wantStops)
			}
		})
	}
}

func TestExpandNextTabstop(t *testing.T) {
	t.Parallel()

	if got := expandNextTabstop([]int{1, 5}, 6, expandRemainingNone); got != 1 {
		t.Fatalf("expandNextTabstop(None) = %d, want 1", got)
	}
	if got := expandNextTabstop([]int{1, 5}, 6, expandRemainingPlus); got != 5 {
		t.Fatalf("expandNextTabstop(Plus) = %d, want 5", got)
	}
	if got := expandNextTabstop([]int{1, 5}, 6, expandRemainingSlash); got != 4 {
		t.Fatalf("expandNextTabstop(Slash) = %d, want 4", got)
	}
}

func TestExpandBytes(t *testing.T) {
	t.Parallel()

	opts := expandOptions{
		initial:       true,
		tabstops:      []int{4},
		remainingMode: expandRemainingNone,
		spaceCache:    bytes.Repeat([]byte(" "), 4),
	}
	if got, want := string(expandBytes([]byte("\ta\tb"), &opts)), "    a\tb"; got != want {
		t.Fatalf("expandBytes(initial) = %q, want %q", got, want)
	}

	byteCountOpts := expandOptions{
		tabstops:      []int{8},
		remainingMode: expandRemainingNone,
		spaceCache:    bytes.Repeat([]byte(" "), 8),
	}
	if got, want := string(expandBytes([]byte("界\tX"), &byteCountOpts)), "界     X"; got != want {
		t.Fatalf("expandBytes(multibyte) = %q, want %q", got, want)
	}
}
