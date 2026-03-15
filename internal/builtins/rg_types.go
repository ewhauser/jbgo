package builtins

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

type rgFileType struct {
	extensions []string
	globs      []string
}

type rgTypeRegistry struct {
	types map[string]rgFileType
}

func newRGTypeRegistry() *rgTypeRegistry {
	types := make(map[string]rgFileType, len(rgDefaultFileTypes))
	for name, fileType := range rgDefaultFileTypes {
		types[name] = rgFileType{
			extensions: append([]string(nil), fileType.extensions...),
			globs:      append([]string(nil), fileType.globs...),
		}
	}
	return &rgTypeRegistry{types: types}
}

func (r *rgTypeRegistry) AddType(spec string) {
	name, pattern, ok := strings.Cut(spec, ":")
	if !ok || name == "" || pattern == "" {
		return
	}
	current := r.types[name]
	if otherName, ok := strings.CutPrefix(pattern, "include:"); ok {
		other := r.types[otherName]
		current.extensions = appendUniqueStrings(current.extensions, other.extensions...)
		current.globs = appendUniqueStrings(current.globs, other.globs...)
		r.types[name] = current
		return
	}
	if strings.HasPrefix(pattern, "*.") && !strings.Contains(pattern[2:], "*") {
		current.extensions = appendUniqueStrings(current.extensions, pattern[1:])
	} else {
		current.globs = appendUniqueStrings(current.globs, pattern)
	}
	r.types[name] = current
}

func (r *rgTypeRegistry) ClearType(name string) {
	if _, ok := r.types[name]; !ok {
		return
	}
	r.types[name] = rgFileType{}
}

func (r *rgTypeRegistry) MatchesType(filename string, typeNames []string) bool {
	lowerName := strings.ToLower(filename)
	for _, name := range typeNames {
		if name == "all" {
			if r.matchesAnyType(filename) {
				return true
			}
			continue
		}
		fileType, ok := r.types[name]
		if !ok {
			continue
		}
		for _, ext := range fileType.extensions {
			if strings.HasSuffix(lowerName, ext) {
				return true
			}
		}
		for _, glob := range fileType.globs {
			if matched, _ := rgMatchGlob(filename, glob, true); matched {
				return true
			}
		}
	}
	return false
}

func (r *rgTypeRegistry) matchesAnyType(filename string) bool {
	for name := range r.types {
		if r.MatchesType(filename, []string{name}) {
			return true
		}
	}
	return false
}

func formatRGTypeList() string {
	names := make([]string, 0, len(rgDefaultFileTypes))
	for name := range rgDefaultFileTypes {
		names = append(names, name)
	}
	sort.Strings(names)

	lines := make([]string, 0, len(names))
	for _, name := range names {
		fileType := rgDefaultFileTypes[name]
		patterns := make([]string, 0, len(fileType.extensions)+len(fileType.globs))
		for _, ext := range fileType.extensions {
			patterns = append(patterns, "*"+ext)
		}
		patterns = append(patterns, fileType.globs...)
		lines = append(lines, fmt.Sprintf("%s: %s", name, strings.Join(patterns, ", ")))
	}
	return strings.Join(lines, "\n") + "\n"
}

func appendUniqueStrings(dst []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(dst, value) {
			dst = append(dst, value)
		}
	}
	return dst
}

var rgDefaultFileTypes = map[string]rgFileType{
	"bat":      {extensions: []string{".bat", ".cmd"}},
	"c":        {extensions: []string{".c", ".h"}},
	"clojure":  {extensions: []string{".clj", ".cljc", ".cljs", ".edn"}},
	"cpp":      {extensions: []string{".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".h"}},
	"css":      {extensions: []string{".css", ".scss", ".sass", ".less"}},
	"csv":      {extensions: []string{".csv", ".tsv"}},
	"docker":   {globs: []string{"Dockerfile", "Dockerfile.*", "*.dockerfile"}},
	"go":       {extensions: []string{".go"}},
	"graphql":  {extensions: []string{".graphql", ".gql"}},
	"html":     {extensions: []string{".html", ".htm", ".xhtml"}},
	"ini":      {extensions: []string{".ini", ".cfg", ".conf"}},
	"java":     {extensions: []string{".java"}},
	"js":       {extensions: []string{".js", ".mjs", ".cjs", ".jsx"}},
	"json":     {extensions: []string{".json", ".jsonc", ".json5"}},
	"kotlin":   {extensions: []string{".kt", ".kts"}},
	"lua":      {extensions: []string{".lua"}},
	"make":     {extensions: []string{".mk", ".mak"}, globs: []string{"Makefile", "GNUmakefile", "makefile"}},
	"markdown": {extensions: []string{".md", ".mdx", ".markdown", ".mdown", ".mkd"}},
	"md":       {extensions: []string{".md", ".mdx", ".markdown", ".mdown", ".mkd"}},
	"perl":     {extensions: []string{".pl", ".pm", ".pod", ".t"}},
	"php":      {extensions: []string{".php", ".phtml", ".php3", ".php4", ".php5"}},
	"proto":    {extensions: []string{".proto"}},
	"ps":       {extensions: []string{".ps1", ".psm1", ".psd1"}},
	"py":       {extensions: []string{".py", ".pyi", ".pyw"}},
	"rb":       {extensions: []string{".rb", ".rake", ".gemspec"}, globs: []string{"Rakefile", "Gemfile"}},
	"rst":      {extensions: []string{".rst"}},
	"rust":     {extensions: []string{".rs"}},
	"scala":    {extensions: []string{".scala", ".sc"}},
	"sh":       {extensions: []string{".sh", ".bash", ".zsh", ".fish"}, globs: []string{".bashrc", ".zshrc", ".profile"}},
	"sql":      {extensions: []string{".sql"}},
	"tex":      {extensions: []string{".tex", ".ltx", ".sty", ".cls"}},
	"tf":       {extensions: []string{".tf", ".tfvars"}},
	"toml":     {extensions: []string{".toml"}, globs: []string{"Cargo.toml", "pyproject.toml"}},
	"ts":       {extensions: []string{".ts", ".tsx", ".mts", ".cts"}},
	"txt":      {extensions: []string{".txt", ".text"}},
	"xml":      {extensions: []string{".xml", ".xsl", ".xslt"}},
	"yaml":     {extensions: []string{".yaml", ".yml"}},
	"zig":      {extensions: []string{".zig"}},
}
