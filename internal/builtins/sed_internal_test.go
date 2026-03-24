package builtins

import (
	"slices"
	"testing"
)

func TestNormalizeSedArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "short in-place suffix",
			args: []string{"-i.bak", "s/x/y/", "file"},
			want: []string{"--in-place=.bak", "s/x/y/", "file"},
		},
		{
			name: "grouped short flags before in-place suffix",
			args: []string{"-Ei.bak", "s/x/y/", "file"},
			want: []string{"-E", "--in-place=.bak", "s/x/y/", "file"},
		},
		{
			name: "plain grouped flags stay grouped",
			args: []string{"-ni", "s/x/y/", "file"},
			want: []string{"-ni", "s/x/y/", "file"},
		},
		{
			name: "script value after expression is not rewritten",
			args: []string{"-e", "-i.bak", "file"},
			want: []string{"-e", "-i.bak", "file"},
		},
		{
			name: "file after script is not rewritten",
			args: []string{"s/x/y/", "-i.bak"},
			want: []string{"s/x/y/", "-i.bak"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeSedArgs(tc.args); !slices.Equal(got, tc.want) {
				t.Fatalf("normalizeSedArgs(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
