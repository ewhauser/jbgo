package expand

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ewhauser/gbash/shell/syntax"
)

var benchmarkParamArrayFieldsSink []string

func benchmarkParseCommandWord(b *testing.B, src string) *syntax.Word {
	b.Helper()
	p := syntax.NewParser()
	file, err := p.Parse(strings.NewReader("x "+src+"\n"), "")
	if err != nil {
		b.Fatal(err)
	}
	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok {
		b.Fatalf("parsed %q as %T, want *syntax.CallExpr", src, file.Stmts[0].Cmd)
	}
	if len(call.Args) != 2 {
		b.Fatalf("parsed %q into %d args, want 2", src, len(call.Args))
	}
	return call.Args[1]
}

func BenchmarkParamArrayOps(b *testing.B) {
	tests := []struct {
		name string
		env  testEnv
		src  string
		want []string
	}{
		{
			name: "replace_all",
			env:  benchmarkParamArrayEnv(1024, "_%c_"),
			src:  `${arr[@]//_?_/foo}`,
			want: slicesRepeat("foo", 1024),
		},
		{
			name: "replace_prefix",
			env:  benchmarkParamArrayEnv(1024, "_%c_"),
			src:  `${arr[@]/#_?_/foo}`,
			want: slicesRepeat("foo", 1024),
		},
		{
			name: "replace_suffix",
			env:  benchmarkParamArrayEnv(1024, "_%c_"),
			src:  `${arr[@]/%_?_/foo}`,
			want: slicesRepeat("foo", 1024),
		},
		{
			name: "remove_small_prefix",
			env:  benchmarkParamArrayEnv(1024, "_%c_tail"),
			src:  `${arr[@]#_?_}`,
			want: slicesRepeat("tail", 1024),
		},
		{
			name: "upper_all_pattern",
			env:  benchmarkParamArrayEnv(1024, "%cfoo"),
			src:  `${arr[@]^^[a-z]}`,
			want: benchmarkUpperAllWant(1024),
		},
	}

	for _, tc := range tests {
		word := benchmarkParseCommandWord(b, tc.src)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			fields, err := Fields(&Config{Env: tc.env}, word)
			if err != nil {
				b.Fatal(err)
			}
			if !reflect.DeepEqual(fields, tc.want) {
				b.Fatalf("Fields(%q) = %#v, want %#v", tc.src, fields, tc.want)
			}
			b.ResetTimer()
			for range b.N {
				fields, err := Fields(&Config{Env: tc.env}, word)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkParamArrayFieldsSink = fields
			}
		})
	}
}

func benchmarkParamArrayEnv(size int, format string) testEnv {
	elems := make([]string, size)
	for i := range size {
		elems[i] = formatParamBenchElem(format, i)
	}
	return testEnv{
		"arr": {Set: true, Kind: Indexed, List: elems},
	}
}

func benchmarkUpperAllWant(size int) []string {
	elems := make([]string, size)
	for i := range size {
		elems[i] = string(rune('A'+(i%26))) + "FOO"
	}
	return elems
}

func formatParamBenchElem(format string, i int) string {
	var sb strings.Builder
	sb.Grow(len(format))
	for j := 0; j < len(format); j++ {
		if format[j] == '%' && j+1 < len(format) && format[j+1] == 'c' {
			sb.WriteByte(byte('a' + (i % 26)))
			j++
			continue
		}
		sb.WriteByte(format[j])
	}
	return sb.String()
}

func slicesRepeat(s string, n int) []string {
	out := make([]string, n)
	for i := range n {
		out[i] = s
	}
	return out
}
