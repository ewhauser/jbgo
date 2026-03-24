package builtins_test

import (
	"bytes"
	"context"
	stdfs "io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ewhauser/gbash/policy"
)

func TestLSSupportsHelpDirectoryAndHumanReadableFlags(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/home/agent/local.txt", []byte("should not appear\n"))
	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/lsdir/sub", 0o755); err != nil {
		t.Fatalf("MkdirAll(lsdir/sub) error = %v", err)
	}
	writeSessionFile(t, session, "/tmp/lsdir/big.bin", bytes.Repeat([]byte("x"), 2048))

	result := mustExecSession(t, session,
		"ls --help\n"+
			"echo ---\n"+
			"ls -ld /tmp/lsdir\n"+
			"echo ---\n"+
			"ls -h -l /tmp/lsdir/big.bin\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 3 {
		t.Fatalf("Stdout blocks = %q, want 3 blocks", result.Stdout)
	}

	helpBlock := parts[0]
	if !strings.Contains(helpBlock, "Usage:\n  ls [OPTION]... [FILE]...") {
		t.Fatalf("help block = %q, want usage text", helpBlock)
	}
	if strings.Contains(helpBlock, "local.txt") {
		t.Fatalf("help block = %q, want no directory listing after help", helpBlock)
	}

	if got := strings.TrimSpace(parts[1]); !strings.HasSuffix(got, "/tmp/lsdir") {
		t.Fatalf("directory listing = %q, want directory path without implicit suffix", got)
	}

	humanLine := strings.TrimSpace(parts[2])
	if !strings.Contains(humanLine, "2.0K") {
		t.Fatalf("human-readable listing = %q, want 2.0K size", humanLine)
	}
	if !strings.HasSuffix(humanLine, "/tmp/lsdir/big.bin") {
		t.Fatalf("human-readable listing = %q, want big.bin path", humanLine)
	}
}

func TestLSVersionLongAliasDiredShortAndTabsizeCompat(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-compat/file.txt", []byte("x"))
	writeSessionFile(t, session, "/tmp/ls-tab-compat/a\t", []byte("a"))
	writeSessionFile(t, session, "/tmp/ls-tab-compat/b", []byte("b"))

	result := mustExecSession(t, session,
		"ls --version\n"+
			"echo ---\n"+
			"ls --help\n"+
			"echo ---\n"+
			"ls --long /tmp/ls-compat/file.txt\n"+
			"echo ---\n"+
			"ls -D /tmp/ls-compat\n"+
			"echo ---\n"+
			"cd /tmp/ls-tab-compat\n"+
			"ls -w0 -x -T1 .\n"+
			"echo ---\n"+
			"ls -w0 -x --tab-size=1 .\n"+
			"echo ---\n"+
			"TABSIZE=1 ls -w0 -x .\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 7 {
		t.Fatalf("Stdout blocks = %q, want 7 blocks", result.Stdout)
	}

	if got, want := strings.TrimSpace(parts[0]), "ls (gbash) dev"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
	if !strings.Contains(parts[1], "--version") || !strings.Contains(parts[1], "-l, --long") {
		t.Fatalf("help output = %q, want version and long alias", parts[1])
	}
	if !strings.Contains(parts[2], "-rw") || !strings.Contains(parts[2], "/tmp/ls-compat/file.txt") {
		t.Fatalf("--long output = %q, want long-format listing", parts[2])
	}
	if !strings.Contains(parts[3], "//DIRED//") || !strings.Contains(parts[3], "file.txt") {
		t.Fatalf("-D output = %q, want dired metadata", parts[3])
	}
	if parts[4] != parts[5] {
		t.Fatalf("--tab-size output = %q, want %q", parts[5], parts[4])
	}
	if parts[4] != parts[6] {
		t.Fatalf("TABSIZE output = %q, want %q", parts[6], parts[4])
	}
}

