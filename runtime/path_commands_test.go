package runtime

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
	"github.com/ewhauser/gbash/policy"
)

func TestTouchCreatesFilesAndPreservesExistingContent(t *testing.T) {
	session := newSession(t, &Config{})

	writeSessionFile(t, session, "/home/agent/existing.txt", []byte("keep\n"))
	before, err := session.FileSystem().Stat(context.Background(), "/home/agent/existing.txt")
	if err != nil {
		t.Fatalf("Stat(before) error = %v", err)
	}

	result := mustExecSession(t, session, "touch existing.txt new.txt another.txt\ncat existing.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "keep\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	for _, name := range []string{"/home/agent/new.txt", "/home/agent/another.txt"} {
		if _, err := session.FileSystem().Stat(context.Background(), name); err != nil {
			t.Fatalf("Stat(%q) error = %v", name, err)
		}
	}
	after, err := session.FileSystem().Stat(context.Background(), "/home/agent/existing.txt")
	if err != nil {
		t.Fatalf("Stat(after) error = %v", err)
	}
	if !after.ModTime().After(before.ModTime()) && !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("ModTime after touch = %v, want >= %v", after.ModTime(), before.ModTime())
	}
}

func TestTouchSupportsNoCreateAndDateParsing(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "touch -c missing.txt\ntouch -d 2024-01-02 dated.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if _, err := session.FileSystem().Stat(context.Background(), "/home/agent/missing.txt"); !os.IsNotExist(err) {
		t.Fatalf("Stat(missing.txt) error = %v, want not exist", err)
	}
	info, err := session.FileSystem().Stat(context.Background(), "/home/agent/dated.txt")
	if err != nil {
		t.Fatalf("Stat(dated.txt) error = %v", err)
	}
	if got, want := info.ModTime().UTC().Format("2006-01-02"), "2024-01-02"; got != want {
		t.Fatalf("ModTime = %q, want %q", got, want)
	}
}

func TestRmdirRemovesEmptyDirectoriesAndParents(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/a/b/c\nrmdir -p /home/agent/a/b/c\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, name := range []string{"/home/agent/a/b/c", "/home/agent/a/b", "/home/agent/a"} {
		if _, err := session.FileSystem().Stat(context.Background(), name); !os.IsNotExist(err) {
			t.Fatalf("Stat(%q) error = %v, want not exist", name, err)
		}
	}
}

func TestRmdirRejectsNonEmptyDirectories(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/dir\necho hi > /home/agent/dir/file.txt\nrmdir /home/agent/dir\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "Directory not empty") {
		t.Fatalf("Stderr = %q, want non-empty error", result.Stderr)
	}
}

func TestLNCreatesSymlinkAndReadlinkPrintsTarget(t *testing.T) {
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo data > /home/agent/file.txt\nln -s file.txt /home/agent/link.txt\nreadlink /home/agent/link.txt\nreadlink -f /home/agent/link.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "file.txt\n/home/agent/file.txt\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLNHardLinkSharesContent(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo original > /home/agent/src.txt\nln /home/agent/src.txt /home/agent/dst.txt\necho updated > /home/agent/dst.txt\ncat /home/agent/src.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "updated\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLinkHardLinkSharesContent(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo original > /home/agent/src.txt\nlink /home/agent/src.txt /home/agent/dst.txt\necho updated > /home/agent/dst.txt\ncat /home/agent/src.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "updated\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLinkReportsMissingSource(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "link /home/agent/missing.txt /home/agent/dst.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "cannot create link") || !strings.Contains(result.Stderr, "No such file or directory") {
		t.Fatalf("Stderr = %q, want missing-source error", result.Stderr)
	}
}

func TestChmodSupportsOctalAndSymbolicModes(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/file.txt\nchmod 755 /home/agent/file.txt\nstat -c '%a %A' /home/agent/file.txt\nchmod g-w,u+x /home/agent/file.txt\nstat -c '%a %A' /home/agent/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("Stdout lines = %v, want 2 lines", lines)
	}
	if got, want := lines[0], "0755 -rwxr-xr-x"; got != want {
		t.Fatalf("First stat = %q, want %q", got, want)
	}
	if got, want := lines[1], "0755 -rwxr-xr-x"; got != want {
		t.Fatalf("Second stat = %q, want %q", got, want)
	}
}

