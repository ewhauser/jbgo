package runtime

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

type fuzzCommandMetadata struct {
	Name     string
	Category string
	Variants []fuzzCommandVariant
}

type fuzzCommandVariant struct {
	Tokens    []string
	StdinHint string
	Features  []string
}

type fuzzCoverageTracker struct {
	mu       sync.Mutex
	commands map[string]int
	flags    map[string]int
	features map[string]int
}

func newFuzzCoverageTracker() *fuzzCoverageTracker {
	return &fuzzCoverageTracker{
		commands: make(map[string]int),
		flags:    make(map[string]int),
		features: make(map[string]int),
	}
}

func (t *fuzzCoverageTracker) Record(spec fuzzCommandMetadata, variant fuzzCommandVariant) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.commands[spec.Name]++
	t.features["category:"+spec.Category]++
	for _, feature := range variant.Features {
		t.features[feature]++
	}
	for _, token := range variant.Tokens {
		if strings.HasPrefix(token, "-") {
			t.flags[fmt.Sprintf("%s:%s", spec.Name, token)]++
		}
	}
}

func (t *fuzzCoverageTracker) Missing(specs []fuzzCommandMetadata) (missingCommands, missingFlags []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, spec := range specs {
		if t.commands[spec.Name] == 0 {
			missingCommands = append(missingCommands, spec.Name)
		}
		seenFlags := make(map[string]struct{})
		for _, variant := range spec.Variants {
			for _, token := range variant.Tokens {
				if !strings.HasPrefix(token, "-") {
					continue
				}
				key := fmt.Sprintf("%s:%s", spec.Name, token)
				seenFlags[key] = struct{}{}
			}
		}
		for key := range seenFlags {
			if t.flags[key] == 0 {
				missingFlags = append(missingFlags, key)
			}
		}
	}

	slices.Sort(missingCommands)
	slices.Sort(missingFlags)
	return missingCommands, missingFlags
}

