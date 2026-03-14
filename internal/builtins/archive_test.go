package builtins_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io/fs"
	"strings"
	"testing"
)

func TestGzipRoundTripReplacesSourceByDefault(t *testing.T) {
	session := newSession(t, nil)
	writeSessionFile(t, session, "/tmp/note.txt", []byte("hello from gzip\n"))

	compress := mustExecSession(t, session, "gzip /tmp/note.txt\n")
	if compress.ExitCode != 0 {
		t.Fatalf("compress ExitCode = %d, want 0 (stderr=%q)", compress.ExitCode, compress.Stderr)
	}
	if _, err := session.FileSystem().Open(context.Background(), "/tmp/note.txt"); err == nil {
		t.Fatalf("Open(/tmp/note.txt) succeeded, want source removed after gzip")
	}
	if got := string(readSessionFile(t, session, "/tmp/note.txt.gz")); got == "" {
		t.Fatalf("compressed output empty, want gzip bytes")
	}

	decompress := mustExecSession(t, session, "gunzip /tmp/note.txt.gz\n")
	if decompress.ExitCode != 0 {
		t.Fatalf("decompress ExitCode = %d, want 0 (stderr=%q)", decompress.ExitCode, decompress.Stderr)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/note.txt")), "hello from gzip\n"; got != want {
		t.Fatalf("restored file = %q, want %q", got, want)
	}
}

func TestGzipSupportsStdoutBinaryRoundTripAndZCat(t *testing.T) {
	session := newSession(t, nil)
	input := []byte{0x00, 0x01, 0x02, 0xff, 'h', 'i', '\n'}
	writeSessionFile(t, session, "/tmp/blob.bin", input)

	compress := mustExecSession(t, session, "gzip -c /tmp/blob.bin > /tmp/blob.bin.gz\n")
	if compress.ExitCode != 0 {
		t.Fatalf("compress ExitCode = %d, want 0 (stderr=%q)", compress.ExitCode, compress.Stderr)
	}

	decompress := mustExecSession(t, session, "zcat /tmp/blob.bin.gz > /tmp/blob.out\n")
	if decompress.ExitCode != 0 {
		t.Fatalf("zcat ExitCode = %d, want 0 (stderr=%q)", decompress.ExitCode, decompress.Stderr)
	}
	if got := readSessionFile(t, session, "/tmp/blob.out"); !bytes.Equal(got, input) {
		t.Fatalf("zcat output = %v, want %v", got, input)
	}
}

func TestGzipKeepCustomSuffixAndTestMode(t *testing.T) {
	session := newSession(t, nil)
	writeSessionFile(t, session, "/tmp/keep.txt", []byte("keep me\n"))

	compress := mustExecSession(t, session, "gzip -k -S .tgz /tmp/keep.txt\n")
	if compress.ExitCode != 0 {
		t.Fatalf("compress ExitCode = %d, want 0 (stderr=%q)", compress.ExitCode, compress.Stderr)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/keep.txt")), "keep me\n"; got != want {
		t.Fatalf("source after -k = %q, want %q", got, want)
	}
	if _, err := session.FileSystem().Open(context.Background(), "/tmp/keep.txt.tgz"); err != nil {
		t.Fatalf("Open(/tmp/keep.txt.tgz) error = %v, want compressed output", err)
	}

	testResult := mustExecSession(t, session, "gunzip -t -S .tgz /tmp/keep.txt.tgz\n")
	if testResult.ExitCode != 0 {
		t.Fatalf("gunzip -t ExitCode = %d, want 0 (stderr=%q)", testResult.ExitCode, testResult.Stderr)
	}
}

func TestGunzipRejectsInvalidInput(t *testing.T) {
	session := newSession(t, nil)
	writeSessionFile(t, session, "/tmp/plain.gz", []byte("not actually gzip\n"))

	result := mustExecSession(t, session, "gunzip -t /tmp/plain.gz\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = 0, want non-zero for invalid gzip input")
	}
	if !strings.Contains(result.Stderr, "gzip:") {
		t.Fatalf("Stderr = %q, want gzip-prefixed error", result.Stderr)
	}
}

