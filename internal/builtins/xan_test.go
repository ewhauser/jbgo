package builtins_test

import (
	"context"
	"strings"
	"testing"
)

const (
	xanUsersCSV = "name,age,email,active\nalice,30,alice@example.com,true\nbob,25,bob@example.com,false\ncharlie,35,charlie@example.com,true\ndiana,28,diana@example.com,true\n"

	xanNumbersCSV = "n\n1\n2\n3\n4\n5\n"

	xanProductsCSV = "id,name,price,category,in_stock\n1,Widget,19.99,electronics,true\n2,Gadget,29.99,electronics,true\n3,Gizmo,9.99,accessories,false\n4,Doodad,49.99,electronics,true\n5,Thingamajig,14.99,accessories,true\n"
)

func newXanConfig(files map[string]string, cwd string) *Config {
	if cwd == "" {
		cwd = "/"
	}
	return &Config{
		FileSystem: CustomFileSystem(seededFSFactory{files: files}, cwd),
	}
}

func runXan(t *testing.T, files map[string]string, cwd, script string) *ExecutionResult {
	t.Helper()
	rt := newRuntime(t, newXanConfig(files, cwd))
	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: script})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return result
}

func runXanSession(t *testing.T, files map[string]string, cwd string) *Session {
	t.Helper()
	return newSession(t, newXanConfig(files, cwd))
}

func requireExitCode(t *testing.T, result *ExecutionResult, want int) {
	t.Helper()
	if result.ExitCode != want {
		t.Fatalf("ExitCode = %d, want %d; stdout=%q stderr=%q", result.ExitCode, want, result.Stdout, result.Stderr)
	}
}

func requireStdout(t *testing.T, result *ExecutionResult, want string) {
	t.Helper()
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func requireStderr(t *testing.T, result *ExecutionResult, want string) {
	t.Helper()
	if result.Stderr != want {
		t.Fatalf("Stderr = %q, want %q", result.Stderr, want)
	}
}

func TestXanBasicCommands(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		cases := []struct {
			name    string
			files   map[string]string
			script  string
			wantOut string
		}{
			{
				name: "counts users",
				files: map[string]string{
					"/users.csv": xanUsersCSV,
				},
				script:  "xan count /users.csv",
				wantOut: "4\n",
			},
			{
				name: "counts numbers",
				files: map[string]string{
					"/numbers.csv": xanNumbersCSV,
				},
				script:  "xan count /numbers.csv",
				wantOut: "5\n",
			},
			{
				name: "header only is zero",
				files: map[string]string{
					"/empty.csv": "name,age\n",
				},
				script:  "xan count /empty.csv",
				wantOut: "0\n",
			},
			{
				name:    "stdin",
				files:   nil,
				script:  "printf 'a\n1\n2\n3\n' | xan count",
				wantOut: "3\n",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				result := runXan(t, tc.files, "", tc.script)
				requireExitCode(t, result, 0)
				requireStdout(t, result, tc.wantOut)
			})
		}
	})

	t.Run("headers head tail slice reverse enum behead", func(t *testing.T) {
		files := map[string]string{
			"/users.csv":   xanUsersCSV,
			"/numbers.csv": xanNumbersCSV,
			"/header.csv":  "a,b,c\n",
		}
		cases := []struct {
			name    string
			script  string
			wantOut string
		}{
			{
				name:    "headers",
				script:  "xan headers /users.csv",
				wantOut: "0   name\n1   age\n2   email\n3   active\n",
			},
			{
				name:    "headers just names",
				script:  "xan headers -j /users.csv",
				wantOut: "name\nage\nemail\nactive\n",
			},
			{
				name:    "headers stdin",
				script:  "printf 'a,b,c\n1,2,3\n' | xan headers -j",
				wantOut: "a\nb\nc\n",
			},
			{
				name:    "head default",
				script:  "xan head /users.csv",
				wantOut: xanUsersCSV,
			},
			{
				name:    "head limit",
				script:  "xan head -l 2 /users.csv",
				wantOut: "name,age,email,active\nalice,30,alice@example.com,true\nbob,25,bob@example.com,false\n",
			},
			{
				name:    "head stdin",
				script:  "printf 'a\n1\n2\n3\n4\n5\n' | xan head -l 2",
				wantOut: "a\n1\n2\n",
			},
			{
				name:    "tail default",
				script:  "xan tail /users.csv",
				wantOut: xanUsersCSV,
			},
			{
				name:    "tail limit",
				script:  "xan tail -l 2 /users.csv",
				wantOut: "name,age,email,active\ncharlie,35,charlie@example.com,true\ndiana,28,diana@example.com,true\n",
			},
			{
				name:    "slice start end",
				script:  "xan slice -s 1 -e 3 /users.csv",
				wantOut: "name,age,email,active\nbob,25,bob@example.com,false\ncharlie,35,charlie@example.com,true\n",
			},
			{
				name:    "slice len",
				script:  "xan slice -l 2 /users.csv",
				wantOut: "name,age,email,active\nalice,30,alice@example.com,true\nbob,25,bob@example.com,false\n",
			},
			{
				name:    "reverse",
				script:  "xan reverse /numbers.csv",
				wantOut: "n\n5\n4\n3\n2\n1\n",
			},
			{
				name:    "enum default",
				script:  "xan enum /numbers.csv",
				wantOut: "index,n\n0,1\n1,2\n2,3\n3,4\n4,5\n",
			},
			{
				name:    "enum custom",
				script:  "xan enum -c row_num /numbers.csv",
				wantOut: "row_num,n\n0,1\n1,2\n2,3\n3,4\n4,5\n",
			},
			{
				name:    "behead",
				script:  "xan behead /users.csv",
				wantOut: "alice,30,alice@example.com,true\nbob,25,bob@example.com,false\ncharlie,35,charlie@example.com,true\ndiana,28,diana@example.com,true\n",
			},
			{
				name:    "behead header only",
				script:  "xan behead /header.csv",
				wantOut: "",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				result := runXan(t, files, "", tc.script)
				requireExitCode(t, result, 0)
				requireStdout(t, result, tc.wantOut)
			})
		}
	})

	t.Run("sample help and errors", func(t *testing.T) {
		result := runXan(t, map[string]string{"/data.csv": "n\n1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"}, "", "xan sample 3 --seed 42 /data.csv")
		requireExitCode(t, result, 0)
		lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
		if len(lines) != 4 || lines[0] != "n" {
			t.Fatalf("unexpected sample output %q", result.Stdout)
		}

		result = runXan(t, map[string]string{"/data.csv": "n\n1\n2\n3\n"}, "", "xan sample 10 /data.csv")
		requireExitCode(t, result, 0)
		requireStdout(t, result, "n\n1\n2\n3\n")

		result = runXan(t, nil, "", "xan --help")
		requireExitCode(t, result, 0)
		if !strings.Contains(result.Stdout, "xan is a collection of commands for working with CSV data.") {
			t.Fatalf("help output missing summary: %q", result.Stdout)
		}

		result = runXan(t, nil, "", "xan parallel")
		requireExitCode(t, result, 1)
		requireStderr(t, result, "xan parallel: not yet implemented\n")

		result = runXan(t, nil, "", "xan foobar")
		requireExitCode(t, result, 1)
		requireStderr(t, result, "xan: unknown command 'foobar'\nRun 'xan --help' for usage.\n")

		result = runXan(t, nil, "", "xan count /nonexistent.csv")
		requireExitCode(t, result, 1)
		if !strings.Contains(result.Stderr, "No such file or directory") {
			t.Fatalf("missing file stderr = %q", result.Stderr)
		}
	})
}