func TestChmodSupportsRecursiveMode(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/dir/sub\necho hi > /home/agent/dir/sub/file.txt\nchmod -R 700 /home/agent/dir\nstat -c '%a' /home/agent/dir/sub/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "0700"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestChownSupportsNamedAndNumericOwners(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/file.txt\nstat -c '%u:%g:%U:%G' /home/agent/file.txt\nchown 123:456 /home/agent/file.txt\nstat -c '%u:%g:%U:%G' /home/agent/file.txt\nchown agent:agent /home/agent/file.txt\nstat -c '%u:%g:%U:%G' /home/agent/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("Stdout lines = %v, want 3", lines)
	}
	if got, want := lines[0], "1000:1000:agent:agent"; got != want {
		t.Fatalf("initial stat = %q, want %q", got, want)
	}
	if got, want := lines[1], "123:456:123:456"; got != want {
		t.Fatalf("numeric chown stat = %q, want %q", got, want)
	}
	if got, want := lines[2], "1000:1000:agent:agent"; got != want {
		t.Fatalf("named chown stat = %q, want %q", got, want)
	}
}

func TestChownSupportsZeroOwnerIDs(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo hi > /home/agent/root.txt\nchown 0:0 /home/agent/root.txt\nstat -c '%u:%g:%U:%G' /home/agent/root.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "0:0:0:0"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestChownSupportsReferenceFromAndRecursiveFlags(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/tree/sub\necho ref > /home/agent/ref.txt\necho one > /home/agent/tree/file.txt\necho two > /home/agent/tree/sub/file.txt\nchown 41:42 /home/agent/ref.txt\nchown --from=1000:1000 --reference=/home/agent/ref.txt /home/agent/tree/file.txt\nchown --from=7:8 99:100 /home/agent/tree/file.txt\nchown -R 51:52 /home/agent/tree\nstat -c '%u:%g' /home/agent/tree/file.txt\nstat -c '%u:%g' /home/agent/tree/sub/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("Stdout lines = %v, want 2", lines)
	}
	for idx, line := range lines {
		if got, want := line, "51:52"; got != want {
			t.Fatalf("line %d = %q, want %q", idx, got, want)
		}
	}
}

func TestChownNoDereferenceTargetsTheSymlink(t *testing.T) {
	rt := newRuntime(t, &Config{
		Policy: policy.NewStatic(&policy.Config{
			ReadRoots:   []string{"/"},
			WriteRoots:  []string{"/"},
			SymlinkMode: policy.SymlinkFollow,
		}),
	})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo data > /home/agent/target.txt\nln -s target.txt /home/agent/link.txt\nchown 61:62 /home/agent/link.txt\nstat -c '%u:%g' /home/agent/target.txt\nstat -c '%u:%g %F' /home/agent/link.txt\nchown -h 71:72 /home/agent/link.txt\nstat -c '%u:%g' /home/agent/target.txt\nstat -c '%u:%g %F' /home/agent/link.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 4 {
		t.Fatalf("Stdout lines = %v, want 4", lines)
	}
	if got, want := lines[0], "61:62"; got != want {
		t.Fatalf("target after dereference = %q, want %q", got, want)
	}
	if got, want := lines[1], "1000:1000 symbolic link"; got != want {
		t.Fatalf("link before -h = %q, want %q", got, want)
	}
	if got, want := lines[2], "61:62"; got != want {
		t.Fatalf("target after -h = %q, want %q", got, want)
	}
	if got, want := lines[3], "71:72 symbolic link"; got != want {
		t.Fatalf("link after -h = %q, want %q", got, want)
	}
}