func TestLSShortHRemainsHumanReadableAfterSpecMigration(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-short-h/big.bin", bytes.Repeat([]byte("x"), 2048))

	result := mustExecSession(t, session, "ls -h -l /tmp/ls-short-h/big.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if strings.Contains(result.Stdout, "Usage:\n  ls [OPTION]... [FILE]...") {
		t.Fatalf("Stdout = %q, want listing rather than help text", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "2.0K") {
		t.Fatalf("Stdout = %q, want human-readable size", result.Stdout)
	}
}

func TestLSSupportsHiddenAndClassifyFlags(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/classify/.hidden", []byte("secret\n"))
	writeSessionFile(t, session, "/tmp/classify/plain.txt", []byte("plain\n"))
	writeSessionFile(t, session, "/tmp/classify/run.sh", []byte("#!/bin/sh\n"))
	writeSessionFile(t, session, "/tmp/classify/sub/nested.txt", []byte("nested\n"))
	if err := session.FileSystem().Chmod(context.Background(), "/tmp/classify/run.sh", 0o755); err != nil {
		t.Fatalf("Chmod(run.sh) error = %v", err)
	}

	result := mustExecSession(t, session,
		"ls /tmp/classify\n"+
			"echo ---\n"+
			"ls -a /tmp/classify\n"+
			"echo ---\n"+
			"ls -A /tmp/classify\n"+
			"echo ---\n"+
			"ls -F /tmp/classify\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 4 {
		t.Fatalf("Stdout blocks = %q, want 4 blocks", result.Stdout)
	}

	defaultLines := splitTrimmedLines(parts[0])
	if slices.Contains(defaultLines, ".hidden") || slices.Contains(defaultLines, ".") || slices.Contains(defaultLines, "..") {
		t.Fatalf("default ls lines = %v, want hidden entries omitted", defaultLines)
	}

	allLines := splitTrimmedLines(parts[1])
	for _, want := range []string{".", "..", ".hidden"} {
		if !slices.Contains(allLines, want) {
			t.Fatalf("ls -a lines = %v, want %q", allLines, want)
		}
	}

	almostAllLines := splitTrimmedLines(parts[2])
	if !slices.Contains(almostAllLines, ".hidden") {
		t.Fatalf("ls -A lines = %v, want hidden file", almostAllLines)
	}
	if slices.Contains(almostAllLines, ".") || slices.Contains(almostAllLines, "..") {
		t.Fatalf("ls -A lines = %v, want no dot entries", almostAllLines)
	}

	classifiedLines := splitTrimmedLines(parts[3])
	for _, want := range []string{"run.sh*", "sub/"} {
		if !slices.Contains(classifiedLines, want) {
			t.Fatalf("ls -F lines = %v, want %q", classifiedLines, want)
		}
	}
}

func TestLSSupportsSortingAndRecursiveFlags(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/sortsize/a.txt", []byte("a"))
	writeSessionFile(t, session, "/tmp/sortsize/b.txt", bytes.Repeat([]byte("b"), 2048))
	writeSessionFile(t, session, "/tmp/sortsize/c.txt", []byte("1234567890"))
	writeSessionFile(t, session, "/tmp/sorttime/old.txt", []byte("old"))
	writeSessionFile(t, session, "/tmp/sorttime/new.txt", []byte("new"))
	writeSessionFile(t, session, "/tmp/recurse/root.txt", []byte("root"))
	writeSessionFile(t, session, "/tmp/recurse/sub/nested.txt", []byte("nested"))

	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	for _, item := range []struct {
		path string
		when time.Time
	}{
		{"/tmp/sorttime/old.txt", old},
		{"/tmp/sorttime/new.txt", now},
	} {
		if err := session.FileSystem().Chtimes(context.Background(), item.path, item.when, item.when); err != nil {
			t.Fatalf("Chtimes(%q) error = %v", item.path, err)
		}
	}

	result := mustExecSession(t, session,
		"ls -S /tmp/sortsize\n"+
			"echo ---\n"+
			"ls -rt /tmp/sorttime\n"+
			"echo ---\n"+
			"ls -R /tmp/recurse\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 3 {
		t.Fatalf("Stdout blocks = %q, want 3 blocks", result.Stdout)
	}

	if got, want := splitTrimmedLines(parts[0]), []string{"b.txt", "c.txt", "a.txt"}; !slices.Equal(got, want) {
		t.Fatalf("ls -S lines = %v, want %v", got, want)
	}

	if got, want := splitTrimmedLines(parts[1]), []string{"old.txt", "new.txt"}; !slices.Equal(got, want) {
		t.Fatalf("ls -rt lines = %v, want %v", got, want)
	}

	recursive := parts[2]
	for _, want := range []string{"/tmp/recurse:\n", "/tmp/recurse/sub:\n", "root.txt\n", "nested.txt\n"} {
		if !strings.Contains(recursive, want) {
			t.Fatalf("recursive output = %q, want %q", recursive, want)
		}
	}
}

func TestLSReturnsMissingPathExitCode(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "ls /tmp/missing\n")
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "ls: /tmp/missing: No such file or directory") {
		t.Fatalf("Stderr = %q, want missing-path error", result.Stderr)
	}
}

func TestLSAlmostAllOverridesAllAndRecursiveDotKeepsPrefixedHeaders(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/ls-aA/dir/subdir", 0o755); err != nil {
		t.Fatalf("MkdirAll(ls-aA) error = %v", err)
	}
	writeSessionFile(t, session, "/tmp/ls-aA/dir/subdir/file2", []byte("x"))

	result := mustExecSession(t, session,
		"ls -aA /tmp/ls-aA/dir\n"+
			"echo ---\n"+
			"cd /tmp/ls-aA\n"+
			"ls -R1 .\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}
	if got := strings.TrimSpace(parts[0]); got != "subdir" {
		t.Fatalf("ls -aA output = %q, want only subdir", got)
	}
	for _, want := range []string{"./dir:\n", "./dir/subdir:\n"} {
		if !strings.Contains(parts[1], want) {
			t.Fatalf("recursive output = %q, want %q", parts[1], want)
		}
	}
}

func TestLSLongFormatListsDanglingSymlinkWithoutDereferencing(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	result := mustExecSession(t, session, "cd /home/agent\nln -s no-such dangle\nls -ldo dangle\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
	if !strings.Contains(result.Stdout, "dangle -> no-such") {
		t.Fatalf("Stdout = %q, want dangling symlink listing", result.Stdout)
	}
}

func TestLSDereferenceKeepsDanglingDirectoryEntriesForInodeAndBlockPrefixes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/ls-dangle-dir/d", 0o755); err != nil {
		t.Fatalf("MkdirAll(d) error = %v", err)
	}
	if err := session.FileSystem().Symlink(context.Background(), "no-such", "/tmp/ls-dangle-dir/d/dangle"); err != nil {
		t.Fatalf("Symlink(dangle) error = %v", err)
	}

	result := mustExecSession(t, session,
		"cd /tmp/ls-dangle-dir\n"+
			"ls -Li d\n"+
			"echo ---\n"+
			"ls -Ls d\n",
	)
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}
	if got, want := parts[0], "? dangle\n"; got != want {
		t.Fatalf("ls -Li output = %q, want %q", got, want)
	}
	if got, want := parts[1], "total 0\n? dangle\n"; got != want {
		t.Fatalf("ls -Ls output = %q, want %q", got, want)
	}
}