func TestXanColumnOperations(t *testing.T) {
	files := map[string]string{
		"/users.csv":   xanUsersCSV,
		"/numbers.csv": xanNumbersCSV,
		"/data.csv":    "name,vec_1,vec_2,count_1,count_2\njohn,1,2,3,4\nmary,5,6,7,8\n",
		"/range.csv":   "a,b,c,d,e\n1,2,3,4,5\n",
		"/small.csv":   "a,b\n1,2\n",
		"/abcd.csv":    "a,b,c,d\n1,2,3,4\n",
	}

	cases := []struct {
		name    string
		files   map[string]string
		cwd     string
		script  string
		wantOut string
	}{
		{
			name:    "select by name",
			files:   files,
			script:  "xan select name,email /users.csv",
			wantOut: "name,email\nalice,alice@example.com\nbob,bob@example.com\ncharlie,charlie@example.com\ndiana,diana@example.com\n",
		},
		{
			name:    "select by index",
			files:   files,
			script:  "xan select 0,2 /users.csv",
			wantOut: "name,email\nalice,alice@example.com\nbob,bob@example.com\ncharlie,charlie@example.com\ndiana,diana@example.com\n",
		},
		{
			name:    "reorders columns",
			files:   files,
			script:  "xan select email,name /users.csv",
			wantOut: "email,name\nalice@example.com,alice\nbob@example.com,bob\ncharlie@example.com,charlie\ndiana@example.com,diana\n",
		},
		{
			name:    "range notation",
			files:   files,
			script:  "xan select 0-1 /users.csv",
			wantOut: "name,age\nalice,30\nbob,25\ncharlie,35\ndiana,28\n",
		},
		{
			name:    "relative path",
			files:   map[string]string{"/home/user/data.csv": "a,b,c\n1,2,3\n"},
			cwd:     "/home/user",
			script:  "xan select a,b data.csv",
			wantOut: "a,b\n1,2\n",
		},
		{
			name:    "drop by name",
			files:   files,
			script:  "xan drop email,active /users.csv",
			wantOut: "name,age\nalice,30\nbob,25\ncharlie,35\ndiana,28\n",
		},
		{
			name:    "drop by index",
			files:   files,
			script:  "xan drop 2,3 /users.csv",
			wantOut: "name,age\nalice,30\nbob,25\ncharlie,35\ndiana,28\n",
		},
		{
			name:    "rename all columns",
			files:   files,
			script:  "xan rename VALUE /numbers.csv",
			wantOut: "VALUE\n1\n2\n3\n4\n5\n",
		},
		{
			name:    "rename selected column",
			files:   files,
			script:  "xan rename username -s name /users.csv | xan select username",
			wantOut: "username\nalice\nbob\ncharlie\ndiana\n",
		},
		{
			name:    "select prefix glob",
			files:   files,
			script:  "xan select 'vec_*' /data.csv",
			wantOut: "vec_1,vec_2\n1,2\n5,6\n",
		},
		{
			name:    "select suffix glob",
			files:   files,
			script:  "xan select '*_1' /data.csv",
			wantOut: "vec_1,count_1\n1,3\n5,7\n",
		},
		{
			name:    "select glob with regular column",
			files:   files,
			script:  "xan select 'name,vec_*' /data.csv",
			wantOut: "name,vec_1,vec_2\njohn,1,2\nmary,5,6\n",
		},
		{
			name:    "select all",
			files:   files,
			script:  "xan select '*' /small.csv",
			wantOut: "a,b\n1,2\n",
		},
		{
			name:    "select all plus duplicate",
			files:   files,
			script:  "xan select '*,a' /small.csv",
			wantOut: "a,b,a\n1,2,1\n",
		},
		{
			name:    "column range",
			files:   files,
			script:  "xan select 'a:c' /range.csv",
			wantOut: "a,b,c\n1,2,3\n",
		},
		{
			name:    "reverse column range",
			files:   files,
			script:  "xan select 'c:a' /range.csv",
			wantOut: "c,b,a\n3,2,1\n",
		},
		{
			name:    "range to end",
			files:   files,
			script:  "xan select 'd:' /range.csv",
			wantOut: "d,e\n4,5\n",
		},
		{
			name:    "range from start",
			files:   files,
			script:  "xan select ':b' /range.csv",
			wantOut: "a,b\n1,2\n",
		},
		{
			name:    "combined ranges",
			files:   files,
			script:  "xan select 'a:b,d:e' /range.csv",
			wantOut: "a,b,d,e\n1,2,4,5\n",
		},
		{
			name:    "negation single",
			files:   files,
			script:  "xan select '*,!b' /abcd.csv",
			wantOut: "a,c,d\n1,3,4\n",
		},
		{
			name:    "negation multiple",
			files:   files,
			script:  "xan select '*,!b,!d' /abcd.csv",
			wantOut: "a,c\n1,3\n",
		},
		{
			name:    "negation range",
			files:   files,
			script:  "xan select '*,!b:c' /abcd.csv",
			wantOut: "a,d\n1,4\n",
		},
		{
			name:    "numeric indices",
			files:   files,
			script:  "xan select '0,2' /abcd.csv",
			wantOut: "a,c\n1,3\n",
		},
		{
			name:    "numeric range",
			files:   files,
			script:  "xan select '1-3' /abcd.csv",
			wantOut: "b,c,d\n2,3,4\n",
		},
		{
			name:    "duplicate selections",
			files:   files,
			script:  "xan select 'a,a' /abcd.csv",
			wantOut: "a,a\n1,1\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runXan(t, tc.files, tc.cwd, tc.script)
			requireExitCode(t, result, 0)
			requireStdout(t, result, tc.wantOut)
		})
	}
}

