package builtins

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	stdfs "io/fs"

	gbfs "github.com/ewhauser/gbash/fs"
)

type removeNotExistAfterDeleteFS struct {
	gbfs.FileSystem
	target string
}

type permissionDeniedReadDirFS struct {
	gbfs.FileSystem
	targets map[string]struct{}
}

type wrappedTTYReader struct {
	reader any
}

type redirectMetadataReader struct {
	*strings.Reader
	path string
}

type redirectFDReader struct {
	redirectMetadataReader
	fd uintptr
}

func (fs removeNotExistAfterDeleteFS) Remove(ctx context.Context, name string, recursive bool) error {
	if name == fs.target {
		if err := fs.FileSystem.Remove(ctx, name, recursive); err != nil {
			return err
		}
		return stdfs.ErrNotExist
	}
	return fs.FileSystem.Remove(ctx, name, recursive)
}

func (fs permissionDeniedReadDirFS) ReadDir(ctx context.Context, name string) ([]stdfs.DirEntry, error) {
	if _, ok := fs.targets[name]; ok {
		return nil, &stdfs.PathError{Op: "readdir", Path: name, Err: stdfs.ErrPermission}
	}
	return fs.FileSystem.ReadDir(ctx, name)
}

func (r wrappedTTYReader) Read(p []byte) (int, error) {
	reader, _ := r.reader.(io.Reader)
	if reader == nil {
		return 0, io.EOF
	}
	return reader.Read(p)
}

func (r wrappedTTYReader) UnderlyingReader() io.Reader {
	reader, _ := r.reader.(io.Reader)
	return reader
}

func (r redirectMetadataReader) RedirectPath() string { return r.path }

func (redirectMetadataReader) RedirectFlags() int { return 0 }

func (redirectMetadataReader) RedirectOffset() int64 { return 0 }

func (r redirectFDReader) Fd() uintptr { return r.fd }

func parseRMSpec(t *testing.T, args ...string) (*Invocation, *ParsedCommand, string, error) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	inv := &Invocation{
		Args:   args,
		Stdout: &stdout,
		Stderr: &stderr,
	}
	spec := NewRM().Spec()
	parsed, action, err := ParseCommandSpec(inv, &spec)
	return inv, parsed, action, err
}

func TestParseRMSpecParsesGroupedAndOptionalInteractiveFlags(t *testing.T) {
	t.Parallel()

	_, matches, action, err := parseRMSpec(t, "-rfv", "--interactive=once", "target")
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	for _, name := range []string{"force", "recursive", "verbose", "interactive"} {
		if !matches.Has(name) {
			t.Fatalf("%s option not parsed: %#v", name, matches.OptionOrder())
		}
	}
	if got, want := matches.Value("interactive"), "once"; got != want {
		t.Fatalf("interactive value = %q, want %q", got, want)
	}
}

func TestParseRMSpecTreatsBareInteractiveAsAlways(t *testing.T) {
	t.Parallel()

	inv, matches, action, err := parseRMSpec(t, "--interactive", "target")
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	opts, err := parseRMMatches(inv, matches)
	if err != nil {
		t.Fatalf("parseRMMatches() error = %v", err)
	}
	if got, want := opts.interactive, rmInteractiveAlways; got != want {
		t.Fatalf("interactive = %v, want %v", got, want)
	}
}

func TestParseRMSpecUsesPerOccurrenceInteractiveValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want rmInteractiveMode
	}{
		{
			name: "bare interactive overrides prior value",
			args: []string{"--interactive=never", "--interactive", "target"},
			want: rmInteractiveAlways,
		},
		{
			name: "explicit never remains last override",
			args: []string{"--interactive", "--interactive=never", "target"},
			want: rmInteractiveNever,
		},
		{
			name: "interactive clears prior force",
			args: []string{"-f", "-i", "target"},
			want: rmInteractiveAlways,
		},
		{
			name: "interactive never preserves prior force",
			args: []string{"-f", "--interactive=never", "target"},
			want: rmInteractiveNever,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inv, matches, action, err := parseRMSpec(t, tt.args...)
			if err != nil {
				t.Fatalf("ParseCommandSpec() error = %v", err)
			}
			if action != "" {
				t.Fatalf("action = %q, want empty", action)
			}
			opts, err := parseRMMatches(inv, matches)
			if err != nil {
				t.Fatalf("parseRMMatches() error = %v", err)
			}
			if got := opts.interactive; got != tt.want {
				t.Fatalf("interactive = %v, want %v", got, tt.want)
			}
			switch tt.name {
			case "interactive clears prior force":
				if opts.force {
					t.Fatalf("force = true, want false after later interactive option")
				}
			case "interactive never preserves prior force":
				if !opts.force {
					t.Fatalf("force = false, want true when --interactive=never follows -f")
				}
			}
		})
	}
}

