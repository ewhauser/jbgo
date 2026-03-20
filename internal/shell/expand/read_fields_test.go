package expand

import "testing"

func testReadChars(s string) []ReadFieldChar {
	chars := make([]ReadFieldChar, 0, len(s))
	escaped := false
	for i := 0; i < len(s); i++ {
		if escaped {
			chars = append(chars, ReadFieldChar{Value: s[i], Escaped: true})
			escaped = false
			continue
		}
		if s[i] == '\\' {
			escaped = true
			continue
		}
		chars = append(chars, ReadFieldChar{Value: s[i]})
	}
	return chars
}

func TestReadFieldsFromChars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ifs  string
		in   string
		n    int
		want []string
	}{
		{
			name: "default remainder",
			in:   "hello world  test   ",
			n:    2,
			want: []string{"hello", "world  test"},
		},
		{
			name: "ifs whitespace plus char",
			ifs:  "x ",
			in:   "a x b",
			n:    -1,
			want: []string{"a", "b"},
		},
		{
			name: "adjacent non-whitespace delimiters",
			ifs:  "x ",
			in:   "a xx b",
			n:    -1,
			want: []string{"a", "", "b"},
		},
		{
			name: "max split preserves remainder",
			ifs:  "x ",
			in:   "a ax  x  ",
			n:    2,
			want: []string{"a", "ax  x"},
		},
		{
			name: "leading empty with trailing delimiters",
			ifs:  "x ",
			in:   "xaxx",
			n:    2,
			want: []string{"", "axx"},
		},
		{
			name: "escaped delimiter",
			ifs:  "\\ ",
			in:   "hello\\ world  test",
			n:    2,
			want: []string{"hello world", "test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &Config{}
			if tt.ifs != "" {
				cfg.Env = ListEnviron("IFS=" + tt.ifs)
			}
			got := ReadFieldsFromChars(cfg, testReadChars(tt.in), tt.n)
			if len(got) != len(tt.want) {
				t.Fatalf("len(ReadFieldsFromChars(%q)) = %d, want %d (%q)", tt.in, len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("ReadFieldsFromChars(%q)[%d] = %q, want %q", tt.in, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestReadFieldsPreservesEscapedIFSDelimiters(t *testing.T) {
	t.Parallel()

	cfg := &Config{Env: ListEnviron("IFS=:%")}
	got := ReadFields(cfg, `spam:eggs%ham cheese\:colon`, -1, false)
	want := []string{"spam", "eggs", "ham cheese:colon"}
	if len(got) != len(want) {
		t.Fatalf("len(ReadFields()) = %d, want %d (%q)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ReadFields()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