func TestXanFilterSortSearch(t *testing.T) {
	files := map[string]string{
		"/users.csv":    xanUsersCSV,
		"/products.csv": xanProductsCSV,
		"/data.csv":     "h1,h2\nfoobar,x\nabc,y\nbarfoo,z\n",
	}

	cases := []struct {
		name     string
		files    map[string]string
		script   string
		wantOut  string
		wantErr  string
		wantCode int
	}{
		{
			name:     "filter numeric comparison",
			files:    files,
			script:   "xan filter 'age > 28' /users.csv",
			wantOut:  "name,age,email,active\nalice,30,alice@example.com,true\ncharlie,35,charlie@example.com,true\n",
			wantCode: 0,
		},
		{
			name:     "filter string equality",
			files:    files,
			script:   "xan filter 'active eq \"true\"' /users.csv",
			wantOut:  "name,age,email,active\nalice,30,alice@example.com,true\ncharlie,35,charlie@example.com,true\ndiana,28,diana@example.com,true\n",
			wantCode: 0,
		},
		{
			name:     "filter invert",
			files:    files,
			script:   "xan filter -v 'age > 28' /users.csv",
			wantOut:  "name,age,email,active\nbob,25,bob@example.com,false\ndiana,28,diana@example.com,true\n",
			wantCode: 0,
		},
		{
			name:     "filter limit",
			files:    files,
			script:   "xan filter -l 1 'age > 20' /users.csv",
			wantOut:  "name,age,email,active\nalice,30,alice@example.com,true\n",
			wantCode: 0,
		},
		{
			name:     "filter no matches",
			files:    map[string]string{"/data.csv": "n\n1\n2\n3\n"},
			script:   "xan filter 'n > 100' /data.csv",
			wantOut:  "n\n",
			wantCode: 0,
		},
		{
			name:     "sort string",
			files:    files,
			script:   "xan sort -s name /users.csv",
			wantOut:  xanUsersCSV,
			wantCode: 0,
		},
		{
			name:     "sort numeric",
			files:    files,
			script:   "xan sort -s age -N /users.csv",
			wantOut:  "name,age,email,active\nbob,25,bob@example.com,false\ndiana,28,diana@example.com,true\nalice,30,alice@example.com,true\ncharlie,35,charlie@example.com,true\n",
			wantCode: 0,
		},
		{
			name:     "sort reverse",
			files:    files,
			script:   "xan sort -s age -N -R /users.csv",
			wantOut:  "name,age,email,active\ncharlie,35,charlie@example.com,true\nalice,30,alice@example.com,true\ndiana,28,diana@example.com,true\nbob,25,bob@example.com,false\n",
			wantCode: 0,
		},
		{
			name:     "dedup category",
			files:    files,
			script:   "xan dedup -s category /products.csv",
			wantOut:  "id,name,price,category,in_stock\n1,Widget,19.99,electronics,true\n3,Gizmo,9.99,accessories,false\n",
			wantCode: 0,
		},
		{
			name:     "dedup unique",
			files:    map[string]string{"/data.csv": "n\n1\n2\n3\n"},
			script:   "xan dedup -s n /data.csv",
			wantOut:  "n\n1\n2\n3\n",
			wantCode: 0,
		},
		{
			name:     "dedup same",
			files:    map[string]string{"/data.csv": "n\n5\n5\n5\n"},
			script:   "xan dedup -s n /data.csv",
			wantOut:  "n\n5\n",
			wantCode: 0,
		},
		{
			name:     "top numeric",
			files:    files,
			script:   "xan top price -l 2 /products.csv",
			wantOut:  "id,name,price,category,in_stock\n4,Doodad,49.99,electronics,true\n2,Gadget,29.99,electronics,true\n",
			wantCode: 0,
		},
		{
			name:     "top reverse",
			files:    files,
			script:   "xan top price -l 2 -R /products.csv",
			wantOut:  "id,name,price,category,in_stock\n3,Gizmo,9.99,accessories,false\n5,Thingamajig,14.99,accessories,true\n",
			wantCode: 0,
		},
		{
			name:     "search regex",
			files:    files,
			script:   "xan search -r '^foo' /data.csv",
			wantOut:  "h1,h2\nfoobar,x\n",
			wantCode: 0,
		},
		{
			name:     "search invert",
			files:    files,
			script:   "xan search -v -r '^foo' /data.csv",
			wantOut:  "h1,h2\nabc,y\nbarfoo,z\n",
			wantCode: 0,
		},
		{
			name:     "search select columns",
			files:    map[string]string{"/data.csv": "h1,h2\nfoo,bar\nbar,foo\n"},
			script:   "xan search -s h1 -r 'foo' /data.csv",
			wantOut:  "h1,h2\nfoo,bar\n",
			wantCode: 0,
		},
		{
			name:     "search ignore case",
			files:    map[string]string{"/data.csv": "name\nFOO\nfoo\nbar\n"},
			script:   "xan search -i -r 'foo' /data.csv",
			wantOut:  "name\nFOO\nfoo\n",
			wantCode: 0,
		},
		{
			name:     "invalid regex",
			files:    map[string]string{"/data.csv": "name\nalice\n"},
			script:   "xan search '[' /data.csv",
			wantErr:  "xan search: invalid regex pattern '['\n",
			wantCode: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := runXan(t, tc.files, "", tc.script)
			requireExitCode(t, result, tc.wantCode)
			if tc.wantOut != "" {
				requireStdout(t, result, tc.wantOut)
			}
			if tc.wantErr != "" {
				requireStderr(t, result, tc.wantErr)
			}
		})
	}
}