func mustFuzzCommandMetadata(tb testing.TB) []fuzzCommandMetadata {
	tb.Helper()

	specs := []fuzzCommandMetadata{
		fuzzSpec("echo", "shell",
			fuzzVariant("", "text", "{token.value}"),
			fuzzVariant("", "flag:n", "-n", "{token.value}"),
			fuzzVariant("", "flag:e", "-e", "{token.value}"),
			fuzzVariant("", "flag:E", "-E", "{token.value}"),
			fuzzVariant("", "flag:literal-dash", "--", "{token.value}"),
			fuzzVariant("", "flag:version", "--version"),
			fuzzVariant("", "flag:help", "--help"),
		),
		fuzzSpec("pwd", "shell",
			fuzzVariant("", "cwd"),
			fuzzVariant("", "flag:L", "-L"),
			fuzzVariant("", "flag:P", "-P"),
			fuzzVariant("", "flag:long", "--logical"),
			fuzzVariant("", "flag:long", "--physical"),
		),
		fuzzSpec("touch", "file",
			fuzzVariant("", "path.write", "{path.touch}"),
		),
		fuzzSpec("truncate", "file",
			fuzzVariant("", "path.write", "-s", "1", "{path.touch}"),
			fuzzVariant("", "flag:short", "-c", "-s", "1", "{path.touch}"),
			fuzzVariant("", "flag:short", "-r", "{path.text}", "{path.touch}"),
			fuzzVariant("", "flag:short", "-o", "-s", "+1", "{path.touch}"),
		),
		fuzzSpec("cat", "file",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "stdin", "-"),
			fuzzVariant("", "flag:long", "--number", "{path.text}"),
			fuzzVariant("", "flag:nonblank", "-b", "{path.text}"),
			fuzzVariant("", "flag:show-ends", "-E", "{path.text}"),
			fuzzVariant("", "flag:show-all", "-A", "{path.text}"),
			fuzzVariant("", "flag:squeeze", "-s", "{path.text}"),
		),
		fuzzSpec("cp", "file",
			fuzzVariant("", "path.copy", "{path.text}", "{path.copy}"),
			fuzzVariant("", "flag:long", "--no-clobber", "--preserve", "--verbose", "{path.text}", "{path.copy}"),
			fuzzVariant("", "flag:short", "-n", "-p", "-v", "{path.text}", "{path.copy}"),
		),
		fuzzSpec("mv", "file",
			fuzzVariant("", "path.move", "{path.copy}", "{path.moved}"),
			fuzzVariant("", "flag:long", "--force", "--no-clobber", "--verbose", "{path.copy}", "{path.moved}"),
			fuzzVariant("", "flag:short", "-f", "-n", "-v", "{path.copy}", "{path.moved}"),
		),
		fuzzSpec("ln", "file",
			fuzzVariant("", "path.link", "-s", "-f", "{path.text}", "{path.link}"),
		),
		fuzzSpec("link", "file",
			fuzzVariant("", "path.link", "{path.text}", "{path.copy}"),
		),
		fuzzSpec("unlink", "file",
			fuzzVariant("", "path.remove", "{path.remove}"),
			fuzzVariant("", "flag:help", "--help"),
			fuzzVariant("", "flag:version", "--version"),
		),
		fuzzSpec("ls", "file",
			fuzzVariant("", "path.dir", "{path.dir}"),
			fuzzVariant("", "flag:a", "-a", "{path.dir}"),
			fuzzVariant("", "flag:A", "-A", "{path.dir}"),
			fuzzVariant("", "flag:F", "-F", "{path.dir}"),
			fuzzVariant("", "flag:R", "-R", "{path.dir}"),
			fuzzVariant("", "flag:S", "-S", "{path.dir}"),
			fuzzVariant("", "flag:d", "-d", "{path.dir}"),
			fuzzVariant("", "flag:h", "-h", "-l", "{path.text}"),
			fuzzVariant("", "flag:l", "-l", "{path.dir}"),
			fuzzVariant("", "flag:r", "-r", "{path.dir}"),
			fuzzVariant("", "flag:t", "-t", "{path.dir}"),
			fuzzVariant("", "flag:1", "-1", "{path.dir}"),
			fuzzVariant("", "flag:long", "--all", "{path.dir}"),
			fuzzVariant("", "flag:long", "--almost-all", "{path.dir}"),
			fuzzVariant("", "flag:long", "--classify", "{path.dir}"),
			fuzzVariant("", "flag:long", "--directory", "{path.dir}"),
			fuzzVariant("", "flag:long", "--human-readable", "-l", "{path.text}"),
			fuzzVariant("", "flag:long", "--recursive", "{path.dir}"),
			fuzzVariant("", "flag:long", "--reverse", "{path.dir}"),
		),
		fuzzSpec("dir", "file",
			fuzzVariant("", "path.dir", "{path.dir}"),
			fuzzVariant("", "flag:l", "-l", "{path.dir}"),
		),
		fuzzSpec("rmdir", "file",
			fuzzVariant("", "path.rmdir", "{path.rmdir}"),
		),
		fuzzSpec("readlink", "file",
			fuzzVariant("", "path.link", "{path.link}"),
			fuzzVariant("", "flag:f", "-f", "{path.link}"),
		),
		fuzzSpec("realpath", "file",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("", "flag:z", "-z", "{path.text}"),
			fuzzVariant("", "flag:s", "-s", "{path.link}"),
			fuzzVariant("", "flag:L", "-L", "{path.link}/.."),
			fuzzVariant("", "flag:P", "-P", "{path.link}/.."),
			fuzzVariant("", "flag:E", "-E", "/tmp/missing"),
			fuzzVariant("", "flag:e", "-e", "{path.text}"),
			fuzzVariant("", "flag:m", "-m", "/tmp/missing"),
			fuzzVariant("", "flag:q", "-q", "/tmp/missing"),
			fuzzVariant("", "flag:long", "--relative-to={path.dir}", "{path.text}"),
			fuzzVariant("", "flag:long", "--relative-base={path.dir}", "{path.text}"),
		),
		fuzzSpec("stat", "file",
			fuzzVariant("", "path.read", "{path.text}"),
		),
		fuzzSpec("basename", "file",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("", "flag:long", "--suffix", ".txt", "{path.text}"),
		),
		fuzzSpec("dirname", "file",
			fuzzVariant("", "path.read", "{path.text}"),
		),
		fuzzSpec("tree", "file",
			fuzzVariant("", "path.dir", "{path.dir}"),
			fuzzVariant("", "flag:a", "-a", "{path.dir}"),
			fuzzVariant("", "flag:L", "-L", "2", "{path.dir}"),
		),
		fuzzSpec("du", "file",
			fuzzVariant("", "path.dir", "{path.dir}"),
			fuzzVariant("", "flag:a", "-a", "{path.dir}"),
			fuzzVariant("", "flag:s", "-s", "{path.dir}"),
		),
		fuzzSpec("file", "file",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("", "flag:b", "-b", "{path.text}"),
			fuzzVariant("", "flag:long", "--brief", "{path.text}"),
		),
		fuzzSpec("find", "file",
			fuzzVariant("", "path.dir", "{path.dir}"),
			fuzzVariant("", "path.dir", "{path.dir}", "-name", "{pattern.glob}"),
			fuzzVariant("", "path.dir", "{path.dir}", "-iname", "{pattern.glob}"),
			fuzzVariant("", "path.dir", "{path.dir}", "-path", "{pattern.glob}"),
			fuzzVariant("", "path.dir", "{path.dir}", "-ipath", "{pattern.glob}"),
			fuzzVariant("", "path.dir", "{path.dir}", "-regex", ".*"),
			fuzzVariant("", "path.dir", "{path.dir}", "-iregex", ".*"),
			fuzzVariant("", "path.dir", "{path.dir}", "-empty"),
			fuzzVariant("", "path.dir", "{path.dir}", "-mtime", "0"),
			fuzzVariant("", "path.dir", "{path.dir}", "-newer", "{path.text}"),
			fuzzVariant("", "path.dir", "{path.dir}", "-size", "1c"),
			fuzzVariant("", "path.dir", "{path.dir}", "-perm", "755"),
			fuzzVariant("", "path.dir", "{path.dir}", "-prune"),
			fuzzVariant("", "path.dir", "{path.dir}", "-mindepth", "1"),
			fuzzVariant("", "path.dir", "{path.dir}", "-depth"),
			fuzzVariant("", "path.dir", "{path.dir}", "-exec", "echo", "{}", ";"),
			fuzzVariant("", "path.dir", "{path.dir}", "-print"),
			fuzzVariant("", "path.dir", "{path.dir}", "-print0"),
			fuzzVariant("", "path.dir", "{path.dir}", "-printf", "%p\\n"),
			fuzzVariant("", "path.dir", "{path.dir}", "-delete"),
		),
		fuzzSpec("grep", "text",
			fuzzVariant("", "pattern", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:n", "-n", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:i", "-i", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:long", "--fixed-strings", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:long", "--line-regexp", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:long", "--only-matching", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:long", "--files-without-match", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:long", "--quiet", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:short", "-A1", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:short", "-B1", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:short", "-C1", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:short", "-F", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:short", "-L", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:short", "-P", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:short", "-h", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:short", "-m1", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:short", "-o", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:short", "-q", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:short", "-x", "{token.pattern}", "{path.text}"),
		),
		fuzzSpec("rg", "text",
			fuzzVariant("", "pattern", "{token.pattern}", "{path.dir}"),
			fuzzVariant("", "flag:n", "-n", "{token.pattern}", "{path.dir}"),
			fuzzVariant("", "flag:i", "-i", "{token.pattern}", "{path.dir}"),
		),
		fuzzSpec("head", "text",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("", "flag:n", "-n", "3", "{path.text}"),
		),
		fuzzSpec("tail", "text",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("", "flag:n", "-n", "3", "{path.text}"),
		),
		fuzzSpec("wc", "text",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("", "flag:l", "-l", "{path.text}"),
			fuzzVariant("", "flag:w", "-w", "{path.text}"),
			fuzzVariant("", "flag:c", "-c", "{path.text}"),
		),
		fuzzSpec("seq", "text",
			fuzzVariant("", "range", "5"),
			fuzzVariant("", "flag:s", "-s", ",", "1", "3"),
			fuzzVariant("", "flag:t", "-t", "!", "1", "3"),
			fuzzVariant("", "flag:w", "-w", "1", "5"),
			fuzzVariant("", "flag:f", "-f", "%.2f", "0", "0.5", "2"),
		),
		fuzzSpec("sort", "text",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("", "flag:r", "-r", "{path.text}"),
			fuzzVariant("", "flag:u", "-u", "{path.text}"),
			fuzzVariant("", "flag:C", "-C", "{path.sorted}"),
			fuzzVariant("", "flag:g", "-g", "{path.text}"),
			fuzzVariant("", "flag:i", "-i", "{path.text}"),
			fuzzVariant("", "flag:m", "-m", "{path.sorted}", "{path.sorted2}"),
			fuzzVariant("", "flag:R", "-R", "--random-source={path.text}", "{path.text}"),
			fuzzVariant("", "flag:z", "-z", "{path.text}"),
			fuzzVariant("", "flag:long", "--ignore-leading-blanks", "--dictionary-order", "{path.text}"),
			fuzzVariant("", "flag:long", "--debug", "{path.text}"),
			fuzzVariant("", "flag:long", "--general-numeric-sort", "{path.text}"),
			fuzzVariant("", "flag:long", "--human-numeric-sort", "{path.text}"),
			fuzzVariant("", "flag:long", "--ignore-nonprinting", "{path.text}"),
			fuzzVariant("", "flag:long", "--merge", "{path.sorted}", "{path.sorted2}"),
			fuzzVariant("", "flag:long", "--month-sort", "{path.text}"),
			fuzzVariant("", "flag:long", "--parallel=2", "--batch-size=2", "{path.text}"),
			fuzzVariant("", "flag:long", "--compress-program=cat", "{path.text}"),
			fuzzVariant("", "flag:long", "--sort=version", "{path.text}"),
			fuzzVariant("", "flag:long", "--version-sort", "{path.text}"),
			fuzzVariant("", "flag:long", "--field-separator=,", "--key=2,2n", "{path.csv}"),
			fuzzVariant("", "flag:long", "--check", "{path.sorted}"),
			fuzzVariant("", "flag:long", "--output", "{path.out}", "{path.text}"),
			fuzzVariant("", "flag:short", "-b", "-d", "{path.text}"),
			fuzzVariant("", "flag:short", "-h", "{path.text}"),
			fuzzVariant("", "flag:short", "-M", "{path.text}"),
			fuzzVariant("", "flag:short", "-V", "{path.text}"),
			fuzzVariant("", "flag:short", "-c", "{path.sorted}"),
			fuzzVariant("", "flag:short", "-o", "{path.out}", "{path.text}"),
			fuzzVariant("", "flag:short", "-s", "{path.text}"),
		),
		fuzzSpec("tsort", "text",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "stdin"),
			fuzzVariant("", "flag:w", "-w", "{path.text}"),
			fuzzVariant("", "flag:help", "--help"),
			fuzzVariant("", "flag:version", "--version"),
		),
		fuzzSpec("uniq", "text",
			fuzzVariant("", "path.read", "{path.sorted}"),
			fuzzVariant("", "flag:c", "-c", "{path.sorted}"),
			fuzzVariant("", "flag:d", "-d", "{path.sorted}"),
			fuzzVariant("", "flag:i", "--ignore-case", "{path.sorted}"),
		),
		fuzzSpec("cut", "text",
			fuzzVariant("", "flag:b", "-b", "1-3", "{path.text}"),
			fuzzVariant("", "flag:c", "-c", "1-3", "{path.text}"),
			fuzzVariant("", "flag:complement", "--complement", "-c", "2-4", "{path.text}"),
			fuzzVariant("", "flag:fd", "-d", ",", "-f", "1", "{path.csv}"),
			fuzzVariant("", "flag:long", "--only-delimited", "-d", ",", "-f", "1", "{path.csv}"),
			fuzzVariant("", "flag:z", "-z", "-d", ",", "-f", "1", "{path.csv}"),
		),
		fuzzSpec("sed", "text",
			fuzzVariant("", "script", "{program.sed}", "{path.text}"),
			fuzzVariant("", "flag:n", "-n", "{program.sed}", "{path.text}"),
			fuzzVariant("", "flag:f", "-f", "{path.sedscript}", "{path.text}"),
		),
		fuzzSpec("printf", "shell",
			fuzzVariant("", "format", "%s\\n", "{token.value}"),
		),
		fuzzSpec("tee", "shell",
			fuzzVariant("text", "stdin", "{path.out}"),
			fuzzVariant("text", "flag:a", "-a", "{path.out}"),
		),
		fuzzSpec("env", "shell",
			fuzzVariant("", "flag:i", "-i", "ONLY={token.value}", "printenv", "ONLY"),
			fuzzVariant("", "flag:u", "-u", "HOME", "printenv", "PWD"),
			fuzzVariant("", "flag:long", "--ignore-environment", "ONLY={token.value}", "printenv", "ONLY"),
		),
		fuzzSpec("test", "shell",
			fuzzVariant("", "predicate", "-e", "{path.text}"),
			fuzzVariant("", "predicate", "{token.value}", "=", "{token.value}"),
		),
		fuzzSpec("[", "shell",
			fuzzVariant("", "predicate", "-e", "{path.text}"),
			fuzzVariant("", "predicate", "{token.value}", "=", "{token.value}"),
		),
		fuzzSpec("id", "shell",
			fuzzVariant("", "identity"),
			fuzzVariant("", "flag:a", "-a"),
			fuzzVariant("", "flag:A", "-A"),
			fuzzVariant("", "flag:u", "-u"),
			fuzzVariant("", "flag:g", "-g"),
			fuzzVariant("", "flag:G", "-G"),
			fuzzVariant("", "flag:n", "-u", "-n"),
			fuzzVariant("", "flag:P", "-P"),
			fuzzVariant("", "flag:p", "-p"),
			fuzzVariant("", "flag:r", "-u", "-r"),
			fuzzVariant("", "flag:z", "-G", "-z"),
			fuzzVariant("", "flag:Z", "-Z"),
		),
		fuzzSpec("arch", "shell",
			fuzzVariant("", "machine"),
			fuzzVariant("", "flag:h", "-h"),
			fuzzVariant("", "flag:help", "--help"),
			fuzzVariant("", "flag:V", "-V"),
			fuzzVariant("", "flag:version", "--version"),
			fuzzVariant("", "flag:infer-long", "--ver"),
		),
		fuzzSpec("whoami", "shell",
			fuzzVariant("", "identity"),
		),
		fuzzSpec("printenv", "shell",
			fuzzVariant("", "env", "HOME"),
		),
		fuzzSpec("true", "shell",
			fuzzVariant("", "builtin"),
		),
		fuzzSpec("false", "shell",
			fuzzVariant("", "builtin"),
		),
		fuzzSpec("which", "shell",
			fuzzVariant("", "lookup", "echo"),
		),
		fuzzSpec("dircolors", "shell",
			fuzzVariant("", "flag:b", "-b"),
			fuzzVariant("text", "stdin", "-c", "-"),
			fuzzVariant("", "flag:p", "--print-database"),
			fuzzVariant("", "flag:display", "--print-ls-colors"),
		),
		fuzzSpec("help", "shell",
			fuzzVariant("", "builtin", "pwd"),
			fuzzVariant("", "flag:s", "-s", "pwd"),
		),
		fuzzSpec("yes", "shell",
			fuzzVariant("", "flag:help", "--help"),
			fuzzVariant("", "flag:version", "--version"),
		),
		fuzzSpec("date", "shell",
			fuzzVariant("", "format"),
			fuzzVariant("", "flag:ud", "-u", "-d", "{date.fixed}", "{date.format}"),
			fuzzVariant("", "flag:long", "--utc", "--date", "{date.fixed}", "--iso-8601"),
		),
		fuzzSpec("uptime", "shell",
			fuzzVariant("", "status"),
			fuzzVariant("", "flag:s", "-s"),
			fuzzVariant("", "flag:p", "-p"),
		),
		fuzzSpec("sleep", "shell",
			fuzzVariant("", "duration", "{duration.short}"),
		),
		fuzzSpec("timeout", "shell",
			fuzzVariant("", "duration", "{duration.timeout}", "sleep", "{duration.short}"),
			fuzzVariant("", "flag:long", "--signal", "TERM", "--kill-after", "{duration.short}", "{duration.timeout}", "sleep", "{duration.short}"),
		),
		fuzzSpec("xargs", "shell",
			fuzzVariant("text", "stdin", "-n", "1", "echo"),
			fuzzVariant("text", "stdin", "--null", "--verbose", "--max-args", "1", "echo"),
			fuzzVariant("text", "stdin", "--no-run-if-empty", "echo", "skip"),
		),
		fuzzSpec("bash", "shell",
			fuzzVariant("", "flag:c", "-c", "{script.echo}", "ignored", "{token.value}"),
		),
		fuzzSpec("sh", "shell",
			fuzzVariant("script", "stdin", "-s"),
			fuzzVariant("", "flag:c", "-c", "{script.echo}"),
		),
		fuzzSpec("comm", "text",
			fuzzVariant("", "paths", "-12", "{path.sorted}", "{path.sorted2}"),
			fuzzVariant("", "flag:1", "-1", "{path.sorted}", "{path.sorted2}"),
			fuzzVariant("", "flag:total", "--total", "{path.sorted}", "{path.sorted2}"),
			fuzzVariant("", "flag:delimiter", "--output-delimiter=,", "{path.sorted}", "{path.sorted2}"),
			fuzzVariant("", "flag:nocheck", "--nocheck-order", "{path.sorted}", "{path.sorted2}"),
			fuzzVariant("", "flag:zero", "-z", "{path.sorted}", "{path.sorted2}"),
		),
		fuzzSpec("column", "text",
			fuzzVariant("text", "stdin", "-t"),
			fuzzVariant("text", "stdin", "--table", "-s", ",", "-o", "|", "-c", "20", "-n"),
		),
		fuzzSpec("paste", "text",
			fuzzVariant("", "paths", "{path.text}", "{path.alttext}"),
			fuzzVariant("", "flag:d", "-d", ",", "{path.text}", "{path.alttext}"),
			fuzzVariant("", "flag:long", "--serial", "--delimiters=,", "{path.text}"),
			fuzzVariant("", "flag:z", "-z", "-d", "\\0,", "{path.text}", "{path.alttext}"),
		),
		fuzzSpec("tr", "text",
			fuzzVariant("text", "stdin", "a-z", "A-Z"),
			fuzzVariant("text", "flag:long", "--delete", "[:digit:]"),
		),
		fuzzSpec("rev", "text",
			fuzzVariant("", "path.read", "{path.text}"),
		),
		fuzzSpec("nl", "text",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("", "flag:ba", "-ba", "{path.text}"),
			fuzzVariant("", "flag:n", "-n", "rz", "{path.text}"),
			fuzzVariant("", "flag:sections", "-ha", "-fa", "-d", "x", "{path.text}"),
			fuzzVariant("", "flag:negative", "-p", "-i", "-10", "{path.text}"),
		),
		fuzzSpec("join", "text",
			fuzzVariant("", "paths", "{path.joinleft}", "{path.joinright}"),
		),
		fuzzSpec("split", "text",
			fuzzVariant("", "flag:l", "-l", "2", "{path.text}", "{path.splitprefix}"),
		),
		fuzzSpec("tac", "text",
			fuzzVariant("", "path.read", "{path.text}"),
		),
		fuzzSpec("diff", "text",
			fuzzVariant("", "flag:u", "-u", "{path.text}", "{path.alttext}"),
			fuzzVariant("", "flag:long", "--brief", "--ignore-case", "{path.text}", "{path.alttext}"),
		),
		fuzzSpec("expr", "shell",
			fuzzVariant("", "arithmetic", "12", "+", "4"),
			fuzzVariant("", "compare", "1", "<", "2"),
			fuzzVariant("", "regex", "./tests/init.sh", ":", ".*/\\(.*\\)$"),
		),
		fuzzSpec("base32", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "flag:di", "-d", "-i"),
			fuzzVariant("", "flag:long", "--wrap", "0", "{path.text}"),
		),
		fuzzSpec("base64", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("base64", "flag:d", "-d"),
			fuzzVariant("", "flag:long", "--wrap", "0", "{path.text}"),
		),
		fuzzSpec("basenc", "data",
			fuzzVariant("", "flag:base64", "--base64", "{path.text}"),
			fuzzVariant("base64", "flag:decode", "--base64", "-d"),
			fuzzVariant("", "flag:base32", "--base32", "{path.text}"),
			fuzzVariant("", "flag:base16", "--base16", "{path.text}"),
		),
		fuzzSpec("od", "data",
			fuzzVariant("", "flag:hex", "-An", "-tx1", "{path.text}"),
			fuzzVariant("", "flag:char", "-An", "-c", "{path.text}"),
		),
		fuzzSpec("md5sum", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "stdin"),
			fuzzVariant("", "flag:c", "-c", "{path.text}"),
			fuzzVariant("", "flag:short", "-b", "{path.text}"),
			fuzzVariant("", "flag:short", "-t", "{path.text}"),
			fuzzVariant("", "flag:tag", "--binary", "--tag", "{path.text}"),
			fuzzVariant("", "flag:zero", "--zero", "{path.text}"),
		),
		fuzzSpec("sha1sum", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "stdin"),
			fuzzVariant("", "flag:c", "-c", "{path.text}"),
			fuzzVariant("", "flag:short", "-b", "{path.text}"),
			fuzzVariant("", "flag:short", "-t", "{path.text}"),
			fuzzVariant("", "flag:tag", "--binary", "--tag", "{path.text}"),
			fuzzVariant("", "flag:zero", "--zero", "{path.text}"),
		),
		fuzzSpec("sha224sum", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "stdin"),
			fuzzVariant("", "flag:c", "-c", "{path.text}"),
			fuzzVariant("", "flag:short", "-b", "{path.text}"),
			fuzzVariant("", "flag:short", "-t", "{path.text}"),
			fuzzVariant("", "flag:tag", "--binary", "--tag", "{path.text}"),
			fuzzVariant("", "flag:zero", "--zero", "{path.text}"),
		),
		fuzzSpec("sha256sum", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "stdin"),
			fuzzVariant("", "flag:c", "-c", "{path.text}"),
			fuzzVariant("", "flag:short", "-b", "{path.text}"),
			fuzzVariant("", "flag:short", "-t", "{path.text}"),
			fuzzVariant("", "flag:tag", "--binary", "--tag", "{path.text}"),
			fuzzVariant("", "flag:zero", "--zero", "{path.text}"),
		),
		fuzzSpec("sha384sum", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "stdin"),
			fuzzVariant("", "flag:c", "-c", "{path.text}"),
			fuzzVariant("", "flag:short", "-b", "{path.text}"),
			fuzzVariant("", "flag:short", "-t", "{path.text}"),
			fuzzVariant("", "flag:tag", "--binary", "--tag", "{path.text}"),
			fuzzVariant("", "flag:zero", "--zero", "{path.text}"),
		),
		fuzzSpec("sha512sum", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "stdin"),
			fuzzVariant("", "flag:c", "-c", "{path.text}"),
			fuzzVariant("", "flag:short", "-b", "{path.text}"),
			fuzzVariant("", "flag:short", "-t", "{path.text}"),
			fuzzVariant("", "flag:tag", "--binary", "--tag", "{path.text}"),
			fuzzVariant("", "flag:zero", "--zero", "{path.text}"),
		),
		fuzzSpec("b2sum", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "stdin"),
			fuzzVariant("", "flag:c", "-c", "{path.text}"),
			fuzzVariant("", "flag:short", "-b", "{path.text}"),
			fuzzVariant("", "flag:short", "-t", "{path.text}"),
			fuzzVariant("", "flag:tag", "--binary", "--tag", "{path.text}"),
			fuzzVariant("", "flag:zero", "--zero", "{path.text}"),
			fuzzVariant("", "flag:length", "--length=128", "{path.text}"),
			fuzzVariant("", "flag:length-short", "-l128", "{path.text}"),
		),
		fuzzSpec("cksum", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "stdin"),
			fuzzVariant("", "flag:algo-md5", "-a", "md5", "{path.text}"),
			fuzzVariant("", "flag:algo-sha2", "-a", "sha2", "-l", "256", "{path.text}"),
			fuzzVariant("", "flag:algo-blake2b", "-a", "blake2b", "--length=128", "{path.text}"),
			fuzzVariant("", "flag:check", "-c", "{path.text}"),
			fuzzVariant("", "flag:untagged", "-a", "md5", "--untagged", "{path.text}"),
			fuzzVariant("", "flag:raw", "-a", "blake2b", "--length=128", "--raw", "{path.text}"),
		),
		fuzzSpec("tar", "data",
			fuzzVariant("", "flag:cf", "-cf", "{path.archive}", "{path.dir}"),
			fuzzVariant("", "flag:tf", "-tf", "{path.tarfixture}"),
		),
		fuzzSpec("gzip", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("", "flag:c", "-c", "{path.text}"),
		),
		fuzzSpec("gunzip", "data",
			fuzzVariant("", "flag:c", "-c", "{path.gzip}"),
			fuzzVariant("", "flag:t", "-t", "{path.gzip}"),
		),
		fuzzSpec("zcat", "data",
			fuzzVariant("", "path.read", "{path.gzip}"),
		),
		fuzzSpec("chmod", "file",
			fuzzVariant("", "mode", "600", "{path.text}"),
			fuzzVariant("", "flag:R", "-R", "700", "{path.dir}"),
		),
		fuzzSpec("chown", "file",
			fuzzVariant("", "owner", "123:456", "{path.text}"),
			fuzzVariant("", "flag:h", "-h", "321:654", "{path.link}"),
			fuzzVariant("", "flag:R", "-R", "111:222", "{path.dir}"),
		),
		fuzzSpec("chgrp", "file",
			fuzzVariant("", "group", "456", "{path.text}"),
			fuzzVariant("", "flag:h", "-h", "654", "{path.link}"),
			fuzzVariant("", "flag:R", "-R", "111", "{path.dir}"),
			fuzzVariant("", "flag:reference", "--reference={path.text}", "{path.link}"),
		),
		fuzzSpec("yq", "data",
			fuzzVariant("", "filter", "{yq.filter}", "{path.yaml}"),
			fuzzVariant("", "flag:n", "-n", "{yq.build}"),
			fuzzVariant("", "flag:pj", "-p", "json", "-o", "json", "{yq.filter}", "{path.json}"),
		),
		fuzzSpec("mkdir", "file",
			fuzzVariant("", "path.write", "-p", "{path.mkdir}"),
		),
		fuzzSpec("mktemp", "file",
			fuzzVariant("", "path.write", "/tmp/fuzz.XXXX"),
			fuzzVariant("", "flag:d", "-d", "/tmp/fuzzdir.XXXX"),
			fuzzVariant("", "flag:u,flag:suffix", "-u", "--suffix=.txt", "/tmp/fuzzdry.XXXX"),
			fuzzVariant("", "flag:q,flag:p", "-q", "-p", "/no/such/dir"),
			fuzzVariant("", "flag:p", "-p", "/tmp", "fuzz.XXXX"),
			fuzzVariant("", "flag:tmpdir", "--tmpdir=.", "fuzz.XXXX"),
			fuzzVariant("", "flag:t", "-t", "fuzz.XXXX"),
		),
		fuzzSpec("rm", "file",
			fuzzVariant("", "path.remove", "-f", "{path.remove}"),
			fuzzVariant("", "flag:r", "-r", "-f", "{path.rmdir}"),
		),
	}

	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if _, ok := seen[spec.Name]; ok {
			tb.Fatalf("duplicate fuzz metadata for %q", spec.Name)
		}
		seen[spec.Name] = struct{}{}
		if len(spec.Variants) == 0 {
			tb.Fatalf("missing fuzz variants for %q", spec.Name)
		}
	}

	rt := newFuzzRuntime(tb)
	registryNames := slices.DeleteFunc(rt.cfg.Registry.Names(), func(name string) bool {
		return strings.HasPrefix(name, "__jb_")
	})
	for _, name := range registryNames {
		if _, ok := seen[name]; !ok {
			_, ok := rt.cfg.Registry.Lookup(name)
			if !ok {
				tb.Fatalf("registry missing command %q during fuzz metadata validation", name)
			}
			if runtimeCommandReturnsNotImplemented(tb, rt, name) {
				spec := fuzzSpec(name, "shell", fuzzVariant("", "placeholder"))
				specs = append(specs, spec)
				seen[name] = struct{}{}
				continue
			}
			tb.Fatalf("missing fuzz metadata for registered command %q", name)
		}
	}

	return specs
}