func TestChownDoesNotChangeModTime(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/mtime.txt", []byte("hello\n"))

	before, err := session.FileSystem().Stat(context.Background(), "/home/agent/mtime.txt")
	if err != nil {
		t.Fatalf("Stat(before) error = %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	result := mustExecSession(t, session, "chown 123:456 /home/agent/mtime.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	after, err := session.FileSystem().Stat(context.Background(), "/home/agent/mtime.txt")
	if err != nil {
		t.Fatalf("Stat(after) error = %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("ModTime after chown = %v, want unchanged from %v", after.ModTime(), before.ModTime())
	}
}

func TestStatFormatsMultipleFilesAndContinuesOnError(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/one.txt", []byte("hello"))

	result := mustExecSession(t, session, "stat -c '%n %s %F' /home/agent/one.txt /home/agent/missing\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := strings.TrimSpace(result.Stdout), "/home/agent/one.txt 5 regular file"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "missing") {
		t.Fatalf("Stderr = %q, want missing-file message", result.Stderr)
	}
}

func TestBasenameAndDirnameHandleSuffixesAndMultipleOperands(t *testing.T) {
	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "basename -a -s .txt /tmp/a.txt /tmp/b.txt\ndirname /tmp/a.txt plain /root/\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\n/tmp\n.\n/\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBasenameSupportsLongSuffixFlag(t *testing.T) {
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "basename --suffix .log /tmp/build.log\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "build\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestTreeShowsHiddenFilesAndDepthLimits(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/dir/sub\necho hi > /home/agent/dir/file.txt\necho hidden > /home/agent/dir/.secret\necho nested > /home/agent/dir/sub/nested.txt\ntree -L 1 /home/agent/dir\necho ---\ntree -a -L 1 /home/agent/dir\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("tree output blocks = %q, want 2 parts", result.Stdout)
	}
	if strings.Contains(parts[0], ".secret") || strings.Contains(parts[0], "nested.txt") {
		t.Fatalf("first tree block = %q, want hidden and depth-limited entries omitted", parts[0])
	}
	if !strings.Contains(parts[1], ".secret") {
		t.Fatalf("second tree block = %q, want hidden file", parts[1])
	}
}

func TestDUReportsSummaryAllEntriesAndTotals(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /home/agent/dir/sub\necho -n hello > /home/agent/dir/a.txt\necho -n world!! > /home/agent/dir/sub/b.txt\ndu -a /home/agent/dir\ndu -s -c /home/agent/dir /home/agent/dir/sub\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "/home/agent/dir/a.txt") || !strings.Contains(result.Stdout, "/home/agent/dir/sub/b.txt") {
		t.Fatalf("Stdout = %q, want file entries", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "total") {
		t.Fatalf("Stdout = %q, want grand total", result.Stdout)
	}
}

func TestFileDetectsMagicTextAndDirectories(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/image.png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00})
	writeSessionFile(t, session, "/home/agent/script.sh", []byte("#!/bin/sh\necho hi\n"))
	if err := session.FileSystem().MkdirAll(context.Background(), "/home/agent/docs", 0o755); err != nil {
		t.Fatalf("MkdirAll(docs) error = %v", err)
	}

	result := mustExecSession(t, session, "file /home/agent/image.png\nfile -i /home/agent/script.sh\nfile -b /home/agent/docs\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != 3 {
		t.Fatalf("Stdout lines = %v, want 3 lines", lines)
	}
	if !strings.Contains(lines[0], "PNG image data") {
		t.Fatalf("line 1 = %q, want PNG detection", lines[0])
	}
	if got, want := lines[1], "/home/agent/script.sh: text/x-shellscript"; got != want {
		t.Fatalf("line 2 = %q, want %q", got, want)
	}
	if got, want := lines[2], "directory"; got != want {
		t.Fatalf("line 3 = %q, want %q", got, want)
	}
}

func TestFileMissingPathReportsErrorOnStdout(t *testing.T) {
	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: "file /missing\n"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "cannot open") {
		t.Fatalf("Stdout = %q, want missing-path message", result.Stdout)
	}
}

func TestFileSupportsLongBriefAndMimeFlags(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/note.txt", []byte("hello\n"))

	result := mustExecSession(t, session, "file --brief /home/agent/note.txt\nfile --mime /home/agent/note.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "ASCII text\n/home/agent/note.txt: text/plain\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestOverlayFactorySupportsHardLinksAndMetadataCopyUp(t *testing.T) {
	rt := newRuntime(t, &Config{
		FileSystem: CustomFileSystem(
			gbfs.Overlay(seededFSFactory{files: map[string]string{
				"/seed.txt": "seed\n",
			}}),
			defaultHomeDir,
		),
	})

	session, err := rt.NewSession(context.Background())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	result := mustExecSession(t, session, "ln /seed.txt /copy.txt\nchmod 700 /seed.txt\nstat -c '%a' /seed.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "0700"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := readTestFSFile(t, session.FileSystem(), "/copy.txt"); got != "seed\n" {
		t.Fatalf("copy.txt content = %q, want seed", got)
	}
}

func readTestFSFile(t *testing.T, fsys gbfs.FileSystem, name string) string {
	t.Helper()

	file, err := fsys.Open(context.Background(), name)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("ReadAll(%q) error = %v", name, err)
	}
	return string(data)
}

func TestTouchDateParsingUsesStableTimestamp(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "touch --date=2024/03/04 /home/agent/date.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	info, err := session.FileSystem().Stat(context.Background(), "/home/agent/date.txt")
	if err != nil {
		t.Fatalf("Stat(date.txt) error = %v", err)
	}
	if got, want := info.ModTime().UTC(), time.Date(2024, 3, 4, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("ModTime = %v, want %v", got, want)
	}
}