func TestDirUsesDirUsageAndNonLongDefaultOutput(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/dir-view/alpha.txt", []byte("alpha\n"))
	writeSessionFile(t, session, "/tmp/dir-view/beta.txt", []byte("beta\n"))

	result := mustExecSession(t, session, "dir --help\necho ---\ndir /tmp/dir-view\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}
	helpBlock := parts[0]
	if !strings.Contains(helpBlock, "Usage:\n  dir [OPTION]... [FILE]...") {
		t.Fatalf("help block = %q, want dir usage", helpBlock)
	}
	if strings.Contains(helpBlock, "ls [OPTION]") {
		t.Fatalf("help block = %q, want no ls usage", helpBlock)
	}

	listing := strings.TrimSpace(parts[1])
	for _, want := range []string{"alpha.txt", "beta.txt"} {
		if !strings.Contains(listing, want) {
			t.Fatalf("dir output = %q, want %q", listing, want)
		}
	}
	if strings.Contains(listing, "-rw") {
		t.Fatalf("dir output = %q, want non-long default output", listing)
	}
}

func TestDirSupportsLongFormatViaLSFlags(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/dir-long/item.txt", []byte("payload\n"))

	result := mustExecSession(t, session, "dir -l /tmp/dir-long\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "item.txt") || !strings.Contains(result.Stdout, "-rw") {
		t.Fatalf("Stdout = %q, want long-format output", result.Stdout)
	}
}

func TestVdirUsesVdirUsageAndLongEscapedDefaultOutput(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/vdir-view/plain.txt", []byte("plain\n"))
	writeSessionFile(t, session, "/tmp/vdir-view/tab\tname.txt", []byte("tabbed\n"))

	result := mustExecSession(t, session, "vdir --help\necho ---\nvdir /tmp/vdir-view\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}

	helpBlock := parts[0]
	if !strings.Contains(helpBlock, "Usage:\n  vdir [OPTION]... [FILE]...") {
		t.Fatalf("help block = %q, want vdir usage", helpBlock)
	}
	if strings.Contains(helpBlock, "ls [OPTION]") {
		t.Fatalf("help block = %q, want no ls usage", helpBlock)
	}

	listing := parts[1]
	if !strings.Contains(listing, "plain.txt") || !strings.Contains(listing, "-rw") {
		t.Fatalf("listing = %q, want long-format output", listing)
	}
	if !strings.Contains(listing, "tab\\tname.txt") {
		t.Fatalf("listing = %q, want C-style escaping", listing)
	}
}

func TestVdirSupportsColumnOutputViaLSFlags(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/vdir-columns/alpha.txt", []byte("alpha\n"))
	writeSessionFile(t, session, "/tmp/vdir-columns/beta.txt", []byte("beta\n"))

	result := mustExecSession(t, session, "vdir -C /tmp/vdir-columns\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	if !strings.Contains(result.Stdout, "alpha.txt") || !strings.Contains(result.Stdout, "beta.txt") {
		t.Fatalf("Stdout = %q, want both entries", result.Stdout)
	}
	if strings.Contains(result.Stdout, "-rw") {
		t.Fatalf("Stdout = %q, want non-long output", result.Stdout)
	}
}

func TestVdirBatchesCommandLineFilesForExplicitGridOutput(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/vdir-grid/a", []byte("a"))
	writeSessionFile(t, session, "/tmp/vdir-grid/b", []byte("b"))

	result := mustExecSession(t, session, "cd /tmp/vdir-grid\nvdir -w0 -x -T1 a b\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a  b\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestVdirSupportsDiredAndRecursiveHeaders(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/vdir-dired/dir/subdir/file.txt", []byte("x"))

	result := mustExecSession(t, session,
		"vdir --dired /tmp/vdir-dired/dir\n"+
			"echo ---\n"+
			"cd /tmp/vdir-dired\n"+
			"vdir -R .\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}
	if !strings.Contains(parts[0], "//DIRED//") || !strings.Contains(parts[0], "//DIRED-OPTIONS//") {
		t.Fatalf("dired output = %q, want dired footer", parts[0])
	}
	if !strings.Contains(parts[1], "./dir/subdir:") {
		t.Fatalf("recursive output = %q, want nested header with ./ prefix", parts[1])
	}
}

func TestVdirInvalidOptionUsesVdirPrefixAndExitCode(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "vdir -/\n")
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "vdir: invalid option -- '/'\nTry 'vdir --help' for more information.\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestLSColorFlagsAndLSColorsOverride(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-color/plain.txt", []byte("plain\n"))
	writeSessionFile(t, session, "/tmp/ls-color/run.sh", []byte("#!/bin/sh\n"))
	if err := session.FileSystem().Chmod(context.Background(), "/tmp/ls-color/run.sh", 0o755); err != nil {
		t.Fatalf("Chmod(run.sh) error = %v", err)
	}

	result := mustExecSession(t, session,
		"LS_COLORS='*.txt=33:ex=35' ls --color=always /tmp/ls-color\n"+
			"echo ---\n"+
			"ls --color=never /tmp/ls-color\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}

	colored := parts[0]
	for _, want := range []string{"\x1b[33mplain.txt\x1b[0m", "\x1b[35mrun.sh\x1b[0m"} {
		if !strings.Contains(colored, want) {
			t.Fatalf("colored output = %q, want %q", colored, want)
		}
	}

	plain := parts[1]
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("color=never output = %q, want no ANSI escapes", plain)
	}
}

func TestLSColorsRespectOrderedCaseSensitiveExtensions(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-color-ext/img1.jpg", []byte("x"))
	writeSessionFile(t, session, "/tmp/ls-color-ext/IMG2.JPG", []byte("x"))
	writeSessionFile(t, session, "/tmp/ls-color-ext/img3.JpG", []byte("x"))

	result := mustExecSession(t, session, "LS_COLORS='*.jpg=01;35:*.JPG=01;35;46' ls --color=always /tmp/ls-color-ext\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "\x1b[0m\x1b[01;35mimg1.jpg\x1b[0m") {
		t.Fatalf("Stdout = %q, want lowercase extension color", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "\x1b[0m\x1b[01;35;46mIMG2.JPG\x1b[0m") {
		t.Fatalf("Stdout = %q, want ordered uppercase extension override", result.Stdout)
	}
	if strings.Contains(result.Stdout, "\x1b[0m\x1b[01;35mimg3.JpG\x1b[0m") || strings.Contains(result.Stdout, "\x1b[0m\x1b[01;35;46mimg3.JpG\x1b[0m") {
		t.Fatalf("Stdout = %q, want mixed-case extension left uncolored", result.Stdout)
	}
}

func TestLSColorsUseWritableAndStickyDirectoryClasses(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-color-dir/default/file", []byte("x"))
	writeSessionFile(t, session, "/tmp/ls-color-dir/other-writable/file", []byte("x"))
	writeSessionFile(t, session, "/tmp/ls-color-dir/sticky/file", []byte("x"))
	if err := session.FileSystem().Chmod(context.Background(), "/tmp/ls-color-dir/other-writable", 0o777); err != nil {
		t.Fatalf("Chmod(other-writable) error = %v", err)
	}
	if err := session.FileSystem().Chmod(context.Background(), "/tmp/ls-color-dir/sticky", 0o1755); err != nil {
		t.Fatalf("Chmod(sticky) error = %v", err)
	}
	stickyInfo, err := session.FileSystem().Stat(context.Background(), "/tmp/ls-color-dir/sticky")
	if err != nil {
		t.Fatalf("Stat(sticky) error = %v", err)
	}

	result := mustExecSession(t, session, "LS_COLORS='di=01;34:ow=34;42:st=37;44' ls --color=always /tmp/ls-color-dir\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	wants := []string{
		"\x1b[0m\x1b[01;34mdefault\x1b[0m",
		"\x1b[0m\x1b[34;42mother-writable\x1b[0m",
	}
	if stickyInfo.Mode()&stdfs.ModeSticky != 0 {
		wants = append(wants, "\x1b[0m\x1b[37;44msticky\x1b[0m")
	}
	for _, want := range wants {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
		}
	}
}

func TestDirVersionAndColorSupport(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/dir-color/note.txt", []byte("note\n"))

	result := mustExecSession(t, session, "dir --version\necho ---\nLS_COLORS='*.txt=36' dir --color=always /tmp/dir-color\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}
	if got := strings.TrimSpace(parts[0]); got != "dir (gbash) dev" {
		t.Fatalf("version output = %q, want dir version banner", got)
	}
	if !strings.Contains(parts[1], "\x1b[36mnote.txt\x1b[0m") {
		t.Fatalf("dir color output = %q, want colored filename", parts[1])
	}
}

func TestLSFormatFilteringGroupingAndZeroModes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-modes/.hidden", []byte("hidden\n"))
	writeSessionFile(t, session, "/tmp/ls-modes/alpha1", []byte("a\n"))
	writeSessionFile(t, session, "/tmp/ls-modes/alpha10", []byte("a\n"))
	writeSessionFile(t, session, "/tmp/ls-modes/beta.tmp", []byte("tmp\n"))
	writeSessionFile(t, session, "/tmp/ls-modes/backup~", []byte("backup\n"))
	writeSessionFile(t, session, "/tmp/ls-modes/dir/file.txt", []byte("file\n"))

	result := mustExecSession(t, session,
		"ls --sort=version /tmp/ls-modes\n"+
			"echo ---\n"+
			"ls -B --ignore='*.tmp' /tmp/ls-modes\n"+
			"echo ---\n"+
			"ls --group-directories-first -C /tmp/ls-modes\n"+
			"echo ---\n"+
			"ls --zero -m /tmp/ls-modes\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 4 {
		t.Fatalf("Stdout blocks = %q, want 4 blocks", result.Stdout)
	}

	versionLines := splitTrimmedLines(parts[0])
	alpha1Index := slices.Index(versionLines, "alpha1")
	alpha10Index := slices.Index(versionLines, "alpha10")
	if alpha1Index < 0 || alpha10Index < 0 || alpha1Index > alpha10Index {
		t.Fatalf("version-sorted output = %v, want alpha1 before alpha10", versionLines)
	}

	filtered := splitTrimmedLines(parts[1])
	if slices.Contains(filtered, "backup~") || slices.Contains(filtered, "beta.tmp") {
		t.Fatalf("filtered output = %v, want backups and ignored pattern omitted", filtered)
	}

	grouped := strings.TrimSpace(parts[2])
	if !strings.HasPrefix(grouped, "dir") {
		t.Fatalf("group-directories-first output = %q, want dir listed first", grouped)
	}

	zeroBlock := parts[3]
	if !strings.Contains(zeroBlock, "\x00") {
		t.Fatalf("zero output = %q, want NUL separators", zeroBlock)
	}
	if strings.Contains(zeroBlock, ", ") {
		t.Fatalf("zero output = %q, want --zero to override comma formatting", zeroBlock)
	}
}

func TestLSQuotingModes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-quote/line\nbreak", []byte("x"))

	result := mustExecSession(t, session,
		"ls -b /tmp/ls-quote\n"+
			"echo ---\n"+
			"ls -Q /tmp/ls-quote\n"+
			"echo ---\n"+
			"ls --hide-control-chars /tmp/ls-quote\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 3 {
		t.Fatalf("Stdout blocks = %q, want 3 blocks", result.Stdout)
	}
	if !strings.Contains(parts[0], "line\\nbreak") {
		t.Fatalf("escape output = %q, want escaped newline", parts[0])
	}
	if !strings.Contains(parts[1], "\"line\\nbreak\"") {
		t.Fatalf("quote-name output = %q, want quoted filename", parts[1])
	}
	if !strings.Contains(parts[2], "line?break") {
		t.Fatalf("hide-control-chars output = %q, want ? replacement", parts[2])
	}
}

func TestLSLongFormatMetadataFlags(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-long/data.txt", bytes.Repeat([]byte("x"), 2048))

	result := mustExecSession(t, session,
		"ls -li /tmp/ls-long/data.txt\n"+
			"echo ---\n"+
			"ls -lsg /tmp/ls-long/data.txt\n"+
			"echo ---\n"+
			"ls -lo --author /tmp/ls-long/data.txt\n"+
			"echo ---\n"+
			"ls -ln /tmp/ls-long/data.txt\n"+
			"echo ---\n"+
			"ls -l --time-style=long-iso /tmp/ls-long/data.txt\n"+
			"echo ---\n"+
			"ls -l --full-time /tmp/ls-long/data.txt\n"+
			"echo ---\n"+
			"ls -l --block-size=1K /tmp/ls-long/data.txt\n"+
			"echo ---\n"+
			"ls -l --si /tmp/ls-long/data.txt\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 8 {
		t.Fatalf("Stdout blocks = %q, want 8 blocks", result.Stdout)
	}

	if !regexp.MustCompile(`^\d+ `).MatchString(strings.TrimSpace(parts[0])) {
		t.Fatalf("inode output = %q, want inode prefix", parts[0])
	}

	longNoOwner := strings.TrimSpace(parts[1])
	if strings.Contains(longNoOwner, " user ") {
		t.Fatalf("-g output = %q, want owner omitted", longNoOwner)
	}
	if !strings.Contains(longNoOwner, " "+defaultUser+" ") {
		t.Fatalf("-g output = %q, want group shown", longNoOwner)
	}

	longNoGroupWithAuthor := strings.TrimSpace(parts[2])
	longNoGroupWithAuthorFields := strings.Fields(longNoGroupWithAuthor)
	if len(longNoGroupWithAuthorFields) < 9 || longNoGroupWithAuthorFields[2] != defaultUser || longNoGroupWithAuthorFields[3] != defaultUser {
		t.Fatalf("-o --author output = %q, want owner and author shown without group", longNoGroupWithAuthor)
	}

	numericIDs := strings.TrimSpace(parts[3])
	if !regexp.MustCompile(`\s` + defaultUID + `\s` + defaultGID + `\s`).MatchString(numericIDs) {
		t.Fatalf("-n output = %q, want numeric owner/group ids", numericIDs)
	}

	if !regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}`).MatchString(strings.TrimSpace(parts[4])) {
		t.Fatalf("time-style=long-iso output = %q, want ISO timestamp", parts[4])
	}
	if !regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`).MatchString(strings.TrimSpace(parts[5])) {
		t.Fatalf("full-time output = %q, want full timestamp with seconds", parts[5])
	}
	if !regexp.MustCompile(`\s2\s`).MatchString(strings.TrimSpace(parts[6])) {
		t.Fatalf("block-size=1K output = %q, want 1K-scaled size", parts[6])
	}
	if !strings.Contains(parts[7], "2.0K") && !strings.Contains(parts[7], "2.1K") {
		t.Fatalf("--si output = %q, want SI human-readable size", parts[7])
	}
}

func TestLSHyperlinkMode(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-link/item.txt", []byte("x"))

	result := mustExecSession(t, session, "ls --hyperlink=always /tmp/ls-link\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "\x1b]8;;file:///tmp/ls-link/item.txt\x1b\\item.txt\x1b]8;;\x1b\\") {
		t.Fatalf("Stdout = %q, want OSC8 hyperlink output", result.Stdout)
	}
}