func TestXanMapTransformAggAndGroupby(t *testing.T) {
	t.Run("map", func(t *testing.T) {
		cases := []struct {
			name    string
			files   map[string]string
			script  string
			wantOut string
		}{
			{
				name:    "computed column",
				files:   map[string]string{"/data.csv": "a,b\n1,2\n2,3\n"},
				script:  "xan map 'add(a, b) as c' /data.csv",
				wantOut: "a,b,c\n1,2,3\n2,3,5\n",
			},
			{
				name:    "multiple computed columns",
				files:   map[string]string{"/data.csv": "a,b\n1,2\n2,3\n"},
				script:  "xan map 'add(a, b) as c, mul(a, b) as d' /data.csv",
				wantOut: "a,b,c,d\n1,2,3,2\n2,3,5,6\n",
			},
			{
				name:    "index function",
				files:   map[string]string{"/data.csv": "n\n10\n15\n"},
				script:  "xan map 'index() as r' /data.csv",
				wantOut: "n,r\n10,0\n15,1\n",
			},
			{
				name:    "overwrite",
				files:   map[string]string{"/data.csv": "a,b\n1,4\n5,2\n"},
				script:  "xan map -O 'b * 10 as b, a * b as c' /data.csv",
				wantOut: "a,b,c\n1,40,4\n5,20,10\n",
			},
			{
				name:    "filter rows",
				files:   map[string]string{"/data.csv": "full_name\njohn landis\nbéatrice babka\n"},
				script:  "xan map \"if(startswith(full_name, 'j'), split(full_name, ' ')[0]) as first_name\" --filter /data.csv",
				wantOut: "full_name,first_name\njohn landis,john\n",
			},
			{
				name:    "split function",
				files:   map[string]string{"/data.csv": "full_name\njohn landis\nmary smith\n"},
				script:  "xan map \"split(full_name, ' ')[0] as first\" /data.csv",
				wantOut: "full_name,first\njohn landis,john\nmary smith,mary\n",
			},
			{
				name:    "upper and lower",
				files:   map[string]string{"/data.csv": "name\nJohn\nmary\n"},
				script:  "xan map 'upper(name) as upper, lower(name) as lower' /data.csv",
				wantOut: "name,upper,lower\nJohn,JOHN,john\nmary,MARY,mary\n",
			},
			{
				name:    "trim",
				files:   map[string]string{"/data.csv": "text\n\"  hello  \"\n\"  world  \"\n"},
				script:  "xan map 'trim(text) as trimmed' /data.csv",
				wantOut: "text,trimmed\n\"  hello  \",hello\n\"  world  \",world\n",
			},
			{
				name:    "len",
				files:   map[string]string{"/data.csv": "word\ncat\ndog\nelephant\n"},
				script:  "xan map 'len(word) as length' /data.csv",
				wantOut: "word,length\ncat,3\ndog,3\nelephant,8\n",
			},
			{
				name:    "arithmetic",
				files:   map[string]string{"/data.csv": "x,y\n10,3\n20,4\n"},
				script:  "xan map 'x + y as sum, x - y as diff, x * y as prod, x / y as quot' /data.csv",
				wantOut: "x,y,sum,diff,prod,quot\n10,3,13,7,30,3.3333333333333335\n20,4,24,16,80,5\n",
			},
			{
				name:    "abs and round",
				files:   map[string]string{"/data.csv": "n\n-5.7\n3.2\n"},
				script:  "xan map 'abs(n) as absolute, round(n) as rounded' /data.csv",
				wantOut: "n,absolute,rounded\n-5.7,5.7,-6\n3.2,3.2,3\n",
			},
			{
				name:    "if expression",
				files:   map[string]string{"/data.csv": "score\n85\n55\n70\n"},
				script:  "xan map \"if(score >= 60, 'pass', 'fail') as result\" /data.csv",
				wantOut: "score,result\n85,pass\n55,fail\n70,pass\n",
			},
			{
				name:    "coalesce",
				files:   map[string]string{"/data.csv": "name,id\njohn,1\n,2\nmary,3\n"},
				script:  "xan map \"coalesce(name, 'unknown') as name_safe\" /data.csv",
				wantOut: "name,id,name_safe\njohn,1,john\n,2,unknown\nmary,3,mary\n",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				result := runXan(t, tc.files, "", tc.script)
				requireExitCode(t, result, 0)
				requireStdout(t, result, tc.wantOut)
			})
		}
	})

	t.Run("transform", func(t *testing.T) {
		files := map[string]string{"/data.csv": "a,b,c\n1,2,3\n4,5,6\n"}
		cases := []struct {
			name     string
			files    map[string]string
			script   string
			wantOut  string
			wantErr  string
			wantCode int
		}{
			{
				name:     "transform column",
				files:    files,
				script:   "xan transform b 'add(a, b)' /data.csv",
				wantOut:  "a,b,c\n1,3,3\n4,9,6\n",
				wantCode: 0,
			},
			{
				name:     "transform rename",
				files:    files,
				script:   "xan transform b 'add(a, b)' -r sum /data.csv",
				wantOut:  "a,sum,c\n1,3,3\n4,9,6\n",
				wantCode: 0,
			},
			{
				name:     "underscore",
				files:    files,
				script:   "xan transform b 'mul(_, 2)' /data.csv",
				wantOut:  "a,b,c\n1,4,3\n4,10,6\n",
				wantCode: 0,
			},
			{
				name:     "multiple columns",
				files:    files,
				script:   "xan transform a,b 'mul(_, 10)' /data.csv",
				wantOut:  "a,b,c\n10,20,3\n40,50,6\n",
				wantCode: 0,
			},
			{
				name:     "multiple columns rename",
				files:    files,
				script:   "xan transform a,b 'mul(_, 10)' -r x,y /data.csv",
				wantOut:  "x,y,c\n10,20,3\n40,50,6\n",
				wantCode: 0,
			},
			{
				name:     "missing column",
				files:    files,
				script:   "xan transform z 'add(a, b)' /data.csv",
				wantErr:  "xan transform: column 'z' not found\n",
				wantCode: 1,
			},
			{
				name:     "missing args",
				files:    files,
				script:   "xan transform",
				wantErr:  "xan transform: usage: xan transform COLUMN EXPR [FILE]\n",
				wantCode: 1,
			},
			{
				name:     "string function",
				files:    map[string]string{"/data.csv": "name,value\nhello,1\nworld,2\n"},
				script:   "xan transform name 'upper(_)' /data.csv",
				wantOut:  "name,value\nHELLO,1\nWORLD,2\n",
				wantCode: 0,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				result := runXan(t, tc.files, "", tc.script)
				requireExitCode(t, result, tc.wantCode)
				if tc.wantOut != "" {
					requireStdout(t, result, tc.wantOut)
				}
				if tc.wantErr != "" {
					requireStderr(t, result, tc.wantErr)
				}
			})
		}
	})

	t.Run("agg and groupby", func(t *testing.T) {
		cases := []struct {
			name     string
			files    map[string]string
			script   string
			wantOut  string
			wantErr  string
			wantCode int
		}{
			{
				name:     "agg count sum mean avg min max first last median",
				files:    map[string]string{"/data.csv": "n\n1\n2\n3\n4\n"},
				script:   "xan agg 'count() as count, sum(n) as sum, mean(n) as mean, avg(n) as avg, min(n) as min, max(n) as max, first(n) as first, last(n) as last, median(n) as median' /data.csv",
				wantOut:  "count,sum,mean,avg,min,max,first,last,median\n4,10,2.5,2.5,1,4,1,4,2.5\n",
				wantCode: 0,
			},
			{
				name:     "agg boolean and set aggregations",
				files:    map[string]string{"/data.csv": "color\nred\nblue\nyellow\nred\n"},
				script:   "xan agg 'mode(color) as mode, cardinality(color) as cardinality, values(color) as values, distinct_values(color) as distinct_values' /data.csv",
				wantOut:  "mode,cardinality,values,distinct_values\nred,3,red|blue|yellow|red,blue|red|yellow\n",
				wantCode: 0,
			},
			{
				name:     "agg all any and expression",
				files:    map[string]string{"/data.csv": "a,b\n1,2\n2,0\n3,6\n4,2\n"},
				script:   "xan agg 'count(a > 2) as count, all(a >= 1) as all, any(b >= 6) as any, sum(add(a, b + 1)) as sum' /data.csv",
				wantOut:  "count,all,any,sum\n2,true,true,24\n",
				wantCode: 0,
			},
			{
				name:     "agg all requires expression",
				files:    map[string]string{"/data.csv": "a\n1\n"},
				script:   "xan agg 'all() as ok' /data.csv",
				wantErr:  "xan agg: all() requires an expression\n",
				wantCode: 1,
			},
			{
				name:     "groupby sums",
				files:    map[string]string{"/data.csv": "id,value_A,value_B,value_C\nx,1,2,3\ny,2,3,4\nz,3,4,5\ny,1,2,3\nz,2,3,5\nz,3,6,7\n"},
				script:   "xan groupby id 'sum(value_A) as sumA' /data.csv",
				wantOut:  "id,sumA\nx,1\ny,3\nz,8\n",
				wantCode: 0,
			},
			{
				name:     "groupby count complex mean max",
				files:    map[string]string{"/data.csv": "id,value_A,value_B,value_C\nx,1,2,3\ny,2,3,4\nz,3,4,5\ny,1,2,3\nz,2,3,5\nz,3,6,7\n"},
				script:   "xan groupby id 'count() as count, sum(add(value_A, add(value_B, value_C))) as sum, mean(value_A) as meanA, max(value_A) as maxA, max(value_B) as maxB, max(value_C) as maxC' /data.csv",
				wantOut:  "id,count,sum,meanA,maxA,maxB,maxC\nx,1,6,1,1,2,3\ny,2,15,1.5,2,3,4\nz,3,38,2.6666666666666665,3,6,7\n",
				wantCode: 0,
			},
			{
				name:     "groupby multiple columns",
				files:    map[string]string{"/data.csv": "name,color,count\njohn,blue,1\nmary,orange,3\nmary,orange,2\njohn,yellow,9\njohn,blue,2\n"},
				script:   "xan groupby name,color 'sum(count) as sum' /data.csv",
				wantOut:  "name,color,sum\njohn,blue,3\nmary,orange,5\njohn,yellow,9\n",
				wantCode: 0,
			},
			{
				name:     "groupby sorted and empty",
				files:    map[string]string{"/data.csv": "id,value_A,value_B,value_C\n"},
				script:   "xan groupby id 'sum(value_A) as sumA' --sorted /data.csv",
				wantOut:  "id,sumA\n",
				wantCode: 0,
			},
			{
				name:     "groupby missing column",
				files:    map[string]string{"/data.csv": "region,value\nnorth,1\nsouth,2\n"},
				script:   "xan groupby regoin 'sum(value) as total' /data.csv",
				wantErr:  "xan groupby: column 'regoin' not found\n",
				wantCode: 1,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				result := runXan(t, tc.files, "", tc.script)
				requireExitCode(t, result, tc.wantCode)
				if tc.wantOut != "" {
					requireStdout(t, result, tc.wantOut)
				}
				if tc.wantErr != "" {
					requireStderr(t, result, tc.wantErr)
				}
			})
		}
	})
}

