package codingtools

import (
	"context"
	"encoding/base64"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	gbfs "github.com/ewhauser/gbash/fs"
)

func TestToolDefinitionsAndSchemas(t *testing.T) {
	t.Parallel()

	tools := New(Config{})
	defs := tools.ToolDefinitions()
	if len(defs) != 3 {
		t.Fatalf("ToolDefinitions() len = %d, want 3", len(defs))
	}
	if got := defs[0].Name; got != "read" {
		t.Fatalf("ToolDefinitions()[0].Name = %q, want read", got)
	}
	if got := defs[1].Name; got != "edit" {
		t.Fatalf("ToolDefinitions()[1].Name = %q, want edit", got)
	}
	if got := defs[2].Name; got != "write" {
		t.Fatalf("ToolDefinitions()[2].Name = %q, want write", got)
	}

	for _, def := range defs {
		if def.InputSchema["type"] != "object" {
			t.Fatalf("%s schema type = %#v, want object", def.Name, def.InputSchema["type"])
		}
		if def.InputSchema["additionalProperties"] != false {
			t.Fatalf("%s additionalProperties = %#v, want false", def.Name, def.InputSchema["additionalProperties"])
		}
	}
}

func TestParseReadRequestAcceptsFilePathAlias(t *testing.T) {
	t.Parallel()

	req, err := ParseReadRequest(map[string]any{
		"file_path": "notes.txt",
		"offset":    2,
		"limit":     5,
	})
	if err != nil {
		t.Fatalf("ParseReadRequest() error = %v", err)
	}
	if req.Path != "notes.txt" {
		t.Fatalf("Path = %q, want notes.txt", req.Path)
	}
	if req.Offset == nil || *req.Offset != 2 {
		t.Fatalf("Offset = %#v, want 2", req.Offset)
	}
	if req.Limit == nil || *req.Limit != 5 {
		t.Fatalf("Limit = %#v, want 5", req.Limit)
	}
}

func TestParseWriteRequestRejectsWrongType(t *testing.T) {
	t.Parallel()

	_, err := ParseWriteRequest(map[string]any{
		"path":    "notes.txt",
		"content": 123,
	})
	if err == nil || !strings.Contains(err.Error(), "`content` must be a string") {
		t.Fatalf("ParseWriteRequest() error = %v, want content type error", err)
	}
}

func TestParseEditRequestRequiresFields(t *testing.T) {
	t.Parallel()

	_, err := ParseEditRequest(map[string]any{
		"path":    "notes.txt",
		"oldText": "old",
	})
	if err == nil || !strings.Contains(err.Error(), "`newText` is required") {
		t.Fatalf("ParseEditRequest() error = %v, want missing newText error", err)
	}
}

func TestReadRelativeAndAbsolutePathsOnMemoryFS(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/notes.txt", "hello\n")
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	relativeResp, err := tools.Read(context.Background(), ReadRequest{Path: "notes.txt"})
	if err != nil {
		t.Fatalf("Read(relative) error = %v", err)
	}
	if got := relativeResp.Content[0].Text; got != "hello\n" {
		t.Fatalf("Read(relative) text = %q, want hello\\n", got)
	}

	absoluteResp, err := tools.Read(context.Background(), ReadRequest{Path: "/workspace/notes.txt"})
	if err != nil {
		t.Fatalf("Read(absolute) error = %v", err)
	}
	if got := absoluteResp.Content[0].Text; got != "hello\n" {
		t.Fatalf("Read(absolute) text = %q, want hello\\n", got)
	}
}

