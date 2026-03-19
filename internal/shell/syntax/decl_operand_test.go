package syntax

import (
	"reflect"
	"strings"
	"testing"
)

func TestParserDeclOperand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src      string
		wantType any
		want     string
	}{
		{src: "-a", wantType: &DeclFlag{}, want: "-a"},
		{src: "foo", wantType: &DeclName{}, want: "foo"},
		{src: "foo=bar", wantType: &DeclAssign{}, want: "foo=bar"},
		{src: "foo=(1 2)", wantType: &DeclAssign{}, want: "foo=(1 2)"},
		{src: "foo[$k]=bar", wantType: &DeclAssign{}, want: "foo[$k]=bar"},
		{src: "$x", wantType: &DeclDynamicWord{}, want: "$x"},
	}

	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			p := NewParser(Variant(LangBash))
			op, err := p.DeclOperand(strings.NewReader(tc.src))
			if err != nil {
				t.Fatalf("DeclOperand(%q) error = %v", tc.src, err)
			}
			if reflect.TypeOf(op) != reflect.TypeOf(tc.wantType) {
				t.Fatalf("DeclOperand(%q) type = %T, want %T", tc.src, op, tc.wantType)
			}
			var sb strings.Builder
			if err := NewPrinter().Print(&sb, op); err != nil {
				t.Fatalf("Print(%q) error = %v", tc.src, err)
			}
			if got := sb.String(); got != tc.want {
				t.Fatalf("DeclOperand(%q) printed %q, want %q", tc.src, got, tc.want)
			}
		})
	}
}
