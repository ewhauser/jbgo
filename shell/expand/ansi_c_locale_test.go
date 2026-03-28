package expand

import (
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
)

func TestANSICUnicodeEscapesHonorLocale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  testEnv
		src  string
		want string
	}{
		{
			name: "lc_all_c_preserves_u_escape",
			env: testEnv{
				"LC_ALL": {Set: true, Kind: String, Str: "C"},
			},
			src:  `$'[\u03bc]'`,
			want: `[\u03BC]`,
		},
		{
			name: "lc_all_c_preserves_upper_u_escape",
			env: testEnv{
				"LC_ALL": {Set: true, Kind: String, Str: "C"},
			},
			src:  `$'[\U0001f642]'`,
			want: `[\U0001F642]`,
		},
		{
			name: "utf8_locale_decodes_unicode_escape",
			env: testEnv{
				"LC_ALL": {Set: true, Kind: String, Str: "en_US.UTF-8"},
			},
			src:  `$'[\u03bc]'`,
			want: "[\u03bc]",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader("x "+tt.src+"\n"), "")
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tt.src, err)
			}
			call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
			if !ok || len(call.Args) != 2 {
				t.Fatalf("unexpected parse shape for %q", tt.src)
			}
			word := call.Args[1]
			got, err := Literal(&Config{Env: tt.env}, word)
			if err != nil {
				t.Fatalf("Literal(%q) error = %v", tt.src, err)
			}
			if got != tt.want {
				t.Fatalf("Literal(%q) = %q, want %q", tt.src, got, tt.want)
			}
		})
	}
}
