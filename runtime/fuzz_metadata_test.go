package runtime

import (
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
		),
		fuzzSpec("pwd", "shell",
			fuzzVariant("", "cwd"),
		),
		fuzzSpec("touch", "file",
			fuzzVariant("", "path.write", "{path.touch}"),
		),
		fuzzSpec("cat", "file",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("text", "stdin", "-"),
		),
		fuzzSpec("cp", "file",
			fuzzVariant("", "path.copy", "{path.text}", "{path.copy}"),
		),
		fuzzSpec("mv", "file",
			fuzzVariant("", "path.move", "{path.copy}", "{path.moved}"),
		),
		fuzzSpec("ln", "file",
			fuzzVariant("", "path.link", "-s", "-f", "{path.text}", "{path.link}"),
		),
		fuzzSpec("ls", "file",
			fuzzVariant("", "path.dir", "{path.dir}"),
			fuzzVariant("", "flag:a", "-a", "{path.dir}"),
			fuzzVariant("", "flag:l", "-l", "{path.dir}"),
		),
		fuzzSpec("rmdir", "file",
			fuzzVariant("", "path.rmdir", "{path.rmdir}"),
		),
		fuzzSpec("readlink", "file",
			fuzzVariant("", "path.link", "{path.link}"),
			fuzzVariant("", "flag:f", "-f", "{path.link}"),
		),
		fuzzSpec("stat", "file",
			fuzzVariant("", "path.read", "{path.text}"),
		),
		fuzzSpec("basename", "file",
			fuzzVariant("", "path.read", "{path.text}"),
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
		),
		fuzzSpec("find", "file",
			fuzzVariant("", "path.dir", "{path.dir}"),
			fuzzVariant("", "path.dir", "{path.dir}", "-name", "{pattern.glob}"),
		),
		fuzzSpec("grep", "text",
			fuzzVariant("", "pattern", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:n", "-n", "{token.pattern}", "{path.text}"),
			fuzzVariant("", "flag:i", "-i", "{token.pattern}", "{path.text}"),
		),
		fuzzSpec("rg", "text",
			fuzzVariant("", "pattern", "{token.pattern}", "{path.dir}"),
			fuzzVariant("", "flag:n", "-n", "{token.pattern}", "{path.dir}"),
			fuzzVariant("", "flag:i", "-i", "{token.pattern}", "{path.dir}"),
		),
		fuzzSpec("awk", "text",
			fuzzVariant("", "program", "{program.awk}", "{path.text}"),
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
		fuzzSpec("sort", "text",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("", "flag:r", "-r", "{path.text}"),
			fuzzVariant("", "flag:u", "-u", "{path.text}"),
		),
		fuzzSpec("uniq", "text",
			fuzzVariant("", "path.read", "{path.sorted}"),
			fuzzVariant("", "flag:c", "-c", "{path.sorted}"),
			fuzzVariant("", "flag:d", "-d", "{path.sorted}"),
		),
		fuzzSpec("cut", "text",
			fuzzVariant("", "flag:c", "-c", "1-3", "{path.text}"),
			fuzzVariant("", "flag:fd", "-d", ",", "-f", "1", "{path.csv}"),
		),
		fuzzSpec("sed", "text",
			fuzzVariant("", "script", "{program.sed}", "{path.text}"),
			fuzzVariant("", "flag:n", "-n", "{program.sed}", "{path.text}"),
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
		fuzzSpec("help", "shell",
			fuzzVariant("", "builtin", "pwd"),
			fuzzVariant("", "flag:s", "-s", "pwd"),
		),
		fuzzSpec("date", "shell",
			fuzzVariant("", "format"),
			fuzzVariant("", "flag:ud", "-u", "-d", "{date.fixed}", "{date.format}"),
			fuzzVariant("", "flag:long", "--utc", "--date", "{date.fixed}", "--iso-8601"),
		),
		fuzzSpec("sleep", "shell",
			fuzzVariant("", "duration", "{duration.short}"),
		),
		fuzzSpec("timeout", "shell",
			fuzzVariant("", "duration", "{duration.timeout}", "sleep", "{duration.short}"),
		),
		fuzzSpec("xargs", "shell",
			fuzzVariant("text", "stdin", "-n", "1", "echo"),
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
		),
		fuzzSpec("paste", "text",
			fuzzVariant("", "paths", "{path.text}", "{path.alttext}"),
			fuzzVariant("", "flag:d", "-d", ",", "{path.text}", "{path.alttext}"),
		),
		fuzzSpec("tr", "text",
			fuzzVariant("text", "stdin", "a-z", "A-Z"),
		),
		fuzzSpec("rev", "text",
			fuzzVariant("", "path.read", "{path.text}"),
		),
		fuzzSpec("nl", "text",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("", "flag:ba", "-ba", "{path.text}"),
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
		fuzzSpec("base64", "data",
			fuzzVariant("", "path.read", "{path.text}"),
			fuzzVariant("base64", "flag:d", "-d"),
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
		fuzzSpec("jq", "data",
			fuzzVariant("", "filter", "-r", "{jq.filter}", "{path.json}"),
			fuzzVariant("", "flag:n", "-n", "--arg", "value", "{token.value}", "{jq.build}"),
		),
		fuzzSpec("yq", "data",
			fuzzVariant("", "filter", "{yq.filter}", "{path.yaml}"),
			fuzzVariant("", "flag:n", "-n", "{yq.build}"),
			fuzzVariant("", "flag:pj", "-p", "json", "-o", "json", "{yq.filter}", "{path.json}"),
		),
		fuzzSpec("sqlite3", "data",
			fuzzVariant("", "mode:list", ":memory:", "{sqlite.query}"),
			fuzzVariant("", "mode:json", "-json", "{path.sqlite}", "{sqlite.write}"),
		),
		fuzzSpec("mkdir", "file",
			fuzzVariant("", "path.write", "-p", "{path.mkdir}"),
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

	registryNames := slices.DeleteFunc(commandsForFuzzMetadata(tb), func(name string) bool {
		return strings.HasPrefix(name, "__jb_")
	})
	for _, name := range registryNames {
		if _, ok := seen[name]; !ok {
			tb.Fatalf("missing fuzz metadata for registered command %q", name)
		}
	}

	return specs
}

func commandsForFuzzMetadata(tb testing.TB) []string {
	tb.Helper()

	rt := newFuzzRuntime(tb)
	return rt.cfg.Registry.Names()
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