func TestGzipSupportsLongFlagsAndCustomHelp(t *testing.T) {
	session := newSession(t, nil)
	writeSessionFile(t, session, "/tmp/long.txt", []byte("long flag data\n"))

	help := mustExecSession(t, session, "gzip --help\nzcat --help\n")
	if help.ExitCode != 0 {
		t.Fatalf("help ExitCode = %d, want 0 (stderr=%q)", help.ExitCode, help.Stderr)
	}
	if !strings.Contains(help.Stdout, "gzip - gzip-compatible compression inside the gbash sandbox") {
		t.Fatalf("gzip help = %q, want custom help text", help.Stdout)
	}
	if !strings.Contains(help.Stdout, "zcat - gzip-compatible compression inside the gbash sandbox") {
		t.Fatalf("zcat help = %q, want custom help text", help.Stdout)
	}

	compress := mustExecSession(t, session, "gzip --keep --suffix=.tgz /tmp/long.txt\n")
	if compress.ExitCode != 0 {
		t.Fatalf("compress ExitCode = %d, want 0 (stderr=%q)", compress.ExitCode, compress.Stderr)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/long.txt")), "long flag data\n"; got != want {
		t.Fatalf("source after --keep = %q, want %q", got, want)
	}

	decompress := mustExecSession(t, session, "gunzip --stdout --suffix=.tgz /tmp/long.txt.tgz > /tmp/long.out\n")
	if decompress.ExitCode != 0 {
		t.Fatalf("decompress ExitCode = %d, want 0 (stderr=%q)", decompress.ExitCode, decompress.Stderr)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/long.out")), "long flag data\n"; got != want {
		t.Fatalf("decompressed output = %q, want %q", got, want)
	}
}

func TestTarCreateListExtractAndRoundTripSymlink(t *testing.T) {
	session := newSession(t, nil)
	writeSessionFile(t, session, "/tmp/src/file.txt", []byte("archive me\n"))
	if err := session.FileSystem().Symlink(context.Background(), "file.txt", "/tmp/src/link.txt"); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	create := mustExecSession(t, session, "tar -cf /tmp/archive.tar /tmp/src\n")
	if create.ExitCode != 0 {
		t.Fatalf("create ExitCode = %d, want 0 (stderr=%q)", create.ExitCode, create.Stderr)
	}

	list := mustExecSession(t, session, "tar -tf /tmp/archive.tar\n")
	if list.ExitCode != 0 {
		t.Fatalf("list ExitCode = %d, want 0 (stderr=%q)", list.ExitCode, list.Stderr)
	}
	for _, want := range []string{"tmp/src", "tmp/src/file.txt", "tmp/src/link.txt"} {
		if !strings.Contains(list.Stdout, want) {
			t.Fatalf("list stdout = %q, want %q", list.Stdout, want)
		}
	}

	extract := mustExecSession(t, session, "mkdir -p /tmp/out && tar -xf /tmp/archive.tar -C /tmp/out\n")
	if extract.ExitCode != 0 {
		t.Fatalf("extract ExitCode = %d, want 0 (stderr=%q)", extract.ExitCode, extract.Stderr)
	}
	if got, want := string(readSessionFile(t, session, "/tmp/out/tmp/src/file.txt")), "archive me\n"; got != want {
		t.Fatalf("extracted file = %q, want %q", got, want)
	}
	target, err := session.FileSystem().Readlink(context.Background(), "/tmp/out/tmp/src/link.txt")
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}
	if got, want := target, "file.txt"; got != want {
		t.Fatalf("symlink target = %q, want %q", got, want)
	}
}

func TestTarSupportsGzipAndStdoutExtraction(t *testing.T) {
	session := newSession(t, nil)
	writeSessionFile(t, session, "/tmp/one.txt", []byte("hello tar gzip\n"))

	create := mustExecSession(t, session, "tar -czf /tmp/one.tar.gz /tmp/one.txt\n")
	if create.ExitCode != 0 {
		t.Fatalf("create ExitCode = %d, want 0 (stderr=%q)", create.ExitCode, create.Stderr)
	}

	extract := mustExecSession(t, session, "tar -xOzf /tmp/one.tar.gz\n")
	if extract.ExitCode != 0 {
		t.Fatalf("extract ExitCode = %d, want 0 (stderr=%q)", extract.ExitCode, extract.Stderr)
	}
	if got, want := extract.Stdout, "hello tar gzip\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestTarKeepOldFilesRejectsOverwrite(t *testing.T) {
	session := newSession(t, nil)
	writeSessionFile(t, session, "/tmp/original.txt", []byte("new content\n"))
	create := mustExecSession(t, session, "tar -cf /tmp/original.tar /tmp/original.txt\n")
	if create.ExitCode != 0 {
		t.Fatalf("create ExitCode = %d, want 0 (stderr=%q)", create.ExitCode, create.Stderr)
	}
	writeSessionFile(t, session, "/tmp/out/tmp/original.txt", []byte("existing\n"))

	extract := mustExecSession(t, session, "tar -xkf /tmp/original.tar -C /tmp/out\n")
	if extract.ExitCode == 0 {
		t.Fatalf("extract ExitCode = 0, want non-zero for -k overwrite protection")
	}
	if got, want := string(readSessionFile(t, session, "/tmp/out/tmp/original.txt")), "existing\n"; got != want {
		t.Fatalf("existing file after -k = %q, want %q", got, want)
	}
}