func TestLSHyperlinkRecursiveHeadersAndLowercaseEscaping(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-hyperlink/sub/col:on", []byte("x"))
	writeSessionFile(t, session, "/tmp/ls-hyperlink/sub/back\\slash", []byte("x"))
	writeSessionFile(t, session, "/tmp/ls-hyperlink/sub/ques?tion", []byte("x"))

	result := mustExecSession(t, session, "ls --hyperlink=always -R /tmp/ls-hyperlink/sub\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "\x1b]8;;file:///tmp/ls-hyperlink/sub\x1b\\/tmp/ls-hyperlink/sub\x1b]8;;\x1b\\:") {
		t.Fatalf("Stdout = %q, want recursive header hyperlink", result.Stdout)
	}
	for _, want := range []string{
		"file:///tmp/ls-hyperlink/sub/back%5cslash",
		"file:///tmp/ls-hyperlink/sub/col%3aon",
		"file:///tmp/ls-hyperlink/sub/ques%3ftion",
	} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("Stdout = %q, want hyperlink target %q", result.Stdout, want)
		}
	}
}

func TestLSSupportsFormatSortingAndFilteringParityFlags(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-parity/a1.txt", []byte("a"))
	writeSessionFile(t, session, "/tmp/ls-parity/a10.txt", []byte("aaaaaaaaaa"))
	writeSessionFile(t, session, "/tmp/ls-parity/a2.log", []byte("aa"))
	writeSessionFile(t, session, "/tmp/ls-parity/.hidden", []byte("h"))
	writeSessionFile(t, session, "/tmp/ls-parity/backup~", []byte("b"))
	writeSessionFile(t, session, "/tmp/ls-parity/subdir/file.txt", []byte("sub"))

	result := mustExecSession(t, session,
		"ls --format=commas /tmp/ls-parity\n"+
			"echo ---\n"+
			"ls -v /tmp/ls-parity\n"+
			"echo ---\n"+
			"ls -X /tmp/ls-parity\n"+
			"echo ---\n"+
			"ls -A -B --hide='a*.txt' --ignore='*.log' /tmp/ls-parity\n"+
			"echo ---\n"+
			"ls -p /tmp/ls-parity\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 5 {
		t.Fatalf("Stdout blocks = %q, want 5 blocks", result.Stdout)
	}

	if got := strings.TrimSpace(parts[0]); !strings.Contains(got, "a1.txt, a10.txt, a2.log, backup~, subdir") {
		t.Fatalf("comma format = %q, want comma-separated entries", got)
	}
	if got := splitTrimmedLines(parts[1]); !slices.Equal(got, []string{"a1.txt", "a2.log", "a10.txt", "backup~", "subdir"}) {
		t.Fatalf("version sort = %v, want natural order", got)
	}
	if got := splitTrimmedLines(parts[2]); !slices.Equal(got, []string{"backup~", "subdir", "a2.log", "a1.txt", "a10.txt"}) {
		t.Fatalf("extension sort = %v, want extension order", got)
	}
	filtered := splitTrimmedLines(parts[3])
	if slices.Contains(filtered, "a1.txt") || slices.Contains(filtered, "a10.txt") || slices.Contains(filtered, "a2.log") || slices.Contains(filtered, "backup~") {
		t.Fatalf("filtered output = %v, want hidden/ignored entries removed", filtered)
	}
	if !slices.Contains(filtered, ".hidden") || !slices.Contains(filtered, "subdir") {
		t.Fatalf("filtered output = %v, want remaining entries preserved", filtered)
	}
	if got := splitTrimmedLines(parts[4]); !slices.Contains(got, "subdir/") {
		t.Fatalf("slash indicator output = %v, want directory suffix", got)
	}
}