func TestXanFrequencyReshapeMultifileAndData(t *testing.T) {
	t.Run("frequency", func(t *testing.T) {
		result := runXan(t, map[string]string{"/in.csv": "h1,h2\na,z\na,y\na,y\nb,z\n,z\n"}, "", "xan frequency --no-extra -l 0 /in.csv")
		requireExitCode(t, result, 0)
		if !strings.HasPrefix(result.Stdout, "field,value,count\n") {
			t.Fatalf("frequency stdout = %q", result.Stdout)
		}

		result = runXan(t, map[string]string{"/in.csv": "h1,h2\na,z\na,y\na,y\nb,z\n,z\n"}, "", "xan frequency -s h2 --no-extra -l 0 /in.csv")
		requireExitCode(t, result, 0)
		if !strings.Contains(result.Stdout, "h2,z,3\n") || !strings.Contains(result.Stdout, "h2,y,2\n") {
			t.Fatalf("frequency select stdout = %q", result.Stdout)
		}

		result = runXan(t, map[string]string{"/data.csv": "a\nx\nx\ny\ny\nz\nz\n"}, "", "xan frequency /data.csv")
		requireExitCode(t, result, 0)
		requireStdout(t, result, "field,value,count\na,x,2\na,y,2\na,z,2\n")

		result = runXan(t, map[string]string{"/data.csv": "name,color\njohn,blue\nmary,red\nmary,red\nmary,red\nmary,purple\njohn,yellow\njohn,blue\n"}, "", "xan frequency -g name /data.csv")
		requireExitCode(t, result, 0)
		if !strings.HasPrefix(result.Stdout, "field,name,value,count\n") {
			t.Fatalf("grouped frequency stdout = %q", result.Stdout)
		}

		result = runXan(t, map[string]string{"/data.csv": "n\n1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n"}, "", "xan frequency -A /data.csv")
		requireExitCode(t, result, 0)
		if len(strings.Split(strings.TrimSpace(result.Stdout), "\n")) != 12 {
			t.Fatalf("frequency all stdout = %q", result.Stdout)
		}

		result = runXan(t, map[string]string{"/data.csv": "name,score\nalice,10\nbob,20\n"}, "", "xan stats /data.csv")
		requireExitCode(t, result, 0)
		requireStdout(t, result, "field,type,count,min,max,mean\nname,String,2,,,\nscore,Number,2,10,20,15\n")
	})

	t.Run("reshape", func(t *testing.T) {
		cases := []struct {
			name     string
			files    map[string]string
			script   string
			wantOut  string
			wantErr  string
			wantCode int
		}{
			{
				name:     "explode",
				files:    map[string]string{"/data.csv": "id,tags\n1,a|b|c\n2,x|y\n"},
				script:   "xan explode tags /data.csv",
				wantOut:  "id,tags\n1,a\n1,b\n1,c\n2,x\n2,y\n",
				wantCode: 0,
			},
			{
				name:     "explode separator",
				files:    map[string]string{"/data.csv": "id,items\n1,a;b;c\n"},
				script:   "xan explode items -s ';' /data.csv",
				wantOut:  "id,items\n1,a\n1,b\n1,c\n",
				wantCode: 0,
			},
			{
				name:     "explode rename",
				files:    map[string]string{"/data.csv": "id,tags\n1,a|b\n"},
				script:   "xan explode tags -r tag /data.csv",
				wantOut:  "id,tag\n1,a\n1,b\n",
				wantCode: 0,
			},
			{
				name:     "explode empty values",
				files:    map[string]string{"/data.csv": "id,tags\n1,a|b\n2,\n3,c\n"},
				script:   "xan explode tags /data.csv",
				wantOut:  "id,tags\n1,a\n1,b\n2,\n3,c\n",
				wantCode: 0,
			},
			{
				name:     "explode drop empty",
				files:    map[string]string{"/data.csv": "id,tags\n1,a|b\n2,\n3,c\n"},
				script:   "xan explode tags --drop-empty /data.csv",
				wantOut:  "id,tags\n1,a\n1,b\n3,c\n",
				wantCode: 0,
			},
			{
				name:     "explode missing column",
				files:    map[string]string{"/data.csv": "id,tags\n1,a\n"},
				script:   "xan explode nonexistent /data.csv",
				wantErr:  "xan explode: column 'nonexistent' not found\n",
				wantCode: 1,
			},
			{
				name:     "implode",
				files:    map[string]string{"/data.csv": "id,tag\n1,a\n1,b\n1,c\n2,x\n2,y\n"},
				script:   "xan implode tag /data.csv",
				wantOut:  "id,tag\n1,a|b|c\n2,x|y\n",
				wantCode: 0,
			},
			{
				name:     "implode separator",
				files:    map[string]string{"/data.csv": "id,val\n1,a\n1,b\n"},
				script:   "xan implode val -s ';' /data.csv",
				wantOut:  "id,val\n1,a;b\n",
				wantCode: 0,
			},
			{
				name:     "implode rename",
				files:    map[string]string{"/data.csv": "id,tag\n1,a\n1,b\n"},
				script:   "xan implode tag -r tags /data.csv",
				wantOut:  "id,tags\n1,a|b\n",
				wantCode: 0,
			},
			{
				name:     "implode only consecutive",
				files:    map[string]string{"/data.csv": "id,tag\n1,a\n2,x\n1,b\n"},
				script:   "xan implode tag /data.csv",
				wantOut:  "id,tag\n1,a\n2,x\n1,b\n",
				wantCode: 0,
			},
			{
				name:     "pivot count",
				files:    map[string]string{"/data.csv": "region,product,amount\nnorth,A,10\nnorth,B,20\nsouth,A,15\nsouth,B,25\n"},
				script:   "xan pivot product 'count(amount)' -g region /data.csv",
				wantOut:  "region,A,B\nnorth,1,1\nsouth,1,1\n",
				wantCode: 0,
			},
			{
				name:     "pivot sum",
				files:    map[string]string{"/data.csv": "region,product,amount\nnorth,A,10\nnorth,B,20\nsouth,A,15\nsouth,A,5\n"},
				script:   "xan pivot product 'sum(amount)' -g region /data.csv",
				wantOut:  "region,A,B\nnorth,10,20\nsouth,20,0\n",
				wantCode: 0,
			},
			{
				name:     "pivot mean",
				files:    map[string]string{"/data.csv": "cat,type,val\nX,a,10\nX,a,20\nX,b,30\n"},
				script:   "xan pivot type 'mean(val)' -g cat /data.csv",
				wantOut:  "cat,a,b\nX,15,30\n",
				wantCode: 0,
			},
			{
				name:     "pivot auto group columns",
				files:    map[string]string{"/data.csv": "year,quarter,sales\n2023,Q1,100\n2023,Q2,150\n2024,Q1,120\n"},
				script:   "xan pivot quarter 'sum(sales)' /data.csv",
				wantOut:  "year,Q1,Q2\n2023,100,150\n2024,120,0\n",
				wantCode: 0,
			},
			{
				name:     "pivot invalid",
				files:    map[string]string{"/data.csv": "a,b,c\n1,2,3\n"},
				script:   "xan pivot b 'invalid syntax' /data.csv",
				wantErr:  "xan pivot: invalid aggregation expression 'invalid syntax'\n",
				wantCode: 1,
			},
			{
				name:     "flatmap array",
				files:    map[string]string{"/data.csv": "text\nhello world\nfoo bar baz\n"},
				script:   "xan flatmap \"split(text, ' ') as word\" /data.csv",
				wantOut:  "text,word\nhello world,hello\nhello world,world\nfoo bar baz,foo\nfoo bar baz,bar\nfoo bar baz,baz\n",
				wantCode: 0,
			},
			{
				name:     "flatmap non array",
				files:    map[string]string{"/data.csv": "n\n1\n2\n"},
				script:   "xan flatmap 'n * 2 as doubled' /data.csv",
				wantOut:  "n,doubled\n1,2\n2,4\n",
				wantCode: 0,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				result := runXan(t, tc.files, "", tc.script)
				requireExitCode(t, result, tc.wantCode)
				if tc.wantOut != "" {
					requireStdout(t, result, tc.wantOut)
				}
				if tc.wantErr != "" {
					requireStderr(t, result, tc.wantErr)
				}
			})
		}
	})

	t.Run("multi file and data conversion", func(t *testing.T) {
		cases := []struct {
			name     string
			files    map[string]string
			script   string
			wantOut  string
			wantErr  string
			wantCode int
		}{
			{
				name:     "cat",
				files:    map[string]string{"/a.csv": "id,name\n1,alice\n2,bob\n", "/b.csv": "id,name\n3,charlie\n"},
				script:   "xan cat /a.csv /b.csv",
				wantOut:  "id,name\n1,alice\n2,bob\n3,charlie\n",
				wantCode: 0,
			},
			{
				name:     "cat mismatched headers",
				files:    map[string]string{"/a.csv": "id,name\n1,alice\n", "/b.csv": "id,email\n2,bob@x.com\n"},
				script:   "xan cat /a.csv /b.csv",
				wantErr:  "xan cat: headers do not match (use -p to pad)\n",
				wantCode: 1,
			},
			{
				name:     "cat pad",
				files:    map[string]string{"/a.csv": "id,name\n1,alice\n", "/b.csv": "id,email\n2,bob@x.com\n"},
				script:   "xan cat -p /a.csv /b.csv",
				wantOut:  "id,name,email\n1,alice,\n2,,bob@x.com\n",
				wantCode: 0,
			},
			{
				name:     "cat no files",
				files:    nil,
				script:   "xan cat",
				wantErr:  "xan cat: no files specified\n",
				wantCode: 1,
			},
			{
				name:     "join inner",
				files:    map[string]string{"/left.csv": "id,name\n1,alice\n2,bob\n3,charlie\n", "/right.csv": "user_id,score\n1,100\n2,85\n4,90\n"},
				script:   "xan join id /left.csv user_id /right.csv",
				wantOut:  "id,name,user_id,score\n1,alice,1,100\n2,bob,2,85\n",
				wantCode: 0,
			},
			{
				name:     "join left",
				files:    map[string]string{"/left.csv": "id,name\n1,alice\n2,bob\n3,charlie\n", "/right.csv": "user_id,score\n1,100\n2,85\n"},
				script:   "xan join --left id /left.csv user_id /right.csv",
				wantOut:  "id,name,user_id,score\n1,alice,1,100\n2,bob,2,85\n3,charlie,,\n",
				wantCode: 0,
			},
			{
				name:     "join right",
				files:    map[string]string{"/left.csv": "id,name\n1,alice\n", "/right.csv": "user_id,score\n1,100\n2,85\n"},
				script:   "xan join --right id /left.csv user_id /right.csv",
				wantOut:  "id,name,user_id,score\n1,alice,1,100\n,,2,85\n",
				wantCode: 0,
			},
			{
				name:     "join full",
				files:    map[string]string{"/left.csv": "id,name\n1,alice\n2,bob\n", "/right.csv": "user_id,score\n2,85\n3,90\n"},
				script:   "xan join --full id /left.csv user_id /right.csv",
				wantOut:  "id,name,user_id,score\n1,alice,,\n2,bob,2,85\n,,3,90\n",
				wantCode: 0,
			},
			{
				name:     "join default",
				files:    map[string]string{"/left.csv": "id,name\n1,alice\n2,bob\n", "/right.csv": "user_id,score\n1,100\n"},
				script:   "xan join --left -D 'N/A' id /left.csv user_id /right.csv",
				wantOut:  "id,name,user_id,score\n1,alice,1,100\n2,bob,N/A,N/A\n",
				wantCode: 0,
			},
			{
				name:     "join one to many",
				files:    map[string]string{"/users.csv": "id,name\n1,alice\n", "/orders.csv": "user_id,item\n1,book\n1,pen\n1,paper\n"},
				script:   "xan join id /users.csv user_id /orders.csv",
				wantOut:  "id,name,user_id,item\n1,alice,1,book\n1,alice,1,pen\n1,alice,1,paper\n",
				wantCode: 0,
			},
			{
				name:     "join deduplicates shared column names",
				files:    map[string]string{"/left.csv": "id,name,status\n1,alice,active\n2,bob,inactive\n", "/right.csv": "id,status,score\n1,verified,100\n2,pending,85\n"},
				script:   "xan join id /left.csv id /right.csv",
				wantOut:  "id,name,status,score\n1,alice,active,100\n2,bob,inactive,85\n",
				wantCode: 0,
			},
			{
				name:     "join missing key",
				files:    map[string]string{"/a.csv": "id,name\n1,alice\n", "/b.csv": "user_id,score\n1,100\n"},
				script:   "xan join nonexistent /a.csv user_id /b.csv",
				wantErr:  "xan join: column 'nonexistent' not found in first file\n",
				wantCode: 1,
			},
			{
				name:     "merge",
				files:    map[string]string{"/a.csv": "id,val\n1,a\n3,c\n", "/b.csv": "id,val\n2,b\n4,d\n"},
				script:   "xan merge /a.csv /b.csv",
				wantOut:  "id,val\n1,a\n3,c\n2,b\n4,d\n",
				wantCode: 0,
			},
			{
				name:     "merge sort",
				files:    map[string]string{"/a.csv": "id,val\n1,a\n3,c\n", "/b.csv": "id,val\n2,b\n4,d\n"},
				script:   "xan merge -s id /a.csv /b.csv",
				wantOut:  "id,val\n1,a\n2,b\n3,c\n4,d\n",
				wantCode: 0,
			},
			{
				name:     "merge mismatched headers",
				files:    map[string]string{"/a.csv": "id,name\n1,alice\n", "/b.csv": "id,email\n2,bob@x.com\n"},
				script:   "xan merge /a.csv /b.csv",
				wantErr:  "xan merge: all files must have the same headers\n",
				wantCode: 1,
			},
			{
				name:     "merge requires two files",
				files:    map[string]string{"/a.csv": "id,val\n1,a\n"},
				script:   "xan merge /a.csv",
				wantErr:  "xan merge: usage: xan merge [OPTIONS] FILE1 FILE2 ...\n",
				wantCode: 1,
			},
			{
				name:     "to json",
				files:    map[string]string{"/data.csv": "name,age\nalice,30\nbob,25\n"},
				script:   "xan to json /data.csv",
				wantOut:  "[\n  {\n    \"age\": 30,\n    \"name\": \"alice\"\n  },\n  {\n    \"age\": 25,\n    \"name\": \"bob\"\n  }\n]\n",
				wantCode: 0,
			},
			{
				name:     "to missing format",
				files:    map[string]string{"/data.csv": "a\n1\n"},
				script:   "xan to",
				wantErr:  "xan to: usage: xan to <format> [FILE]\n",
				wantCode: 1,
			},
			{
				name:     "from json objects",
				files:    map[string]string{"/data.json": "[{\"name\":\"alice\",\"age\":30},{\"name\":\"bob\",\"age\":25}]"},
				script:   "xan from -f json /data.json",
				wantOut:  "age,name\n30,alice\n25,bob\n",
				wantCode: 0,
			},
			{
				name:     "from json arrays",
				files:    map[string]string{"/data.json": "[[\"name\",\"age\"],[\"alice\",30],[\"bob\",25]]"},
				script:   "xan from -f json /data.json",
				wantOut:  "name,age\nalice,30\nbob,25\n",
				wantCode: 0,
			},
			{
				name:     "from invalid json",
				files:    map[string]string{"/data.json": "not valid json"},
				script:   "xan from -f json /data.json",
				wantErr:  "xan from: invalid JSON input\n",
				wantCode: 1,
			},
			{
				name:     "from missing format",
				files:    map[string]string{"/data.json": "[]"},
				script:   "xan from /data.json",
				wantErr:  "xan from: usage: xan from -f <format> [FILE]\n",
				wantCode: 1,
			},
			{
				name:     "transpose",
				files:    map[string]string{"/data.csv": "metric,jan,feb,mar\nsales,100,150,200\ncosts,80,90,100\n"},
				script:   "xan transpose /data.csv",
				wantOut:  "metric,sales,costs\njan,100,80\nfeb,150,90\nmar,200,100\n",
				wantCode: 0,
			},
			{
				name:     "transpose single column",
				files:    map[string]string{"/data.csv": "name\nalice\nbob\n"},
				script:   "xan transpose /data.csv",
				wantOut:  "name,alice,bob\n",
				wantCode: 0,
			},
			{
				name:     "transpose header only",
				files:    map[string]string{"/data.csv": "a,b,c\n"},
				script:   "xan transpose /data.csv",
				wantOut:  "column\na\nb\nc\n",
				wantCode: 0,
			},
			{
				name:     "fixlengths pad",
				files:    map[string]string{"/data.csv": "a,b,c\n1,2,3\n4,5\n6\n"},
				script:   "xan fixlengths /data.csv",
				wantOut:  "a,b,c\n1,2,3\n4,5,\n6,,\n",
				wantCode: 0,
			},
			{
				name:     "fixlengths truncate",
				files:    map[string]string{"/data.csv": "a,b,c,d\n1,2,3,4\n5,6,7,8\n"},
				script:   "xan fixlengths -l 2 /data.csv",
				wantOut:  "a,b\n1,2\n5,6\n",
				wantCode: 0,
			},
			{
				name:     "fixlengths default",
				files:    map[string]string{"/data.csv": "a,b,c\n1,2\n3\n"},
				script:   "xan fixlengths -d 'N/A' /data.csv",
				wantOut:  "a,b,c\n1,2,N/A\n3,N/A,N/A\n",
				wantCode: 0,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				result := runXan(t, tc.files, "", tc.script)
				requireExitCode(t, result, tc.wantCode)
				if tc.wantOut != "" {
					requireStdout(t, result, tc.wantOut)
				}
				if tc.wantErr != "" {
					requireStderr(t, result, tc.wantErr)
				}
			})
		}

		t.Run("shuffle", func(t *testing.T) {
			result := runXan(t, map[string]string{"/data.csv": "n\n1\n2\n3\n4\n5\n"}, "", "xan shuffle --seed 42 /data.csv")
			requireExitCode(t, result, 0)
			lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
			if len(lines) != 6 || lines[0] != "n" {
				t.Fatalf("shuffle stdout = %q", result.Stdout)
			}

			result1 := runXan(t, map[string]string{"/data.csv": "n\n1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"}, "", "xan shuffle --seed 1 /data.csv")
			result2 := runXan(t, map[string]string{"/data.csv": "n\n1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"}, "", "xan shuffle --seed 2 /data.csv")
			requireExitCode(t, result1, 0)
			requireExitCode(t, result2, 0)
			if result1.Stdout == result2.Stdout {
				t.Fatalf("shuffle outputs should differ: %q", result1.Stdout)
			}
		})

		t.Run("split and partition write files", func(t *testing.T) {
			session := runXanSession(t, map[string]string{"/data.csv": "n\n1\n2\n3\n4\n5\n6\n", "/region.csv": "region,value\nnorth,10\nsouth,20\nnorth,30\n"}, "/")

			result := mustExecSession(t, session, "xan split -c 3 -o /parts /data.csv")
			requireExitCode(t, result, 0)
			requireStdout(t, result, "Split into 3 parts\n")
			if got := string(readSessionFile(t, session, "/parts/data_001.csv")); got != "n\n1\n2\n" {
				t.Fatalf("split part 1 = %q", got)
			}
			if got := string(readSessionFile(t, session, "/parts/data_002.csv")); got != "n\n3\n4\n" {
				t.Fatalf("split part 2 = %q", got)
			}
			if got := string(readSessionFile(t, session, "/parts/data_003.csv")); got != "n\n5\n6\n" {
				t.Fatalf("split part 3 = %q", got)
			}

			result = mustExecSession(t, session, "xan split /data.csv || true")
			if !strings.Contains(result.Stderr, "xan split: must specify -c or -S") {
				t.Fatalf("split error stderr = %q", result.Stderr)
			}

			result = mustExecSession(t, session, "xan partition -o /by-region region /region.csv")
			requireExitCode(t, result, 0)
			requireStdout(t, result, "Partitioned into 2 files by 'region'\n")
			if got := string(readSessionFile(t, session, "/by-region/north.csv")); got != "region,value\nnorth,10\nnorth,30\n" {
				t.Fatalf("north partition = %q", got)
			}
			if got := string(readSessionFile(t, session, "/by-region/south.csv")); got != "region,value\nsouth,20\n" {
				t.Fatalf("south partition = %q", got)
			}

			result = mustExecSession(t, session, "xan partition nonexistent /region.csv || true")
			if !strings.Contains(result.Stderr, "xan partition: column 'nonexistent' not found") {
				t.Fatalf("partition missing column stderr = %q", result.Stderr)
			}

			writeSessionFile(t, session, "/collision.csv", []byte("region,value\na/b,1\na_b,2\n"))
			result = mustExecSession(t, session, "xan partition -o /collide region /collision.csv || true")
			if !strings.Contains(result.Stderr, `xan partition: values "a/b" and "a_b" map to the same output file "a_b.csv"`) {
				t.Fatalf("partition collision stderr = %q", result.Stderr)
			}
		})
	})
}