func TestTarRejectsParentTraversalOnExtract(t *testing.T) {
	session := newSession(t, nil)
	writeSessionFile(t, session, "/tmp/evil.tar", buildTarFixture(t, tarFixtureEntry{
		Name: "safe/../../escape.txt",
		Body: []byte("owned\n"),
	}))

	result := mustExecSession(t, session, "mkdir -p /tmp/out && tar -xf /tmp/evil.tar -C /tmp/out\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = 0, want extract rejection for parent traversal")
	}
	if !strings.Contains(result.Stderr, "unsafe archive path") {
		t.Fatalf("Stderr = %q, want unsafe archive path error", result.Stderr)
	}
}

func TestTarRejectsUnsafeSymlinkTargetsOnExtract(t *testing.T) {
	session := newSession(t, nil)
	writeSessionFile(t, session, "/tmp/evil-link.tar", buildTarFixture(t, tarFixtureEntry{
		Name:     "safe/link.txt",
		Typeflag: tar.TypeSymlink,
		Linkname: "../../escape.txt",
	}))

	result := mustExecSession(t, session, "mkdir -p /tmp/out && tar -xf /tmp/evil-link.tar -C /tmp/out\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = 0, want extract rejection for unsafe symlink target")
	}
	if !strings.Contains(result.Stderr, "unsafe symlink target") {
		t.Fatalf("Stderr = %q, want unsafe symlink target error", result.Stderr)
	}
}

type tarFixtureEntry struct {
	Name     string
	Body     []byte
	Typeflag byte
	Linkname string
}

func buildTarFixture(t *testing.T, entries ...tarFixtureEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, entry := range entries {
		typeflag := entry.Typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     entry.Name,
			Typeflag: typeflag,
			Linkname: entry.Linkname,
			Mode:     0o644,
		}
		switch typeflag {
		case tar.TypeReg, 0:
			header.Size = int64(len(entry.Body))
		case tar.TypeSymlink:
			header.Mode = 0o777
		case tar.TypeDir:
			header.Mode = 0o755
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader(%q) error = %v", entry.Name, err)
		}
		if typeflag == tar.TypeReg || typeflag == 0 {
			if _, err := tw.Write(entry.Body); err != nil {
				t.Fatalf("Write(%q) error = %v", entry.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return buf.Bytes()
}

func buildTarGzipFixture(t *testing.T, entries ...tarFixtureEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, entry := range entries {
		typeflag := entry.Typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     entry.Name,
			Typeflag: typeflag,
			Linkname: entry.Linkname,
			Mode:     0o644,
		}
		if typeflag == tar.TypeReg || typeflag == 0 {
			header.Size = int64(len(entry.Body))
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader(%q) error = %v", entry.Name, err)
		}
		if typeflag == tar.TypeReg || typeflag == 0 {
			if _, err := tw.Write(entry.Body); err != nil {
				t.Fatalf("Write(%q) error = %v", entry.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close() error = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip.Close() error = %v", err)
	}
	return buf.Bytes()
}

func buildGzipFixture(t *testing.T, data []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("gzip.Write() error = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip.Close() error = %v", err)
	}
	return buf.Bytes()
}

func TestTarRejectsTraversalFromGzipArchiveToo(t *testing.T) {
	session := newSession(t, nil)
	writeSessionFile(t, session, "/tmp/evil.tar.gz", buildTarGzipFixture(t, tarFixtureEntry{
		Name: "/../../escape.txt",
		Body: []byte("owned\n"),
	}))

	result := mustExecSession(t, session, "mkdir -p /tmp/out && tar -xzf /tmp/evil.tar.gz -C /tmp/out\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = 0, want extract rejection for gzipped traversal archive")
	}
	if !strings.Contains(result.Stderr, "unsafe archive path") {
		t.Fatalf("Stderr = %q, want unsafe archive path error", result.Stderr)
	}
	if _, err := session.FileSystem().Open(context.Background(), "/tmp/out/escape.txt"); err == nil || !strings.Contains(err.Error(), fs.ErrNotExist.Error()) {
		t.Fatalf("Open(/tmp/out/escape.txt) error = %v, want file to be absent", err)
	}
}