func TestLSSupportsQuotingHideControlAndZeroOutput(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-quote/plain.txt", []byte("p"))
	writeSessionFile(t, session, "/tmp/ls-quote/a\tb\nc", []byte("x"))

	result := mustExecSession(t, session,
		"ls -Q /tmp/ls-quote\n"+
			"echo ---\n"+
			"ls -b /tmp/ls-quote\n"+
			"echo ---\n"+
			"ls -q /tmp/ls-quote\n"+
			"echo ---\n"+
			"ls --zero /tmp/ls-quote\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 4 {
		t.Fatalf("Stdout blocks = %q, want 4 blocks", result.Stdout)
	}
	if !strings.Contains(parts[0], "\"plain.txt\"") {
		t.Fatalf("quote-name output = %q, want double-quoted file name", parts[0])
	}
	if !strings.Contains(parts[1], "a\\tb\\nc") {
		t.Fatalf("escape output = %q, want backslash escapes", parts[1])
	}
	if !strings.Contains(parts[2], "a?b?c") {
		t.Fatalf("hide-control output = %q, want control chars replaced", parts[2])
	}
	if !strings.Contains(parts[3], "plain.txt\x00") || !strings.Contains(parts[3], "a\tb\nc\x00") {
		t.Fatalf("zero output = %q, want NUL terminators", parts[3])
	}
}