func TestXanDangerousHeaders(t *testing.T) {
	keywords := []string{
		"constructor",
		"prototype",
		"hasOwnProperty",
		"isPrototypeOf",
		"propertyIsEnumerable",
		"toString",
		"valueOf",
		"toLocaleString",
	}

	for _, keyword := range keywords {
		t.Run("select "+keyword, func(t *testing.T) {
			result := runXan(t, nil, "", "printf '"+keyword+",value\ntest,data\n' | xan select "+keyword)
			requireExitCode(t, result, 0)
			if !strings.Contains(result.Stdout, keyword) || !strings.Contains(result.Stdout, "test") {
				t.Fatalf("stdout = %q", result.Stdout)
			}
		})
	}

	for _, keyword := range keywords[:4] {
		t.Run("drop "+keyword, func(t *testing.T) {
			result := runXan(t, nil, "", "printf '"+keyword+",value,normal\ntest,data,keep\n' | xan drop "+keyword)
			requireExitCode(t, result, 0)
			if !strings.Contains(result.Stdout, "value,normal") {
				t.Fatalf("stdout = %q", result.Stdout)
			}
		})

		t.Run("sort "+keyword, func(t *testing.T) {
			result := runXan(t, nil, "", "printf '"+keyword+",data\nz,1\na,2\n' | xan sort -s "+keyword)
			requireExitCode(t, result, 0)
			lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
			if len(lines) != 3 || !strings.Contains(lines[1], "a") || !strings.Contains(lines[2], "z") {
				t.Fatalf("stdout = %q", result.Stdout)
			}
		})
	}

	result := runXan(t, nil, "", "printf 'constructor,prototype,hasOwnProperty,isPrototypeOf,toString\n' | xan headers")
	requireExitCode(t, result, 0)
	for _, keyword := range []string{"constructor", "prototype", "hasOwnProperty", "isPrototypeOf", "toString"} {
		if !strings.Contains(result.Stdout, keyword) {
			t.Fatalf("headers stdout = %q", result.Stdout)
		}
	}

	result = runXan(t, nil, "", "printf 'constructor,value\na|b,1\n' | xan explode constructor")
	requireExitCode(t, result, 0)
	if len(strings.Split(strings.TrimSpace(result.Stdout), "\n")) != 3 {
		t.Fatalf("explode stdout = %q", result.Stdout)
	}

	result = runXan(t, nil, "", "printf 'constructor,a,b\nprototype,1,2\n' | xan transpose")
	requireExitCode(t, result, 0)
	if !strings.Contains(result.Stdout, "constructor") || !strings.Contains(result.Stdout, "prototype") {
		t.Fatalf("transpose stdout = %q", result.Stdout)
	}

	result = runXan(t, nil, "", "printf 'value\na\nb\n' | xan enum -c constructor")
	requireExitCode(t, result, 0)
	if !strings.Contains(result.Stdout, "constructor") {
		t.Fatalf("enum stdout = %q", result.Stdout)
	}
}
