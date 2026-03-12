package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

var generatedFuzzCoverage = newFuzzCoverageTracker()

type fuzzCursor struct {
	data []byte
	idx  int
}

type fuzzFixtures struct {
	values   map[string]string
	replacer *strings.Replacer
}

func newFuzzCursor(data []byte) *fuzzCursor {
	return &fuzzCursor{data: clampFuzzData(data)}
}

func (c *fuzzCursor) next() byte {
	if len(c.data) == 0 {
		c.idx++
		return byte((c.idx*31 + 17) % 251)
	}
	b := c.data[c.idx%len(c.data)]
	c.idx++
	return b
}

func (c *fuzzCursor) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(c.next()) % n
}

func prepareFuzzFixtures(t *testing.T, session *Session, raw []byte) fuzzFixtures {
	t.Helper()

	textBytes := normalizeFuzzText(raw)
	text := string(textBytes)
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) == 0 {
		lines = []string{"alpha", "beta"}
	}

	sortedLines := append([]string(nil), lines...)
	sort.Strings(sortedLines)
	sortedText := strings.Join(sortedLines, "\n") + "\n"
	sortedUnique := uniqueStrings(sortedLines)
	altText := strings.ToUpper(text)
	csvText := "name,value\nalpha,1\nbeta,2\n"
	jsonDoc := map[string]any{
		"value": sanitizeFuzzToken(string(raw)),
		"items": []string{sanitizeFuzzToken(string(raw)), "BETA"},
	}
	jsonBytes, err := json.Marshal(jsonDoc)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	yamlText := fmt.Sprintf("value: %s\nitems:\n  - %s\n  - BETA\n", sanitizeFuzzToken(string(raw)), sanitizeFuzzToken(string(raw)))
	base64Text := base64.StdEncoding.EncodeToString(textBytes)

	writeSessionFile(t, session, "/tmp/text.txt", textBytes)
	writeSessionFile(t, session, "/tmp/alt.txt", []byte(altText))
	writeSessionFile(t, session, "/tmp/sorted.txt", []byte(sortedText))
	writeSessionFile(t, session, "/tmp/sorted2.txt", []byte(strings.Join(sortedUnique, "\n")+"\n"))
	writeSessionFile(t, session, "/tmp/data.csv", []byte(csvText))
	writeSessionFile(t, session, "/tmp/data.json", jsonBytes)
	writeSessionFile(t, session, "/tmp/data.yaml", []byte(yamlText))
	writeSessionFile(t, session, "/tmp/raw.json", clampFuzzData(raw))
	writeSessionFile(t, session, "/tmp/base64.txt", []byte(base64Text))
	writeSessionFile(t, session, "/tmp/text.txt.gz", buildGzipFixture(t, textBytes))
	writeSessionFile(t, session, "/tmp/archive-fixture.tar", buildTarFixture(t,
		tarFixtureEntry{Name: "tmp/dir/a.txt", Body: []byte("alpha\nbeta\n")},
		tarFixtureEntry{Name: "tmp/dir/nested/b.txt", Body: []byte("beta\ngamma\n")},
	))
	writeSessionFile(t, session, "/tmp/copy.txt", []byte("copy-source\n"))
	writeSessionFile(t, session, "/tmp/remove.txt", []byte("remove-me\n"))
	writeSessionFile(t, session, "/tmp/script.sh", []byte("echo generated-script\n"))
	writeSessionFile(t, session, "/tmp/script.sed", []byte("1,3p\n"))
	writeSessionFile(t, session, "/tmp/join-left.txt", []byte("alpha left-alpha\nbeta left-beta\n"))
	writeSessionFile(t, session, "/tmp/join-right.txt", []byte("alpha right-alpha\nbeta right-beta\n"))
	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/dir/nested", 0o755); err != nil {
		t.Fatalf("MkdirAll(/tmp/dir/nested) error = %v", err)
	}
	writeSessionFile(t, session, "/tmp/dir/a.txt", []byte("alpha\nbeta\n"))
	writeSessionFile(t, session, "/tmp/dir/nested/b.txt", []byte("beta\ngamma\n"))
	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/empty", 0o755); err != nil {
		t.Fatalf("MkdirAll(/tmp/empty) error = %v", err)
	}
	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/empty-rmdir", 0o755); err != nil {
		t.Fatalf("MkdirAll(/tmp/empty-rmdir) error = %v", err)
	}
	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/remove-dir/sub", 0o755); err != nil {
		t.Fatalf("MkdirAll(/tmp/remove-dir/sub) error = %v", err)
	}
	if err := session.FileSystem().Symlink(context.Background(), "/tmp/text.txt", "/tmp/link.txt"); err != nil && !strings.Contains(err.Error(), "exists") {
		t.Fatalf("Symlink() error = %v", err)
	}

	value := sanitizeFuzzToken(string(raw))
	if value == "value" {
		value = "alpha"
	}
	pattern := sanitizeFuzzPathComponent(strings.ToLower(value))
	if pattern == "" || pattern == "item" {
		pattern = "alpha"
	}

	values := map[string]string{
		"{token.value}":      value,
		"{token.pattern}":    pattern,
		"{pattern.glob}":     "*.txt",
		"{path.text}":        "/tmp/text.txt",
		"{path.alttext}":     "/tmp/alt.txt",
		"{path.sorted}":      "/tmp/sorted.txt",
		"{path.sorted2}":     "/tmp/sorted2.txt",
		"{path.csv}":         "/tmp/data.csv",
		"{path.json}":        "/tmp/data.json",
		"{path.yaml}":        "/tmp/data.yaml",
		"{path.rawjson}":     "/tmp/raw.json",
		"{path.base64}":      "/tmp/base64.txt",
		"{path.gzip}":        "/tmp/text.txt.gz",
		"{path.dir}":         "/tmp/dir",
		"{path.emptydir}":    "/tmp/empty",
		"{path.archive}":     "/tmp/archive.tar",
		"{path.tarfixture}":  "/tmp/archive-fixture.tar",
		"{path.out}":         "/tmp/out.txt",
		"{path.copy}":        "/tmp/copy.txt",
		"{path.moved}":       "/tmp/moved.txt",
		"{path.link}":        "/tmp/link.txt",
		"{path.touch}":       "/tmp/touch.txt",
		"{path.mkdir}":       "/tmp/new-dir/sub",
		"{path.remove}":      "/tmp/remove.txt",
		"{path.rmdir}":       "/tmp/empty-rmdir",
		"{path.splitprefix}": "/tmp/split-",
		"{path.joinleft}":    "/tmp/join-left.txt",
		"{path.joinright}":   "/tmp/join-right.txt",
		"{path.script}":      "/tmp/script.sh",
		"{path.sedscript}":   "/tmp/script.sed",
		"{program.awk}":      "{print NF}",
		"{program.sed}":      "1,3p",
		"{script.echo}":      "echo generated-subshell",
		"{date.fixed}":       "2024-01-02T03:04:05",
		"{date.format}":      "+%F",
		"{duration.short}":   "0.001",
		"{duration.timeout}": "0.01",
		"{jq.filter}":        ".value",
		"{jq.build}":         "{value:$value}",
		"{yq.filter}":        ".value",
		"{yq.build}":         ".value = \"generated\"",
	}

	pairs := make([]string, 0, len(values)*2)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		pairs = append(pairs, key, values[key])
	}

	return fuzzFixtures{
		values:   values,
		replacer: strings.NewReplacer(pairs...),
	}
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := []string{in[0]}
	for _, item := range in[1:] {
		if item == out[len(out)-1] {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (f fuzzFixtures) render(text string) string {
	return f.replacer.Replace(text)
}

func renderCommand(spec fuzzCommandMetadata, variant fuzzCommandVariant, fixtures fuzzFixtures) string {
	parts := []string{spec.Name}
	for _, token := range variant.Tokens {
		parts = append(parts, shellQuote(fixtures.render(token)))
	}
	command := strings.Join(parts, " ")
	if spec.Name == "[" {
		command += " ]"
	}
	if variant.StdinHint == "" {
		return command
	}
	return renderStdinSource(fixtures, variant.StdinHint) + " | " + command
}

func renderStdinSource(fixtures fuzzFixtures, hint string) string {
	switch hint {
	case "text":
		return "cat " + shellQuote(fixtures.values["{path.text}"])
	case "script":
		return "cat " + shellQuote(fixtures.values["{path.script}"])
	case "base64":
		return "cat " + shellQuote(fixtures.values["{path.base64}"])
	default:
		return "printf ''"
	}
}

func chooseMetadataCommand(t *testing.T, cursor *fuzzCursor, specs []fuzzCommandMetadata, category string) (fuzzCommandMetadata, fuzzCommandVariant) {
	t.Helper()

	filtered := make([]fuzzCommandMetadata, 0, len(specs))
	for _, spec := range specs {
		if category == "" || spec.Category == category {
			filtered = append(filtered, spec)
		}
	}
	if len(filtered) == 0 {
		t.Fatalf("no fuzz metadata for category %q", category)
	}
	spec := filtered[cursor.Intn(len(filtered))]
	variant := spec.Variants[cursor.Intn(len(spec.Variants))]
	generatedFuzzCoverage.Record(spec, variant)
	return spec, variant
}

func metadataHit(t *testing.T, specs []fuzzCommandMetadata, name string, flags ...string) (fuzzCommandMetadata, fuzzCommandVariant) {
	t.Helper()

	for _, spec := range specs {
		if spec.Name != name {
			continue
		}
		for _, variant := range spec.Variants {
			if sameFlags(variant.Tokens, flags) {
				return spec, variant
			}
		}
		return spec, spec.Variants[0]
	}
	t.Fatalf("missing metadata for %q", name)
	return fuzzCommandMetadata{}, fuzzCommandVariant{}
}

func sameFlags(tokens, flags []string) bool {
	want := append([]string(nil), flags...)
	got := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if strings.HasPrefix(token, "-") {
			got = append(got, token)
		}
	}
	sort.Strings(want)
	sort.Strings(got)
	return slices.Equal(got, want)
}

func generateFlagDrivenScript(t *testing.T, cursor *fuzzCursor, specs []fuzzCommandMetadata, fixtures fuzzFixtures) string {
	t.Helper()

	spec, variant := chooseMetadataCommand(t, cursor, specs, "")
	return renderCommand(spec, variant, fixtures) + "\n"
}

func generatePipelineScript(t *testing.T, cursor *fuzzCursor, specs []fuzzCommandMetadata, fixtures fuzzFixtures) string {
	t.Helper()

	type pipelineCase struct {
		template string
		hits     []struct {
			name  string
			flags []string
		}
	}
	pipelines := []pipelineCase{
		{
			template: "cat {path.text} | sort | uniq || true\n",
			hits: []struct {
				name  string
				flags []string
			}{{name: "sort"}, {name: "uniq"}},
		},
		{
			template: "cat {path.text} | tr a-z A-Z | head -n 2 || true\n",
			hits: []struct {
				name  string
				flags []string
			}{{name: "tr"}, {name: "head", flags: []string{"-n"}}},
		},
		{
			template: "cat {path.text} | grep -n {token.pattern} | wc -l || true\n",
			hits: []struct {
				name  string
				flags []string
			}{{name: "grep", flags: []string{"-n"}}, {name: "wc", flags: []string{"-l"}}},
		},
		{
			template: "cat {path.json} | jq -r {jq.filter} | sed -n {program.sed} || true\n",
			hits: []struct {
				name  string
				flags []string
			}{{name: "jq", flags: []string{"-r"}}, {name: "sed", flags: []string{"-n"}}},
		},
		{
			template: "cat {path.yaml} | yq {yq.filter} | sed -n {program.sed} || true\n",
			hits: []struct {
				name  string
				flags []string
			}{{name: "yq"}, {name: "sed", flags: []string{"-n"}}},
		},
	}

	chosen := pipelines[cursor.Intn(len(pipelines))]
	for _, hit := range chosen.hits {
		spec, variant := metadataHit(t, specs, hit.name, hit.flags...)
		generatedFuzzCoverage.Record(spec, variant)
	}
	return fixtures.render(chosen.template)
}

func generateShellSyntaxScript(t *testing.T, cursor *fuzzCursor, specs []fuzzCommandMetadata, fixtures fuzzFixtures) string {
	t.Helper()

	firstSpec, firstVariant := chooseMetadataCommand(t, cursor, specs, "")
	secondSpec, secondVariant := chooseMetadataCommand(t, cursor, specs, "")
	first := renderCommand(firstSpec, firstVariant, fixtures)
	second := renderCommand(secondSpec, secondVariant, fixtures)

	switch cursor.Intn(4) {
	case 0:
		return fmt.Sprintf("if true; then %s; else %s; fi\n", first, second)
	case 1:
		return fmt.Sprintf("out=$(%s || true)\nprintf '%%s\\n' \"$out\"\n", first)
	case 2:
		return fmt.Sprintf("(%s)\n%s\n", first, second)
	default:
		return fmt.Sprintf("for x in 1 2; do %s; done\n", first)
	}
}

func seedBytes() [][]byte {
	binarySeed := []byte{0x00, 0x01, 0x02, 0xff}
	return [][]byte{
		[]byte("alpha"),
		[]byte("beta,gamma"),
		binarySeed,
	}
}

func FuzzGeneratedPrograms(f *testing.F) {
	rt := newFuzzRuntime(f)
	specs := mustFuzzCommandMetadata(f)
	for _, seed := range seedBytes() {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		session := newFuzzSession(t, rt)
		cursor := newFuzzCursor(raw)
		fixtures := prepareFuzzFixtures(t, session, raw)

		var script string
		switch cursor.Intn(3) {
		case 0:
			script = generateFlagDrivenScript(t, cursor, specs, fixtures)
		case 1:
			script = generatePipelineScript(t, cursor, specs, fixtures)
		default:
			script = generateShellSyntaxScript(t, cursor, specs, fixtures)
		}

		result, err := runFuzzSessionScript(t, session, []byte(script))
		assertSecureFuzzOutcome(t, []byte(script), result, err)
	})
}
