package builtins

import (
	"slices"
	"testing"
)

func TestUnexpandNormalizeInvocationShortcuts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "shortcut adds first only",
			args: []string{"-2,5", "-7", "--", "-9"},
			want: []string{"--tabs=2", "--tabs=5", "--tabs=7", "--first-only", "--", "-9"},
		},
		{
			name: "all suppresses implicit first only",
			args: []string{"-a", "-2,5"},
			want: []string{"-a", "--tabs=2", "--tabs=5"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd := NewUnexpand()
			inv := &Invocation{Args: tc.args}
			got := cmd.NormalizeInvocation(inv)
			if got == nil || !slices.Equal(got.Args, tc.want) {
				t.Fatalf("NormalizeInvocation() args = %#v, want %#v", got.Args, tc.want)
			}
		})
	}
}

func TestParseUnexpandTabList(t *testing.T) {
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
		{name: "standalone plus", raw: "+6", wantMode: expandRemainingNone, wantStops: []int{6}},
		{name: "standalone slash", raw: "/9", wantMode: expandRemainingNone, wantStops: []int{9}},
		{name: "standalone plus zero falls back", raw: "+0", wantMode: expandRemainingNone, wantStops: []int{8}},
		{name: "standalone slash zero falls back", raw: "/0", wantMode: expandRemainingNone, wantStops: []int{8}},
		{name: "slash suffix", raw: "3,/9", wantMode: expandRemainingSlash, wantStops: []int{3, 9}},
		{name: "plus suffix", raw: "3,+6", wantMode: expandRemainingPlus, wantStops: []int{3, 6}},
		{name: "slash zero suffix ignored", raw: "3,/0", wantMode: expandRemainingNone, wantStops: []int{3}},
		{name: "plus zero suffix ignored", raw: "3,+0", wantMode: expandRemainingNone, wantStops: []int{3}},
		{name: "specifier not at start", raw: "1/", wantErr: "'/' specifier not at start of number: '/'"},
		{name: "specifier only on last", raw: "1,+2,3", wantErr: "'+' specifier only allowed with the last value"},
		{name: "invalid char", raw: "x", wantErr: "tab size contains invalid character(s): 'x'"},
		{name: "zero", raw: "0", wantErr: "tab size cannot be 0"},
		{name: "ascending", raw: "1,1", wantErr: "tab sizes must be ascending"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotMode, gotStops, err := parseUnexpandTabList(tc.raw)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("parseUnexpandTabList(%q) error = %v, want %q", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseUnexpandTabList(%q) error = %v", tc.raw, err)
			}
			if gotMode != tc.wantMode {
				t.Fatalf("parseUnexpandTabList(%q) mode = %v, want %v", tc.raw, gotMode, tc.wantMode)
			}
			if !slices.Equal(gotStops, tc.wantStops) {
				t.Fatalf("parseUnexpandTabList(%q) tabstops = %#v, want %#v", tc.raw, gotStops, tc.wantStops)
			}
		})
	}
}

func TestUnexpandNextTabstop(t *testing.T) {
	t.Parallel()

	if got, ok := unexpandNextTabstop([]int{4}, 6, expandRemainingNone); !ok || got != 2 {
		t.Fatalf("unexpandNextTabstop(single) = (%d, %v), want (2, true)", got, ok)
	}
	if got, ok := unexpandNextTabstop([]int{1, 5}, 6, expandRemainingNone); ok || got != 0 {
		t.Fatalf("unexpandNextTabstop(none) = (%d, %v), want (0, false)", got, ok)
	}
	if got, ok := unexpandNextTabstop([]int{1, 5}, 6, expandRemainingPlus); !ok || got != 5 {
		t.Fatalf("unexpandNextTabstop(plus) = (%d, %v), want (5, true)", got, ok)
	}
	if got, ok := unexpandNextTabstop([]int{1, 5}, 6, expandRemainingSlash); !ok || got != 4 {
		t.Fatalf("unexpandNextTabstop(slash) = (%d, %v), want (4, true)", got, ok)
	}
}

func TestUnexpandBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts unexpandOptions
		in   string
		want string
	}{
		{
			name: "leading only",
			opts: unexpandOptions{tabstops: []int{3}},
			in:   "        A     B",
			want: "\t\t  A     B",
		},
		{
			name: "all blanks",
			opts: unexpandOptions{all: true, tabstops: []int{3}},
			in:   "        A     B",
			want: "\t\t  A\t  B",
		},
		{
			name: "trailing spaces preserved",
			opts: unexpandOptions{all: true, tabstops: []int{4}},
			in:   "123 \t1\n123 1\n123 \n123 ",
			want: "123\t\t1\n123 1\n123 \n123 ",
		},
		{
			name: "spaces after tabs can fold",
			opts: unexpandOptions{all: true, tabstops: []int{1, 4, 5}},
			in:   "a \t   B \t",
			want: "a\t\t  B \t",
		},
		{
			name: "multibyte uses byte columns",
			opts: unexpandOptions{all: true, tabstops: []int{8}},
			in:   "1ΔΔΔ5   99999\n",
			want: "1ΔΔΔ5   99999\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := string(unexpandBytes([]byte(tc.in), &tc.opts)); got != tc.want {
				t.Fatalf("unexpandBytes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