func TestParseRMSpecRejectsExplicitEmptyInteractiveValue(t *testing.T) {
	t.Parallel()

	inv, matches, action, err := parseRMSpec(t, "--interactive=", "target")
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	_, err = parseRMMatches(inv, matches)
	if err == nil {
		t.Fatal("parseRMMatches() error = nil, want empty interactive value failure")
	}
	if !strings.Contains(err.Error(), "invalid argument '' for '--interactive'") {
		t.Fatalf("parseRMMatches() error = %v, want empty interactive diagnostic", err)
	}
}

func TestParseRMSpecRejectsAbbreviatedNoPreserveRoot(t *testing.T) {
	t.Parallel()

	inv, matches, action, err := parseRMSpec(t, "-r", "--no-preserve-r", "/tmp/data")
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	_, err = parseRMMatches(inv, matches)
	if err == nil {
		t.Fatal("parseRMMatches() error = nil, want abbreviation failure")
	}
	if !strings.Contains(err.Error(), "may not abbreviate") {
		t.Fatalf("parseRMMatches() error = %v, want abbreviation diagnostic", err)
	}
}

func TestParseRMSpecRejectsAbbreviatedNoPreserveRootWithExactPositional(t *testing.T) {
	t.Parallel()

	inv, matches, action, err := parseRMSpec(t, "-r", "--no-preserve-r", "--", "--no-preserve-root")
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	_, err = parseRMMatches(inv, matches)
	if err == nil {
		t.Fatal("parseRMMatches() error = nil, want abbreviation failure")
	}
	if !strings.Contains(err.Error(), "may not abbreviate") {
		t.Fatalf("parseRMMatches() error = %v, want abbreviation diagnostic", err)
	}
}

func TestParseRMSpecAcceptsTripleHyphenPresumeInputTTY(t *testing.T) {
	t.Parallel()

	_, matches, action, err := parseRMSpec(t, "---presume-input-tty", "target")
	if err != nil {
		t.Fatalf("ParseCommandSpec() error = %v", err)
	}
	if action != "" {
		t.Fatalf("action = %q, want empty", action)
	}
	if !matches.Has("presume-input-tty") {
		t.Fatalf("presume-input-tty option not parsed")
	}
}

