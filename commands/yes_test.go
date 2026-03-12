package commands

import (
	"fmt"
	"testing"
)

func TestPrepareYesBufferMatchesUutilsBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		lineLen int
		wantLen int
	}{
		{150, 16350},
		{1000, 16000},
		{4093, 16372},
		{4099, 12297},
		{4111, 12333},
		{2, 16384},
		{3, 16383},
		{4, 16384},
		{5, 16380},
		{8192, 16384},
		{8191, 16382},
		{8193, 8193},
		{10000, 10000},
		{15000, 15000},
		{25000, 25000},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("len=%d", tc.lineLen), func(t *testing.T) {
			buffer := make([]byte, tc.lineLen)
			for i := range buffer {
				buffer[i] = 'a'
			}
			got := prepareYesBuffer(buffer)
			if len(got) != tc.wantLen {
				t.Fatalf("prepareYesBuffer(len=%d) = %d, want %d", tc.lineLen, len(got), tc.wantLen)
			}
		})
	}
}

func TestYesArgsIntoBuffer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		operands []string
		want     string
	}{
		{name: "default", want: "y\n"},
		{name: "single", operands: []string{"foo"}, want: "foo\n"},
		{name: "multiple", operands: []string{"foo", "bar    baz", "qux"}, want: "foo bar    baz qux\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(yesArgsIntoBuffer(tc.operands)); got != tc.want {
				t.Fatalf("yesArgsIntoBuffer(%q) = %q, want %q", tc.operands, got, tc.want)
			}
		})
	}
}