func runtimeCommandReturnsNotImplemented(tb testing.TB, rt *Runtime, name string) bool {
	tb.Helper()

	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: name + "\n"})
	if err != nil {
		tb.Fatalf("Run(%q) error = %v", name, err)
	}
	if result.ExitCode != 1 {
		return false
	}
	return result.Stderr == fmt.Sprintf("%s: not implemented\n", name)
}

func fuzzSpec(name, category string, variants ...fuzzCommandVariant) fuzzCommandMetadata {
	return fuzzCommandMetadata{
		Name:     name,
		Category: category,
		Variants: variants,
	}
}

func fuzzVariant(stdinHint, features string, tokens ...string) fuzzCommandVariant {
	featureList := []string(nil)
	if features != "" {
		featureList = append(featureList, features)
	}
	if stdinHint != "" {
		featureList = append(featureList, "stdin:"+stdinHint)
	}
	return fuzzCommandVariant{
		Tokens:    tokens,
		StdinHint: stdinHint,
		Features:  featureList,
	}
}

func TestFuzzMetadataSeedCoverage(t *testing.T) {
	specs := mustFuzzCommandMetadata(t)
	tracker := newFuzzCoverageTracker()
	for _, spec := range specs {
		for _, variant := range spec.Variants {
			tracker.Record(spec, variant)
		}
	}

	missingCommands, missingFlags := tracker.Missing(specs)
	if len(missingCommands) > 0 {
		t.Fatalf("missing fuzz command seed coverage: %v", missingCommands)
	}
	if len(missingFlags) > 0 {
		t.Fatalf("missing fuzz flag seed coverage: %v", missingFlags)
	}
}