func TestRMForceIgnoresRemoveNotExistAfterSuccessfulLstat(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := gbfs.NewMemory()
	file, err := mem.OpenFile(ctx, "/tmp/race", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var stderr bytes.Buffer
	inv := NewInvocation(&InvocationOptions{
		Cwd:        "/",
		FileSystem: removeNotExistAfterDeleteFS{FileSystem: mem, target: "/tmp/race"},
		Stderr:     &stderr,
	})
	info, err := inv.FS.Lstat(ctx, "/tmp/race")
	if err != nil {
		t.Fatalf("Lstat() error = %v", err)
	}

	result, err := rmRemoveFile(ctx, inv, "/tmp/race", "/tmp/race", info, rmOptions{force: true})
	if err != nil {
		t.Fatalf("rmRemoveFile() error = %v", err)
	}
	if result.hadErr {
		t.Fatalf("hadErr = true, want false")
	}
	if !result.removed {
		t.Fatalf("removed = false, want true when target disappears during forced removal")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if _, err := inv.FS.Lstat(ctx, "/tmp/race"); !errorsIsNotExist(err) {
		t.Fatalf("post-remove Lstat() error = %v, want not exist", err)
	}
}

func TestRMRecursiveEmptyOperandDoesNotTouchWorkingDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := gbfs.NewMemory()
	file, err := mem.OpenFile(ctx, "/tmp/keep", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var stderr bytes.Buffer
	inv := NewInvocation(&InvocationOptions{
		Cwd:        "/tmp",
		FileSystem: mem,
		Stderr:     &stderr,
	})

	result, err := rmRemovePath(ctx, inv, "", rmOptions{recursive: true, promptInput: &rmPromptInput{}})
	if err != nil {
		t.Fatalf("rmRemovePath() error = %v", err)
	}
	if !result.hadErr {
		t.Fatalf("hadErr = false, want true")
	}
	if result.removed {
		t.Fatalf("removed = true, want false")
	}
	if got, want := stderr.String(), "rm: cannot remove '': No such file or directory\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if _, err := inv.FS.Lstat(ctx, "/tmp/keep"); err != nil {
		t.Fatalf("post-remove Lstat(/tmp/keep) error = %v, want file to remain", err)
	}
}

func TestRMForceIgnoresInvalidLookupErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := gbfs.NewMemory()
	file, err := mem.OpenFile(ctx, "/tmp/existing-non-dir", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var stderr bytes.Buffer
	inv := NewInvocation(&InvocationOptions{
		Cwd:        "/tmp",
		FileSystem: mem,
		Stderr:     &stderr,
	})

	result, err := rmRemovePath(ctx, inv, "existing-non-dir/f", rmOptions{force: true, interactive: rmInteractiveNever, promptInput: &rmPromptInput{}})
	if err != nil {
		t.Fatalf("rmRemovePath() error = %v", err)
	}
	if result.hadErr {
		t.Fatalf("hadErr = true, want false")
	}
	if result.removed {
		t.Fatalf("removed = true, want false")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRMInteractiveAlwaysUsesWriteProtectedPrompts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		contents   string
		wantPrompt string
	}{
		{
			name:       "empty file",
			contents:   "",
			wantPrompt: "rm: remove write-protected regular empty file 'empty'? ",
		},
		{
			name:       "non-empty file",
			contents:   "data",
			wantPrompt: "rm: remove write-protected regular file 'full'? ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			mem := gbfs.NewMemory()
			name := "empty"
			if tt.contents != "" {
				name = "full"
			}
			file, err := mem.OpenFile(ctx, "/tmp/"+name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				t.Fatalf("OpenFile() error = %v", err)
			}
			if _, err := file.Write([]byte(tt.contents)); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			if err := file.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			if err := mem.Chmod(ctx, "/tmp/"+name, 0o444); err != nil {
				t.Fatalf("Chmod() error = %v", err)
			}

			var stderr bytes.Buffer
			inv := NewInvocation(&InvocationOptions{
				Cwd:        "/tmp",
				FileSystem: mem,
				Stdin:      bytes.NewBufferString("y\n"),
				Stderr:     &stderr,
			})
			info, err := inv.FS.Lstat(ctx, "/tmp/"+name)
			if err != nil {
				t.Fatalf("Lstat() error = %v", err)
			}

			result, err := rmRemoveFile(ctx, inv, name, "/tmp/"+name, info, rmOptions{interactive: rmInteractiveAlways, promptInput: &rmPromptInput{}})
			if err != nil {
				t.Fatalf("rmRemoveFile() error = %v", err)
			}
			if result.hadErr {
				t.Fatalf("hadErr = true, want false")
			}
			if !result.removed {
				t.Fatalf("removed = false, want true")
			}
			if got := stderr.String(); got != tt.wantPrompt {
				t.Fatalf("stderr = %q, want %q", got, tt.wantPrompt)
			}
		})
	}
}

func TestRMDirDoesNotPromptWhenTTYEnvButRedirectedStdin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := gbfs.NewMemory()
	if err := mem.MkdirAll(ctx, "/tmp/inacc", 0o000); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	var stderr bytes.Buffer
	inv := NewInvocation(&InvocationOptions{
		Cwd:        "/tmp",
		Env:        map[string]string{"TTY": "/dev/pts/0"},
		FileSystem: mem,
		Stdin:      strings.NewReader(""),
		Stderr:     &stderr,
	})

	result, err := rmRemovePath(ctx, inv, "inacc", rmOptions{dir: true, promptInput: &rmPromptInput{}})
	if err != nil {
		t.Fatalf("rmRemovePath() error = %v", err)
	}
	if result.hadErr {
		t.Fatalf("hadErr = true, want false")
	}
	if !result.removed {
		t.Fatalf("removed = false, want true")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if _, err := inv.FS.Lstat(ctx, "/tmp/inacc"); !errorsIsNotExist(err) {
		t.Fatalf("post-remove Lstat() error = %v, want not exist", err)
	}
}