func TestLSSupportsQuotingAliasAndWidthZeroWithTabsize(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-width/a", []byte("a"))
	writeSessionFile(t, session, "/tmp/ls-width/b", []byte("b"))
	writeSessionFile(t, session, "/tmp/ls-width/target file", []byte("x"))
	if err := session.FileSystem().Symlink(context.Background(), "target file", "/tmp/ls-width/link"); err != nil {
		t.Fatalf("Symlink(link) error = %v", err)
	}

	result := mustExecSession(t, session,
		"cd /tmp/ls-width\n"+
			"ls -w0 -x -T1 a b\n"+
			"echo ---\n"+
			"ls -l --quoting=shell-escape link\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}
	if got := strings.TrimSpace(parts[0]); got != "a  b" {
		t.Fatalf("width/tabsize output = %q, want %q", got, "a  b")
	}
	if !strings.Contains(parts[1], "link -> 'target file'") {
		t.Fatalf("quoting alias output = %q, want shell-escaped symlink target", parts[1])
	}
}

func TestLSLongShellEscapeSpacingRespectsTimeStyleEnv(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-quote-align/a b", []byte("a"))
	writeSessionFile(t, session, "/tmp/ls-quote-align/c.foo", []byte("b"))

	result := mustExecSession(t, session,
		"cd /tmp\n"+
			"env TERM=xterm LS_COLORS='*.foo=0;31;42' ls -x --quoting=shell-escape --color=always ls-quote-align\n"+
			"echo ---\n"+
			"env TERM=xterm LS_COLORS='*.foo=0;31;42' TIME_STYLE=+T ls -og --quoting=shell-escape --color=always ls-quote-align\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}
	if !strings.Contains(parts[0], "'a b'   \x1b[0m\x1b[0;31;42mc.foo\x1b[0m") {
		t.Fatalf("grid output = %q, want GNU spacing for quoted/colorized grid cells", parts[0])
	}
	if !regexp.MustCompile(`\sT 'a b'`).MatchString(parts[1]) {
		t.Fatalf("long output = %q, want TIME_STYLE env to drive +T timestamps", parts[1])
	}
	if !regexp.MustCompile(`\n.* {2}\x1b\[0m\x1b\[0;31;42mc\.foo\x1b\[0m\n`).MatchString(parts[1]) {
		t.Fatalf("long output = %q, want GNU shell-escape spacing for unquoted names", parts[1])
	}
}

func TestLSInvalidWidthValuesUseGNUUsageExitCode(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	for _, script := range []string{"ls -w-1\n", "ls -w08\n"} {
		result := mustExecSession(t, session, script)
		if result.ExitCode != 2 {
			t.Fatalf("script %q ExitCode = %d, want 2; stderr=%q", script, result.ExitCode, result.Stderr)
		}
		if !strings.Contains(result.Stderr, "invalid line width") {
			t.Fatalf("script %q Stderr = %q, want invalid width diagnostic", script, result.Stderr)
		}
	}
}

func TestLSInvalidTimeStyleMatchesGNUDiagnostic(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "ls -l --time-style=XX\n")
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{
		"ls: invalid argument 'XX' for 'time style'",
		"Valid arguments are:",
		"[posix-]full-iso",
		"+FORMAT (e.g., +%H:%M) for a 'date'-style format",
		"Try 'ls --help' for more information.",
	} {
		if !strings.Contains(result.Stderr, want) {
			t.Fatalf("Stderr = %q, want %q", result.Stderr, want)
		}
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", result.Stdout)
	}
}

func TestLSGroupDirectoriesFirstTreatsSymlinkedDirectoriesAsDirectories(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/ls-group/dir/b", 0o755); err != nil {
		t.Fatalf("MkdirAll(dir/b) error = %v", err)
	}
	writeSessionFile(t, session, "/tmp/ls-group/dir/a", []byte("a"))
	if err := session.FileSystem().Symlink(context.Background(), "b", "/tmp/ls-group/dir/bl"); err != nil {
		t.Fatalf("Symlink(bl) error = %v", err)
	}

	result := mustExecSession(t, session,
		"cd /tmp/ls-group\n"+
			"ls --group-directories-first dir\n"+
			"echo ---\n"+
			"ls --group-directories-first -d dir/*\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}
	if got, want := parts[0], "b\nbl\na\n"; got != want {
		t.Fatalf("directory listing = %q, want %q", got, want)
	}
	if got, want := parts[1], "dir/b\ndir/bl\ndir/a\n"; got != want {
		t.Fatalf("directory-only listing = %q, want %q", got, want)
	}
}

func TestLSZeroKeepsLongFormatAndDisablesFormattingOverrides(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-zero/dir/a", []byte("a"))
	writeSessionFile(t, session, "/tmp/ls-zero/dir/b", []byte("b"))
	writeSessionFile(t, session, "/tmp/ls-zero/dir/cc", []byte("cc"))

	longResult := mustExecSession(t, session, "cd /tmp/ls-zero\nLC_ALL=C ls -l --zero dir\n")
	if longResult.ExitCode != 0 {
		t.Fatalf("long ExitCode = %d, want 0; stderr=%q", longResult.ExitCode, longResult.Stderr)
	}
	if !strings.Contains(longResult.Stdout, "total ") {
		t.Fatalf("long zero output = %q, want long-format total line", longResult.Stdout)
	}

	zeroResult := mustExecSession(t, session, "cd /tmp/ls-zero\nLC_ALL=C ls --color=always -x -m -C -Q -q --zero dir\n")
	if zeroResult.ExitCode != 0 {
		t.Fatalf("disabled ExitCode = %d, want 0; stderr=%q", zeroResult.ExitCode, zeroResult.Stderr)
	}
	if got, want := zeroResult.Stdout, "a\x00b\x00cc\x00"; got != want {
		t.Fatalf("disabled zero output = %q, want %q", got, want)
	}
}

func TestLSDereferenceListsImplicitBrokenSymlinksAndRecursesIntoSymlinkDirs(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/ls-follow/dir/sub", 0o755); err != nil {
		t.Fatalf("MkdirAll(dir/sub) error = %v", err)
	}
	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/ls-follow/dir1", 0o755); err != nil {
		t.Fatalf("MkdirAll(dir1) error = %v", err)
	}
	if err := session.FileSystem().Symlink(context.Background(), "link", "/tmp/ls-follow/dir/link"); err != nil {
		t.Fatalf("Symlink(link) error = %v", err)
	}
	if err := session.FileSystem().Symlink(context.Background(), "../../dir1", "/tmp/ls-follow/dir/sub/link-to-dir"); err != nil {
		t.Fatalf("Symlink(link-to-dir) error = %v", err)
	}

	result := mustExecSession(t, session,
		"cd /tmp/ls-follow/dir\n"+
			"LC_ALL=C ls -L\n"+
			"echo ---\n"+
			"LC_ALL=C ls -FLR sub\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}
	if got, want := parts[0], "link\nsub\n"; got != want {
		t.Fatalf("ls -L output = %q, want %q", got, want)
	}
	if got, want := parts[1], "sub:\nlink-to-dir/\n\nsub/link-to-dir:\n"; got != want {
		t.Fatalf("ls -FLR output = %q, want %q", got, want)
	}
}