func TestReadOffsetAndLimitPagination(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/notes.txt", "one\ntwo\nthree\nfour\n")
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})
	offset := 2
	limit := 2

	resp, err := tools.Read(context.Background(), ReadRequest{
		Path:   "notes.txt",
		Offset: &offset,
		Limit:  &limit,
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := resp.Content[0].Text; got != "two\nthree\n\n[2 more lines in file. Use offset=4 to continue.]" {
		t.Fatalf("Read() text = %q", got)
	}
}

func TestReadTruncatesByLineLimit(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/notes.txt", "one\ntwo\nthree\nfour\n")
	tools := New(Config{
		FS:         fsys,
		WorkingDir: "/workspace",
		Truncation: TruncationOptions{MaxLines: 2, MaxBytes: 1024},
	})

	resp, err := tools.Read(context.Background(), ReadRequest{Path: "notes.txt"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !strings.Contains(resp.Content[0].Text, "[Showing lines 1-2 of 5. Use offset=3 to continue.]") {
		t.Fatalf("Read() text = %q, want continuation note", resp.Content[0].Text)
	}
	if resp.Details == nil || resp.Details.Truncation == nil || resp.Details.Truncation.TruncatedBy != "lines" {
		t.Fatalf("Read() truncation = %#v, want lines truncation", resp.Details)
	}
}

func TestReadTruncatesByByteLimit(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/notes.txt", "alpha\nbeta\ngamma\n")
	tools := New(Config{
		FS:         fsys,
		WorkingDir: "/workspace",
		Truncation: TruncationOptions{MaxLines: 100, MaxBytes: 9},
	})

	resp, err := tools.Read(context.Background(), ReadRequest{Path: "notes.txt"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !strings.Contains(resp.Content[0].Text, "[Showing lines 1-1 of 4 (9B limit). Use offset=2 to continue.]") {
		t.Fatalf("Read() text = %q, want byte-limit continuation note", resp.Content[0].Text)
	}
	if resp.Details == nil || resp.Details.Truncation == nil || resp.Details.Truncation.TruncatedBy != "bytes" {
		t.Fatalf("Read() truncation = %#v, want bytes truncation", resp.Details)
	}
}

func TestReadRejectsOversizedFirstLine(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/notes.txt", "abcdefgh\nx\n")
	tools := New(Config{
		FS:         fsys,
		WorkingDir: "/workspace",
		Truncation: TruncationOptions{MaxLines: 100, MaxBytes: 4},
	})

	resp, err := tools.Read(context.Background(), ReadRequest{Path: "notes.txt"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !strings.Contains(resp.Content[0].Text, "exceeds the 4B read limit") {
		t.Fatalf("Read() text = %q, want oversized first-line message", resp.Content[0].Text)
	}
	if resp.Details == nil || resp.Details.Truncation == nil || !resp.Details.Truncation.FirstLineExceedsLimit {
		t.Fatalf("Read() details = %#v, want first-line limit flag", resp.Details)
	}
}

func TestReadMissingFile(t *testing.T) {
	t.Parallel()

	tools := New(Config{FS: gbfs.NewMemory(), WorkingDir: "/workspace"})
	_, err := tools.Read(context.Background(), ReadRequest{Path: "missing.txt"})
	if err == nil {
		t.Fatal("Read() error = nil, want not-exist error")
	}
}

func TestReadImageResponse(t *testing.T) {
	t.Parallel()

	const pngBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+yF9kAAAAASUVORK5CYII="
	data, err := base64.StdEncoding.DecodeString(pngBase64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}

	fsys := gbfs.NewMemory()
	mustWriteVirtualBytes(t, fsys, "/workspace/pixel.png", data)
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	resp, err := tools.Read(context.Background(), ReadRequest{Path: "pixel.png"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(resp.Content))
	}
	if resp.Content[1].Type != "image" || resp.Content[1].MIMEType != "image/png" {
		t.Fatalf("image block = %#v, want png image block", resp.Content[1])
	}
}

func TestReadPathFallbackVariants(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/Cafe\u0301 PM.txt", "accent\n")
	mustWriteVirtualFile(t, fsys, "/workspace/Capture d\u2019ecran.txt", "quote\n")
	mustWriteVirtualFile(t, fsys, "/workspace/Screenshot\u202FPM.png", "ampm\n")
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	resp, err := tools.Read(context.Background(), ReadRequest{Path: "Café PM.txt"})
	if err != nil {
		t.Fatalf("Read(NFD) error = %v", err)
	}
	if got := resp.Content[0].Text; got != "accent\n" {
		t.Fatalf("Read(NFD) text = %q, want accent", got)
	}

	resp, err = tools.Read(context.Background(), ReadRequest{Path: "Capture d'ecran.txt"})
	if err != nil {
		t.Fatalf("Read(curly) error = %v", err)
	}
	if got := resp.Content[0].Text; got != "quote\n" {
		t.Fatalf("Read(curly) text = %q, want quote", got)
	}

	resp, err = tools.Read(context.Background(), ReadRequest{Path: "Screenshot PM.png"})
	if err != nil {
		t.Fatalf("Read(AM/PM) error = %v", err)
	}
	if got := resp.Content[0].Text; got != "ampm\n" {
		t.Fatalf("Read(AM/PM) text = %q, want ampm", got)
	}
}

func TestWriteCreatesParentsAndOverwrites(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	_, err := tools.Write(context.Background(), WriteRequest{
		Path:    "nested/note.txt",
		Content: "first\n",
	})
	if err != nil {
		t.Fatalf("Write(first) error = %v", err)
	}
	_, err = tools.Write(context.Background(), WriteRequest{
		Path:    "nested/note.txt",
		Content: "second\n",
	})
	if err != nil {
		t.Fatalf("Write(second) error = %v", err)
	}

	data := mustReadVirtualFile(t, fsys, "/workspace/nested/note.txt")
	if got := string(data); got != "second\n" {
		t.Fatalf("final file content = %q, want second", got)
	}
}

func TestWriteSerializesConcurrentMutations(t *testing.T) {
	t.Parallel()

	base := gbfs.NewMemory()
	fsys := &serialCheckFS{FileSystem: base}
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := tools.Write(context.Background(), WriteRequest{
				Path:    "shared.txt",
				Content: strings.Repeat(string(rune('a'+i)), 16),
			})
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if got := fsys.MaxActiveWrites(); got != 1 {
		t.Fatalf("MaxActiveWrites() = %d, want 1", got)
	}
}

func TestEditExactReplacementAndDiff(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/note.txt", "hello\nworld\n")
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	resp, err := tools.Edit(context.Background(), EditRequest{
		Path:    "note.txt",
		OldText: "world",
		NewText: "gbash",
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if got := string(mustReadVirtualFile(t, fsys, "/workspace/note.txt")); got != "hello\ngbash\n" {
		t.Fatalf("file content = %q, want edited content", got)
	}
	if resp.Details == nil || !strings.Contains(resp.Details.Diff, "+2 gbash") {
		t.Fatalf("Edit() diff = %#v, want added line", resp.Details)
	}
	if resp.Details.FirstChangedLine != 2 {
		t.Fatalf("FirstChangedLine = %d, want 2", resp.Details.FirstChangedLine)
	}
}

func TestEditFuzzyReplacement(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/note.txt", "smart quote: \u201chello\u201d\n")
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	_, err := tools.Edit(context.Background(), EditRequest{
		Path:    "note.txt",
		OldText: "smart quote: \"hello\"",
		NewText: "smart quote: \"goodbye\"",
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if got := string(mustReadVirtualFile(t, fsys, "/workspace/note.txt")); got != "smart quote: \"goodbye\"\n" {
		t.Fatalf("file content = %q, want normalized fuzzy replacement", got)
	}
}

func TestEditDuplicateMatchError(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/note.txt", "hello\nhello\n")
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	_, err := tools.Edit(context.Background(), EditRequest{
		Path:    "note.txt",
		OldText: "hello",
		NewText: "goodbye",
	})
	if err == nil || !strings.Contains(err.Error(), "must be unique") {
		t.Fatalf("Edit() error = %v, want duplicate-match error", err)
	}
}

func TestEditMissingMatchError(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/note.txt", "hello\n")
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	_, err := tools.Edit(context.Background(), EditRequest{
		Path:    "note.txt",
		OldText: "missing",
		NewText: "goodbye",
	})
	if err == nil || !strings.Contains(err.Error(), "could not find the exact text") {
		t.Fatalf("Edit() error = %v, want missing-match error", err)
	}
}

func TestEditNoOpError(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/note.txt", "hello\n")
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	_, err := tools.Edit(context.Background(), EditRequest{
		Path:    "note.txt",
		OldText: "hello",
		NewText: "hello",
	})
	if err == nil || !strings.Contains(err.Error(), "produced identical content") {
		t.Fatalf("Edit() error = %v, want no-op error", err)
	}
}

func TestEditPreservesBOMAndCRLF(t *testing.T) {
	t.Parallel()

	fsys := gbfs.NewMemory()
	mustWriteVirtualFile(t, fsys, "/workspace/note.txt", "\uFEFFhello\r\nworld\r\n")
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	_, err := tools.Edit(context.Background(), EditRequest{
		Path:    "note.txt",
		OldText: "world",
		NewText: "gbash",
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}
	if got := string(mustReadVirtualFile(t, fsys, "/workspace/note.txt")); got != "\uFEFFhello\r\ngbash\r\n" {
		t.Fatalf("file content = %q, want BOM + CRLF preserved", got)
	}
}

func TestEditAndWriteSerializeSamePath(t *testing.T) {
	t.Parallel()

	base := gbfs.NewMemory()
	mustWriteVirtualFile(t, base, "/workspace/shared.txt", "hello\n")
	fsys := &serialCheckFS{FileSystem: base, started: make(chan struct{}, 4)}
	tools := New(Config{FS: fsys, WorkingDir: "/workspace"})

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := tools.Edit(context.Background(), EditRequest{
			Path:    "shared.txt",
			OldText: "hello",
			NewText: "goodbye",
		})
		errCh <- err
	}()

	select {
	case <-fsys.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for edit to enter write phase")
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := tools.Write(context.Background(), WriteRequest{
			Path:    "shared.txt",
			Content: "final\n",
		})
		errCh <- err
	}()

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("mutation error = %v", err)
		}
	}
	if got := fsys.MaxActiveWrites(); got != 1 {
		t.Fatalf("MaxActiveWrites() = %d, want 1", got)
	}
}

func TestToolsetWorksWithHostBackedFS(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(path.Join(root, "workspace"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path.Join(root, "workspace", "note.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	fsys, err := gbfs.NewReadWrite(gbfs.ReadWriteOptions{Root: root})
	if err != nil {
		t.Fatalf("NewReadWrite() error = %v", err)
	}
	tools := New(Config{FS: fsys, WorkingDir: "/workspace", HomeDir: "/home/agent"})

	readResp, err := tools.Read(context.Background(), ReadRequest{Path: "note.txt"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := readResp.Content[0].Text; got != "hello\nworld\n" {
		t.Fatalf("Read() text = %q, want file contents", got)
	}

	_, err = tools.Edit(context.Background(), EditRequest{
		Path:    "note.txt",
		OldText: "world",
		NewText: "gbash",
	})
	if err != nil {
		t.Fatalf("Edit() error = %v", err)
	}

	_, err = tools.Write(context.Background(), WriteRequest{
		Path:    "nested/out.txt",
		Content: "done\n",
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	data, err := os.ReadFile(path.Join(root, "workspace", "note.txt"))
	if err != nil {
		t.Fatalf("ReadFile(note.txt) error = %v", err)
	}
	if got := string(data); got != "hello\ngbash\n" {
		t.Fatalf("edited host file = %q, want edited content", got)
	}
	data, err = os.ReadFile(path.Join(root, "workspace", "nested", "out.txt"))
	if err != nil {
		t.Fatalf("ReadFile(out.txt) error = %v", err)
	}
	if got := string(data); got != "done\n" {
		t.Fatalf("written host file = %q, want done", got)
	}
}

func mustWriteVirtualFile(tb testing.TB, fsys gbfs.FileSystem, name, content string) {
	tb.Helper()
	mustWriteVirtualBytes(tb, fsys, name, []byte(content))
}

func mustWriteVirtualBytes(tb testing.TB, fsys gbfs.FileSystem, name string, data []byte) {
	tb.Helper()
	if err := fsys.MkdirAll(context.Background(), path.Dir(name), 0o755); err != nil {
		tb.Fatalf("MkdirAll(%q) error = %v", path.Dir(name), err)
	}
	file, err := fsys.OpenFile(context.Background(), name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		tb.Fatalf("OpenFile(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		tb.Fatalf("Write(%q) error = %v", name, err)
	}
}

func mustReadVirtualFile(tb testing.TB, fsys gbfs.FileSystem, name string) []byte {
	tb.Helper()
	file, err := fsys.Open(context.Background(), name)
	if err != nil {
		tb.Fatalf("Open(%q) error = %v", name, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		tb.Fatalf("ReadAll(%q) error = %v", name, err)
	}
	return data
}

type serialCheckFS struct {
	gbfs.FileSystem
	mu      sync.Mutex
	active  int
	max     int
	started chan struct{}
}

func (fsys *serialCheckFS) OpenFile(ctx context.Context, name string, flag int, perm stdfs.FileMode) (gbfs.File, error) {
	file, err := fsys.FileSystem.OpenFile(ctx, name, flag, perm)
	if err != nil {
		return nil, err
	}
	if flag&(os.O_WRONLY|os.O_RDWR) == 0 {
		return file, nil
	}

	fsys.mu.Lock()
	fsys.active++
	if fsys.active > fsys.max {
		fsys.max = fsys.active
	}
	fsys.mu.Unlock()
	if fsys.started != nil {
		select {
		case fsys.started <- struct{}{}:
		default:
		}
	}

	time.Sleep(75 * time.Millisecond)
	return &trackedFile{
		File: file,
		done: func() {
			fsys.mu.Lock()
			fsys.active--
			fsys.mu.Unlock()
		},
	}, nil
}

func (fsys *serialCheckFS) MaxActiveWrites() int {
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	return fsys.max
}

type trackedFile struct {
	gbfs.File
	once sync.Once
	done func()
}

func (f *trackedFile) Close() error {
	err := f.File.Close()
	f.once.Do(func() {
		if f.done != nil {
			f.done()
		}
	})
	return err
}