func TestRMInputIsTTYUnwrapsRedirectedReaders(t *testing.T) {
	t.Parallel()

	if rmInputIsTTY(&Invocation{
		Stdin: wrappedTTYReader{
			reader: redirectMetadataReader{Reader: strings.NewReader(""), path: "/dev/null"},
		},
	}, rmOptions{}) {
		t.Fatal("rmInputIsTTY(/dev/null) = true, want false")
	}

	if !rmInputIsTTY(&Invocation{
		Stdin: wrappedTTYReader{
			reader: redirectMetadataReader{Reader: strings.NewReader(""), path: "/dev/tty"},
		},
	}, rmOptions{}) {
		t.Fatal("rmInputIsTTY(/dev/tty) = false, want true")
	}

	if rmInputIsTTY(&Invocation{
		Stdin: redirectFDReader{
			redirectMetadataReader: redirectMetadataReader{Reader: strings.NewReader(""), path: "/dev/null"},
			fd:                     0,
		},
	}, rmOptions{}) {
		t.Fatal("rmInputIsTTY(/dev/null fd=0) = true, want false")
	}
}

func TestRMRecursiveRemovesUnreadableEmptyDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := gbfs.NewMemory()
	if err := mem.MkdirAll(ctx, "/tmp/inacc", 0o000); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	var stderr bytes.Buffer
	inv := NewInvocation(&InvocationOptions{
		Cwd: "/tmp",
		FileSystem: permissionDeniedReadDirFS{
			FileSystem: mem,
			targets:    map[string]struct{}{"/tmp/inacc": {}},
		},
		Stderr: &stderr,
	})

	result, err := rmRemovePath(ctx, inv, "inacc", rmOptions{recursive: true, promptInput: &rmPromptInput{}})
	if err != nil {
		t.Fatalf("rmRemovePath() error = %v", err)
	}
	if result.hadErr {
		t.Fatalf("hadErr = true, want false")
	}
	if !result.removed {
		t.Fatalf("removed = false, want true")
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
	if _, err := inv.FS.Lstat(ctx, "/tmp/inacc"); !errorsIsNotExist(err) {
		t.Fatalf("post-remove Lstat() error = %v, want not exist", err)
	}
}

func TestRMRecursiveUnreadableNonEmptyDirectoryReportsPermissionDenied(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := gbfs.NewMemory()
	if err := mem.MkdirAll(ctx, "/tmp/dir", 0o000); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	file, err := mem.OpenFile(ctx, "/tmp/dir/x", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var stderr bytes.Buffer
	inv := NewInvocation(&InvocationOptions{
		Cwd: "/tmp",
		FileSystem: permissionDeniedReadDirFS{
			FileSystem: mem,
			targets:    map[string]struct{}{"/tmp/dir": {}},
		},
		Stderr: &stderr,
	})

	result, err := rmRemovePath(ctx, inv, "dir", rmOptions{recursive: true, promptInput: &rmPromptInput{}})
	if err != nil {
		t.Fatalf("rmRemovePath() error = %v", err)
	}
	if !result.hadErr {
		t.Fatalf("hadErr = false, want true")
	}
	if result.removed {
		t.Fatalf("removed = true, want false")
	}
	if got, want := stderr.String(), "rm: cannot remove 'dir': Permission denied\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if _, err := inv.FS.Lstat(ctx, "/tmp/dir"); err != nil {
		t.Fatalf("post-remove Lstat(/tmp/dir) error = %v, want directory to remain", err)
	}
}

func TestRMVerbosePreservesTopLevelTrailingSlash(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	mem := gbfs.NewMemory()
	if err := mem.MkdirAll(ctx, "/tmp/a", 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	file, err := mem.OpenFile(ctx, "/tmp/a/x", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var stdout bytes.Buffer
	inv := NewInvocation(&InvocationOptions{
		Cwd:        "/tmp",
		FileSystem: mem,
		Stdout:     &stdout,
	})

	result, err := rmRemovePath(ctx, inv, "a///", rmOptions{recursive: true, interactive: rmInteractiveNever, verbose: true, promptInput: &rmPromptInput{}})
	if err != nil {
		t.Fatalf("rmRemovePath() error = %v", err)
	}
	if result.hadErr {
		t.Fatalf("hadErr = true, want false")
	}
	if !result.removed {
		t.Fatalf("removed = false, want true")
	}
	if got, want := stdout.String(), "removed 'a/x'\nremoved directory 'a/'\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