func TestLSDereferenceExplicitBrokenSymlinkStillFails(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/ls-explicit/dir", 0o755); err != nil {
		t.Fatalf("MkdirAll(dir) error = %v", err)
	}
	if err := session.FileSystem().Symlink(context.Background(), "link", "/tmp/ls-explicit/dir/link"); err != nil {
		t.Fatalf("Symlink(link) error = %v", err)
	}

	classify := mustExecSession(t, session, "cd /tmp/ls-explicit/dir\nls -F link >/dev/null\n")
	if classify.ExitCode != 0 {
		t.Fatalf("classify ExitCode = %d, want 0; stderr=%q", classify.ExitCode, classify.Stderr)
	}

	failed := mustExecSession(t, session, "cd /tmp/ls-explicit/dir\nls -L link\n")
	if failed.ExitCode != 2 {
		t.Fatalf("dereference ExitCode = %d, want 2; stderr=%q", failed.ExitCode, failed.Stderr)
	}
}

func TestLSRecursiveListsCommandLineFilesBeforeDirectories(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/ls-mixed/x", 0o755); err != nil {
		t.Fatalf("MkdirAll(x) error = %v", err)
	}
	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/ls-mixed/y", 0o755); err != nil {
		t.Fatalf("MkdirAll(y) error = %v", err)
	}
	writeSessionFile(t, session, "/tmp/ls-mixed/f", []byte("f"))

	result := mustExecSession(t, session, "cd /tmp/ls-mixed\nls -R1 x y f\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "f\n\nx:\n\ny:\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLSTrailingSlashDereferencesCommandLineSymlinkToDir(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})

	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/ls-slash/dir", 0o755); err != nil {
		t.Fatalf("MkdirAll(dir) error = %v", err)
	}
	if err := session.FileSystem().Symlink(context.Background(), "dir", "/tmp/ls-slash/symlink"); err != nil {
		t.Fatalf("Symlink(symlink) error = %v", err)
	}

	result := mustExecSession(t, session, "cd /tmp/ls-slash\nls -l symlink/\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "total 0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q; stderr=%q", got, want, result.Stderr)
	}
}

