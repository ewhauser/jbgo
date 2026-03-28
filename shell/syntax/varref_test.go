package syntax

import (
	"strings"
	"testing"
)

func TestParserVarRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src     string
		want    string
		wantErr bool
	}{
		{src: "foo", want: "foo"},
		{src: "a[1]", want: "a[1]"},
		{src: `A["k"]`, want: `A["k"]`},
		{src: `A[$key]`, want: `A[$key]`},
		{src: "a[", wantErr: true},
		{src: "a[k]z", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			p := NewParser()
			ref, err := p.VarRef(strings.NewReader(tc.src))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("VarRef(%q) succeeded, want error", tc.src)
				}
				return
			}
			if err != nil {
				t.Fatalf("VarRef(%q) error = %v", tc.src, err)
			}
			var sb strings.Builder
			if err := NewPrinter().Print(&sb, ref); err != nil {
				t.Fatalf("Print(%q) error = %v", tc.src, err)
			}
			if got := sb.String(); got != tc.want {
				t.Fatalf("VarRef(%q) printed %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}
