package builtins

import (
	"context"
	"testing"
)

func TestParseTestClassicDisambiguation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    bool
		wantErr bool
	}{
		{
			name: "leading bang uses expression precedence",
			args: []string{"!", "x", "-a", "!", "x"},
			want: false,
		},
		{
			name: "grouped literal bang stays valid",
			args: []string{"x", "-a", "(", "!", ")"},
			want: true,
		},
		{
			name: "grouped binary expression still parses",
			args: []string{"(", "x", "=", "y", ")"},
			want: false,
		},
		{
			name: "grouped left paren literal stays valid",
			args: []string{"(", "x", "=", "(", ")"},
			want: false,
		},
		{
			name: "grouped nested bool expression still parses",
			args: []string{"(", "x", "-a", "(", "y", ")", ")"},
			want: true,
		},
		{
			name: "deeply nested grouped bool expression still parses",
			args: []string{"(", "x", "-a", "(", "y", "-a", "(", "z", ")", ")", ")"},
			want: true,
		},
		{
			name:    "empty grouped expression is rejected",
			args:    []string{"x", "-a", "(", ")"},
			wantErr: true,
		},
		{
			name:    "nested one-word group is rejected",
			args:    []string{"(", "(", "x", ")", ")"},
			wantErr: true,
		},
		{
			name:    "grouped right paren literal is rejected",
			args:    []string{"(", "x", "=", ")", ")"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stack, err := parseTest(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseTest(%q) error = nil, want non-nil", tt.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTest(%q) error = %v", tt.args, err)
			}

			got, err := evalTest(context.Background(), nil, stack)
			if err != nil {
				t.Fatalf("evalTest(%q) error = %v", tt.args, err)
			}
			if got != tt.want {
				t.Fatalf("evalTest(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
