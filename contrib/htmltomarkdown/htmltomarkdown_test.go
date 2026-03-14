package htmltomarkdown

import (
	"slices"
	"strings"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func TestRegisterNilRegistry(t *testing.T) {
	t.Parallel()

	if err := Register(nil); err != nil {
		t.Fatalf("Register(nil) error = %v", err)
	}
}

func TestRegisterAddsCommand(t *testing.T) {
	t.Parallel()

	registry := newHTMLToMarkdownRegistry(t)
	if !slices.Contains(registry.Names(), htmlToMarkdownName) {
		t.Fatalf("Names() missing %q: %v", htmlToMarkdownName, registry.Names())
	}
}

func TestDefaultRegistryDoesNotIncludeCommand(t *testing.T) {
	t.Parallel()

	if slices.Contains(gbruntime.DefaultRegistry().Names(), htmlToMarkdownName) {
		t.Fatalf("DefaultRegistry() unexpectedly contains %q", htmlToMarkdownName)
	}
}

func TestHTMLToMarkdownConvertsCommonElements(t *testing.T) {
	t.Parallel()

	result := mustExecHTMLToMarkdown(t, "printf '<h1>Hello World</h1>' | html-to-markdown\n"+
		"printf '<a href=\"https://example.com\">Click here</a>' | html-to-markdown\n"+
		"printf '<strong>bold</strong> and <em>italic</em>' | html-to-markdown\n"+
		"printf 'Use <code>npm install</code> to install' | html-to-markdown\n"+
		"printf '<img src=\"photo.jpg\" alt=\"A photo\">' | html-to-markdown\n"+
		"printf '<blockquote>A wise quote</blockquote>' | html-to-markdown\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "# Hello World\n[Click here](https://example.com)\n**bold** and _italic_\nUse `npm install` to install\n![A photo](photo.jpg)\n> A wise quote\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHTMLToMarkdownConvertsCodeBlocks(t *testing.T) {
	t.Parallel()

	result := mustExecHTMLToMarkdown(t, "printf '<pre><code>const x = 1;</code></pre>' | html-to-markdown\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "```\nconst x = 1;\n```\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHTMLToMarkdownReadsFromFile(t *testing.T) {
	t.Parallel()

	session := newHTMLToMarkdownSession(t)
	writeHTMLToMarkdownSessionFile(t, session, "/tmp/page.html", []byte("<h2>From File</h2>"))

	result := mustExecHTMLToMarkdownSession(t, session, "html-to-markdown /tmp/page.html\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "## From File\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHTMLToMarkdownReportsMissingFile(t *testing.T) {
	t.Parallel()

	result := mustExecHTMLToMarkdown(t, "html-to-markdown /nonexistent.html\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stderr, "html-to-markdown: /nonexistent.html: No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestHTMLToMarkdownReturnsNoOutputForWhitespaceInput(t *testing.T) {
	t.Parallel()

	for _, script := range []string{
		"printf '' | html-to-markdown\n",
		"printf '   \n\t' | html-to-markdown\n",
	} {
		result := mustExecHTMLToMarkdown(t, script)
		if result.ExitCode != 0 {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
		}
		if result.Stdout != "" {
			t.Fatalf("Stdout = %q, want empty", result.Stdout)
		}
	}
}

func TestHTMLToMarkdownSupportsFormattingFlags(t *testing.T) {
	t.Parallel()

	result := mustExecHTMLToMarkdown(t, "printf '<h1>Title</h1>' | html-to-markdown --heading-style=setext\n"+
		"printf '<ul><li>Item</li></ul>' | html-to-markdown -b '*'\n"+
		"printf '<pre><code>code</code></pre>' | html-to-markdown -c '~~~'\n"+
		"printf '<hr>' | html-to-markdown -r '***'\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "Title\n=====\n* Item\n~~~\ncode\n~~~\n***\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHTMLToMarkdownStripsRemovedTags(t *testing.T) {
	t.Parallel()

	result := mustExecHTMLToMarkdown(t, "printf '<p>Hello</p><script>alert(1);</script><style>.x { color: red; }</style><footer>Footer</footer><p>World</p>' | html-to-markdown\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "Hello\n\nWorld\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHTMLToMarkdownHelp(t *testing.T) {
	t.Parallel()

	result := mustExecHTMLToMarkdown(t, "html-to-markdown --help\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, fragment := range []string{"Usage: html-to-markdown", "--bullet=CHAR", "--heading-style=STYLE", "Examples:"} {
		if !strings.Contains(result.Stdout, fragment) {
			t.Fatalf("Stdout = %q, want fragment %q", result.Stdout, fragment)
		}
	}
}

func TestHTMLToMarkdownUnknownOption(t *testing.T) {
	t.Parallel()

	result := mustExecHTMLToMarkdown(t, "html-to-markdown --invalid\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	want := "html-to-markdown: unrecognized option '--invalid'\nTry 'html-to-markdown --help' for more information.\n"
	if got := result.Stderr; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestHTMLToMarkdownVersionIsUnsupported(t *testing.T) {
	t.Parallel()

	result := mustExecHTMLToMarkdown(t, "html-to-markdown --version\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	want := "html-to-markdown: unrecognized option '--version'\nTry 'html-to-markdown --help' for more information.\n"
	if got := result.Stderr; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestHTMLToMarkdownMissingOptionValuesUseDefaults(t *testing.T) {
	t.Parallel()

	result := mustExecHTMLToMarkdown(t, "printf '<ul><li>Item</li></ul>' | html-to-markdown -b\n"+
		"printf '<pre><code>code</code></pre>' | html-to-markdown -c\n"+
		"printf '<hr>' | html-to-markdown -r\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "- Item\n```\ncode\n```\n---\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHTMLToMarkdownIgnoresInvalidHeadingStyle(t *testing.T) {
	t.Parallel()

	result := mustExecHTMLToMarkdown(t, "printf '<h1>Title</h1>' | html-to-markdown --heading-style=invalid\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "# Title\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestHTMLToMarkdownIgnoresExtraOperands(t *testing.T) {
	t.Parallel()

	session := newHTMLToMarkdownSession(t)
	writeHTMLToMarkdownSessionFile(t, session, "/tmp/left.html", []byte("<h2>Left</h2>"))
	writeHTMLToMarkdownSessionFile(t, session, "/tmp/right.html", []byte("<h2>Right</h2>"))

	result := mustExecHTMLToMarkdownSession(t, session, "html-to-markdown /tmp/left.html /tmp/right.html\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "## Left\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