func TestDirDefaultsToColumnsAndRespectsExplicitFormats(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/dir-format/alpha.txt", []byte("alpha"))
	writeSessionFile(t, session, "/tmp/dir-format/beta.txt", []byte("beta"))
	writeSessionFile(t, session, "/tmp/dir-format/gamma.txt", []byte("gamma"))

	result := mustExecSession(t, session,
		"dir /tmp/dir-format\n"+
			"echo ---\n"+
			"dir --format=single-column /tmp/dir-format\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout blocks = %q, want 2 blocks", result.Stdout)
	}
	if got := strings.TrimSpace(parts[0]); !strings.Contains(got, "alpha.txt  beta.txt") {
		t.Fatalf("dir default output = %q, want column-style spacing", got)
	}
	if got := splitTrimmedLines(parts[1]); !slices.Equal(got, []string{"alpha.txt", "beta.txt", "gamma.txt"}) {
		t.Fatalf("dir single-column output = %v, want one-per-line format", got)
	}
}

func TestLSDiredParityModes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-dired/dir/a", []byte("a"))

	result := mustExecSession(t, session,
		"ls --dired /tmp/ls-dired/dir\n"+
			"echo ---\n"+
			"ls --dired --hyperlink=always -R /tmp/ls-dired/dir\n"+
			"echo ---\n"+
			"ls --hyperlink=always --dired -R /tmp/ls-dired/dir\n",
	)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 3 {
		t.Fatalf("Stdout blocks = %q, want 3 blocks", result.Stdout)
	}
	if !strings.Contains(parts[0], "  total 1\n") {
		t.Fatalf("dired output = %q, want indented long format", parts[0])
	}
	if !strings.Contains(parts[0], "//DIRED//") || !strings.Contains(parts[0], "//DIRED-OPTIONS// --quoting-style=literal") {
		t.Fatalf("dired output = %q, want dired metadata footer", parts[0])
	}
	if !strings.Contains(parts[1], "file:///tmp/ls-dired/dir/a") || strings.Contains(parts[1], "//DIRED//") {
		t.Fatalf("dired then hyperlink output = %q, want hyperlinks without dired footer", parts[1])
	}
	if strings.Contains(parts[2], "file:///tmp/ls-dired/dir/a") || !strings.Contains(parts[2], "//DIRED//") {
		t.Fatalf("hyperlink then dired output = %q, want dired footer without hyperlinks", parts[2])
	}
}

func TestLSDiredRejectsZeroAndTracksSymlinkFields(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-dired-link/target", []byte("x"))
	if err := session.FileSystem().Symlink(context.Background(), "target", "/tmp/ls-dired-link/link"); err != nil {
		t.Fatalf("Symlink(link) error = %v", err)
	}

	failed := mustExecSession(t, session, "ls --dired --zero /tmp/ls-dired-link\n")
	if failed.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", failed.ExitCode, failed.Stderr)
	}
	if !strings.Contains(failed.Stderr, "options '--dired' and '--zero' are incompatible") {
		t.Fatalf("Stderr = %q, want dired/zero incompatibility", failed.Stderr)
	}

	result := mustExecSession(t, session, "ls --dired -l --color=never /tmp/ls-dired-link\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "link -> target") {
		t.Fatalf("Stdout = %q, want symlink target in long output", result.Stdout)
	}
	if got, want := extractDiredFields(t, result.Stdout), []string{"link -> target", "target"}; !slices.Equal(got, want) {
		t.Fatalf("dired fields = %v, want %v\nstdout=%q", got, want, result.Stdout)
	}
}

func TestLSDiredMatchesGNUByteRangesForLongListings(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/tmp/ls-dired-ranges/dir/1a", []byte("a"))
	writeSessionFile(t, session, "/tmp/ls-dired-ranges/dir/2æ", []byte("a"))
	writeSessionFile(t, session, "/tmp/ls-dired-ranges/dir/aaa", []byte("a"))
	if err := session.FileSystem().MkdirAll(context.Background(), "/tmp/ls-dired-ranges/dir/3dir", 0o755); err != nil {
		t.Fatalf("MkdirAll(3dir) error = %v", err)
	}
	if err := session.FileSystem().Symlink(context.Background(), "target", "/tmp/ls-dired-ranges/dir/0aaa_link"); err != nil {
		t.Fatalf("Symlink(0aaa_link) error = %v", err)
	}

	result := mustExecSession(t, session, "LC_MESSAGES=C ls -l --dired /tmp/ls-dired-ranges/dir\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := extractDiredFields(t, result.Stdout), []string{"0aaa_link -> target", "1a", "2æ", "3dir", "aaa"}; !slices.Equal(got, want) {
		t.Fatalf("dired fields = %v, want %v\nstdout=%q", got, want, result.Stdout)
	}
}

func splitTrimmedLines(block string) []string {
	trimmed := strings.TrimSpace(block)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func extractDiredFields(t *testing.T, output string) []string {
	t.Helper()

	match := regexp.MustCompile(`(?m)^//DIRED//(.*)$`).FindStringSubmatch(output)
	if match == nil {
		t.Fatalf("output = %q, want //DIRED// footer", output)
	}
	fields := strings.Fields(match[1])
	if len(fields)%2 != 0 {
		t.Fatalf("dired footer = %q, want start/end pairs", match[0])
	}

	values := make([]string, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		start, err := strconv.Atoi(fields[i])
		if err != nil {
			t.Fatalf("Atoi(%q) error = %v", fields[i], err)
		}
		end, err := strconv.Atoi(fields[i+1])
		if err != nil {
			t.Fatalf("Atoi(%q) error = %v", fields[i+1], err)
		}
		if start < 0 || end < start || end > len(output) {
			t.Fatalf("dired range = [%d,%d), output length = %d", start, end, len(output))
		}
		values = append(values, output[start:end])
	}
	return values
}
