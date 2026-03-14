package runtime

import (
	"bytes"
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLSSupportsHelpDirectoryAndHumanReadableFlags(t *testing.T) {
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

func TestLSShortHRemainsHumanReadableAfterSpecMigration(t *testing.T) {
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
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "ls /tmp/missing\n")
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "ls: /tmp/missing: No such file or directory") {
		t.Fatalf("Stderr = %q, want missing-path error", result.Stderr)
	}
}

func TestDirUsesDirUsageAndNonLongDefaultOutput(t *testing.T) {
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

func TestVdirInvalidOptionUsesVdirPrefixAndExitCode(t *testing.T) {
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

func TestDirVersionAndColorSupport(t *testing.T) {
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
	if !regexp.MustCompile(`\bgroup\b`).MatchString(longNoOwner) {
		t.Fatalf("-g output = %q, want group shown", longNoOwner)
	}

	longNoGroupWithAuthor := strings.TrimSpace(parts[2])
	if strings.Contains(longNoGroupWithAuthor, " group ") {
		t.Fatalf("-o --author output = %q, want group omitted", longNoGroupWithAuthor)
	}
	if !strings.Contains(longNoGroupWithAuthor, " author ") {
		t.Fatalf("-o --author output = %q, want author shown", longNoGroupWithAuthor)
	}

	numericIDs := strings.TrimSpace(parts[3])
	if !regexp.MustCompile(`\s0\s0\s`).MatchString(numericIDs) {
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

func TestLSSupportsFormatSortingAndFilteringParityFlags(t *testing.T) {
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

func TestDirDefaultsToColumnsAndRespectsExplicitFormats(t *testing.T) {
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

func TestLSDiredRejectsZeroAndTracksSymlinkTargets(t *testing.T) {
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
	if !regexp.MustCompile(`//DIRED// \d+ \d+ \d+ \d+ \d+ \d+`).MatchString(result.Stdout) {
		t.Fatalf("Stdout = %q, want dired offsets for symlink name and target", result.Stdout)
	}
}

func splitTrimmedLines(block string) []string {
	trimmed := strings.TrimSpace(block)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
