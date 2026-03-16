package builtins_test

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

func TestChecksumSumHashesFilesAndStdin(t *testing.T) {
	tests := []struct {
		cmd string
		sum func([]byte) string
	}{
		{cmd: "b2sum", sum: b2Hex},
		{cmd: "md5sum", sum: md5Hex},
		{cmd: "sha1sum", sum: sha1Hex},
		{cmd: "sha224sum", sum: sha224Hex},
		{cmd: "sha256sum", sum: sha256Hex},
		{cmd: "sha384sum", sum: sha384Hex},
		{cmd: "sha512sum", sum: sha512Hex},
	}

	alpha := []byte("alpha\n")
	binary := []byte{0x80, 0x90, 0xa0, 0xb0, 0xff}

	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			session := newSession(t, &Config{})
			writeSessionFile(t, session, "/tmp/a.txt", alpha)
			writeSessionFile(t, session, "/tmp/bin.dat", binary)

			cases := []struct {
				name   string
				script string
				want   string
			}{
				{
					name:   "empty-stdin",
					script: fmt.Sprintf("printf '' | %s\n", tc.cmd),
					want:   fmt.Sprintf("%s  -\n", tc.sum(nil)),
				},
				{
					name:   "explicit-stdin-argument",
					script: fmt.Sprintf("cat /tmp/bin.dat | %s -\n", tc.cmd),
					want:   fmt.Sprintf("%s  -\n", tc.sum(binary)),
				},
				{
					name:   "multiple-files",
					script: fmt.Sprintf("%s /tmp/a.txt /tmp/bin.dat\n", tc.cmd),
					want: fmt.Sprintf(
						"%s  /tmp/a.txt\n%s  /tmp/bin.dat\n",
						tc.sum(alpha),
						tc.sum(binary),
					),
				},
			}

			for _, testCase := range cases {
				t.Run(testCase.name, func(t *testing.T) {
					result := mustExecSession(t, session, testCase.script)
					if result.ExitCode != 0 {
						t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
					}
					if got := result.Stdout; got != testCase.want {
						t.Fatalf("Stdout = %q, want %q", got, testCase.want)
					}
					if result.Stderr != "" {
						t.Fatalf("Stderr = %q, want empty", result.Stderr)
					}
				})
			}
		})
	}
}

func TestChecksumSumOutputModes(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("mode-data")
	writeSessionFile(t, session, "/tmp/input.txt", data)
	sum := md5Hex(data)

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "binary",
			script: "md5sum --binary /tmp/input.txt\n",
			want:   fmt.Sprintf("%s */tmp/input.txt\n", sum),
		},
		{
			name:   "tag",
			script: "md5sum --binary --tag /tmp/input.txt\n",
			want:   fmt.Sprintf("MD5 (/tmp/input.txt) = %s\n", sum),
		},
		{
			name:   "zero",
			script: "md5sum --zero /tmp/input.txt\n",
			want:   fmt.Sprintf("%s  /tmp/input.txt\x00", sum),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestB2SumLengthModes(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("foobar\n")
	writeSessionFile(t, session, "/tmp/input.txt", data)

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "default-length",
			script: "b2sum /tmp/input.txt\n",
			want:   fmt.Sprintf("%s  /tmp/input.txt\n", b2Hex(data)),
		},
		{
			name:   "explicit-128",
			script: "b2sum --length=128 /tmp/input.txt\n",
			want:   fmt.Sprintf("%s  /tmp/input.txt\n", b2HexSized(data, 16)),
		},
		{
			name:   "attached-short-length-tagged",
			script: "b2sum -l128 --tag /tmp/input.txt\n",
			want:   fmt.Sprintf("BLAKE2b-128 (/tmp/input.txt) = %s\n", b2HexSized(data, 16)),
		},
		{
			name:   "zero-means-default",
			script: "b2sum --length=0 --tag /tmp/input.txt\n",
			want:   fmt.Sprintf("BLAKE2b (/tmp/input.txt) = %s\n", b2Hex(data)),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got := result.Stdout; got != tc.want {
				t.Fatalf("Stdout = %q, want %q", got, tc.want)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestChecksumSumCheckModeSupportsShortAndLongFlags(t *testing.T) {
	data := []byte("verify-me")
	sum := strings.ToUpper(md5Hex(data))
	checksums := fmt.Sprintf("%s */tmp/input.txt\n", sum)

	for _, flag := range []string{"-c", "--check"} {
		t.Run(flag, func(t *testing.T) {
			session := newSession(t, &Config{})
			writeSessionFile(t, session, "/tmp/input.txt", data)
			writeSessionFile(t, session, "/tmp/checksums.txt", []byte(checksums))

			result := mustExecSession(t, session, fmt.Sprintf("md5sum %s /tmp/checksums.txt\n", flag))
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got, want := result.Stdout, "/tmp/input.txt: OK\n"; got != want {
				t.Fatalf("Stdout = %q, want %q", got, want)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestChecksumSumCheckModeParsesTaggedAndSingleSpaceFormats(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("format-data")
	writeSessionFile(t, session, "/tmp/file.txt", data)

	checks := []struct {
		name    string
		content string
	}{
		{
			name:    "single-space",
			content: fmt.Sprintf("%s /tmp/file.txt\n", md5Hex(data)),
		},
		{
			name:    "tagged",
			content: fmt.Sprintf("MD5 (/tmp/file.txt) = %s\n", md5Hex(data)),
		},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			writeSessionFile(t, session, "/tmp/checksums.txt", []byte(tc.content))
			result := mustExecSession(t, session, "md5sum --check /tmp/checksums.txt\n")
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got, want := result.Stdout, "/tmp/file.txt: OK\n"; got != want {
				t.Fatalf("Stdout = %q, want %q", got, want)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestChecksumSumCheckModeHandlesCRLFAndEscapedFilenames(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/.\rfoo", nil)
	sum := md5Hex(nil)
	writeSessionFile(t, session, "/tmp/checksums.txt", fmt.Appendf(nil, "\\%s  /tmp/.\\rfoo\r\n", sum))

	result := mustExecSession(t, session, "md5sum --check /tmp/checksums.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "\\/tmp/.\\rfoo: OK\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}
func TestB2SumCheckModeSupportsVariableLengths(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("format-data")
	writeSessionFile(t, session, "/tmp/file.txt", data)

	checks := []struct {
		name    string
		content string
	}{
		{
			name:    "single-space-8-bit",
			content: fmt.Sprintf("%s /tmp/file.txt\n", b2HexSized(data, 1)),
		},
		{
			name:    "untagged-128-bit",
			content: fmt.Sprintf("%s  /tmp/file.txt\n", b2HexSized(data, 16)),
		},
		{
			name:    "tagged-128-bit",
			content: fmt.Sprintf("BLAKE2b-128 (/tmp/file.txt) = %s\n", b2HexSized(data, 16)),
		},
	}

	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			writeSessionFile(t, session, "/tmp/checksums.txt", []byte(tc.content))
			result := mustExecSession(t, session, "b2sum --check /tmp/checksums.txt\n")
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if got, want := result.Stdout, "/tmp/file.txt: OK\n"; got != want {
				t.Fatalf("Stdout = %q, want %q", got, want)
			}
			if result.Stderr != "" {
				t.Fatalf("Stderr = %q, want empty", result.Stderr)
			}
		})
	}
}

func TestB2SumStrictCheckAcceptsOpenSSLTaggedLengthLines(t *testing.T) {
	session := newSession(t, &Config{})

	files := []string{"/tmp/a", "/tmp/ b", "/tmp/*c", "/tmp/44", "/tmp/ "}
	var content strings.Builder
	for _, name := range files {
		data := []byte(name + "\n")
		writeSessionFile(t, session, name, data)
		fmt.Fprintf(&content, "BLAKE2b(%s)= %s\n", name[len("/tmp/"):], b2Hex(data))
		fmt.Fprintf(&content, "BLAKE2b-128(%s)= %s\n", name[len("/tmp/"):], b2HexSized(data, 16))
	}
	writeSessionFile(t, session, "/tmp/openssl.b2sum", []byte(content.String()))

	result := mustExecSession(t, session, "cd /tmp\nb2sum --strict -c openssl.b2sum\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if got, want := result.Stdout, "a: OK\na: OK\n b: OK\n b: OK\n*c: OK\n*c: OK\n44: OK\n44: OK\n : OK\n : OK\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}
func TestChecksumSumCheckModeResolvesTargetsAgainstWorkingDirectory(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("cwd-target")
	writeSessionFile(t, session, "/tmp/input.txt", data)
	writeSessionFile(t, session, "/tmp/sub/checksums.txt", fmt.Appendf(nil, "%s  input.txt\n", md5Hex(data)))

	result, err := session.Exec(context.Background(), &ExecutionRequest{
		Script:  "md5sum -c /tmp/sub/checksums.txt\n",
		WorkDir: "/tmp",
	})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "input.txt: OK\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}
}

func TestChecksumSumCheckModeVerbosityAndStrictFlags(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/f.txt", []byte{})
	writeSessionFile(t, session, "/tmp/ok.md5", []byte("d41d8cd98f00b204e9800998ecf8427e  /tmp/f.txt\n"))
	writeSessionFile(t, session, "/tmp/mixed.md5", []byte("d41d8cd98f00b204e9800998ecf8427e  /tmp/f.txt\ninvalid\n"))
	writeSessionFile(t, session, "/tmp/fail.md5", []byte("ffffffffffffffffffffffffffffffff  /tmp/f.txt\n"))

	quiet := mustExecSession(t, session, "md5sum --quiet --check /tmp/ok.md5\n")
	if quiet.ExitCode != 0 || quiet.Stdout != "" || quiet.Stderr != "" {
		t.Fatalf("quiet result = %+v, want silent success", quiet)
	}

	warn := mustExecSession(t, session, "md5sum --warn --check /tmp/mixed.md5\n")
	if warn.ExitCode != 0 {
		t.Fatalf("warn ExitCode = %d, want 0; stderr=%q", warn.ExitCode, warn.Stderr)
	}
	if got, want := warn.Stdout, "/tmp/f.txt: OK\n"; got != want {
		t.Fatalf("warn Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(warn.Stderr, "improperly formatted MD5 checksum line") {
		t.Fatalf("warn Stderr = %q, want per-line warning", warn.Stderr)
	}
	if !strings.Contains(warn.Stderr, "WARNING: 1 line is improperly formatted") {
		t.Fatalf("warn Stderr = %q, want summary warning", warn.Stderr)
	}

	strict := mustExecSession(t, session, "md5sum --strict --check /tmp/mixed.md5\n")
	if strict.ExitCode != 1 {
		t.Fatalf("strict ExitCode = %d, want 1", strict.ExitCode)
	}
	if got, want := strict.Stdout, "/tmp/f.txt: OK\n"; got != want {
		t.Fatalf("strict Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(strict.Stderr, "WARNING: 1 line is improperly formatted") {
		t.Fatalf("strict Stderr = %q, want summary warning", strict.Stderr)
	}

	status := mustExecSession(t, session, "md5sum --status --check /tmp/fail.md5\n")
	if status.ExitCode != 1 {
		t.Fatalf("status ExitCode = %d, want 1", status.ExitCode)
	}
	if status.Stdout != "" || status.Stderr != "" {
		t.Fatalf("status output = stdout %q stderr %q, want empty", status.Stdout, status.Stderr)
	}
}

func TestChecksumSumCheckModeIgnoreMissing(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/f.txt", []byte("foobar\n"))
	writeSessionFile(t, session, "/tmp/checksums.txt", []byte(strings.Join([]string{
		fmt.Sprintf("%s  /tmp/f.txt", md5Hex([]byte("foobar\n"))),
		fmt.Sprintf("%s  /tmp/missing.txt", md5Hex([]byte("missing"))),
		"",
	}, "\n")))

	result := mustExecSession(t, session, "md5sum --check --ignore-missing /tmp/checksums.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "/tmp/f.txt: OK\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty", result.Stderr)
	}

	writeSessionFile(t, session, "/tmp/only-missing.md5", fmt.Appendf(nil, "%s  /tmp/missing.txt\n", md5Hex([]byte("missing"))))
	onlyMissing := mustExecSession(t, session, "md5sum --check --ignore-missing /tmp/only-missing.md5\n")
	if onlyMissing.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", onlyMissing.ExitCode)
	}
	if onlyMissing.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", onlyMissing.Stdout)
	}
	if !strings.Contains(onlyMissing.Stderr, "no file was verified") {
		t.Fatalf("Stderr = %q, want no-file-verified message", onlyMissing.Stderr)
	}
}

func TestChecksumSumReportsMissingInputsToStderrAndContinues(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("present")
	writeSessionFile(t, session, "/tmp/present.txt", data)

	result := mustExecSession(t, session, "md5sum /tmp/missing.txt /tmp/present.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stdout, fmt.Sprintf("%s  /tmp/present.txt\n", md5Hex(data)); got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "md5sum: /tmp/missing.txt: No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestChecksumSumMissingChecksumListFailsOnStderr(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "md5sum -c /tmp/missing.txt\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if result.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", result.Stdout)
	}
	if got, want := result.Stderr, "md5sum: /tmp/missing.txt: No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestChecksumSumRejectsMeaninglessAndUnknownOptions(t *testing.T) {
	tests := []struct {
		name   string
		script string
		stderr string
	}{
		{
			name:   "ignore-missing-without-check",
			script: "md5sum --ignore-missing\n",
			stderr: "md5sum: the --ignore-missing option is meaningful only when verifying checksums\nTry 'md5sum --help' for more information.\n",
		},
		{
			name:   "quiet-without-check",
			script: "md5sum --quiet\n",
			stderr: "md5sum: the --quiet option is meaningful only when verifying checksums\nTry 'md5sum --help' for more information.\n",
		},
		{
			name:   "tag-with-text",
			script: "md5sum --tag --text /tmp/file.txt\n",
			stderr: "md5sum: --tag does not support --text mode\n",
		},
		{
			name:   "tag-with-check",
			script: "md5sum --tag --check /tmp/file.txt\n",
			stderr: "md5sum: the --tag option is meaningless when verifying checksums\n",
		},
		{
			name:   "binary-with-check",
			script: "md5sum --binary --check /tmp/file.txt\n",
			stderr: "md5sum: the --binary and --text options are meaningless when verifying checksums\n",
		},
		{
			name:   "unknown-long",
			script: "md5sum --bogus\n",
			stderr: "md5sum: unrecognized option '--bogus'\nTry 'md5sum --help' for more information.\n",
		},
		{
			name:   "unknown-short",
			script: "md5sum -q\n",
			stderr: "md5sum: invalid option -- 'q'\nTry 'md5sum --help' for more information.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			session := newSession(t, &Config{})
			writeSessionFile(t, session, "/tmp/file.txt", []byte("x"))
			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != 1 {
				t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
			}
			if result.Stdout != "" {
				t.Fatalf("Stdout = %q, want empty", result.Stdout)
			}
			if got := result.Stderr; got != tc.stderr {
				t.Fatalf("Stderr = %q, want %q", got, tc.stderr)
			}
		})
	}
}

func TestB2SumRejectsInvalidLengths(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/file.txt", []byte("foobar\n"))

	tests := []struct {
		name   string
		script string
		stderr string
	}{
		{
			name:   "not-multiple-of-8",
			script: "b2sum --length=9 /tmp/file.txt\n",
			stderr: "b2sum: invalid length: '9'\nb2sum: length is not a multiple of 8\n",
		},
		{
			name:   "too-large",
			script: "b2sum --length=513 /tmp/file.txt\n",
			stderr: "b2sum: invalid length: '513'\nb2sum: maximum digest length for 'BLAKE2b' is 512 bits\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != 1 {
				t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
			}
			if result.Stdout != "" {
				t.Fatalf("Stdout = %q, want empty", result.Stdout)
			}
			if got := result.Stderr; got != tc.stderr {
				t.Fatalf("Stderr = %q, want %q", got, tc.stderr)
			}
		})
	}
}

func TestChecksumSumHelpAndVersion(t *testing.T) {
	session := newSession(t, &Config{})

	help := mustExecSession(t, session, "sha1sum --help\n")
	if help.ExitCode != 0 {
		t.Fatalf("help ExitCode = %d, want 0; stderr=%q", help.ExitCode, help.Stderr)
	}
	if !strings.Contains(help.Stdout, "sha1sum - compute or check SHA1 message digests") {
		t.Fatalf("help Stdout = %q, want header", help.Stdout)
	}
	if help.Stderr != "" {
		t.Fatalf("help Stderr = %q, want empty", help.Stderr)
	}

	version := mustExecSession(t, session, "sha256sum --version\n")
	if version.ExitCode != 0 {
		t.Fatalf("version ExitCode = %d, want 0; stderr=%q", version.ExitCode, version.Stderr)
	}
	if got, want := version.Stdout, "sha256sum (gbash)\n"; got != want {
		t.Fatalf("version Stdout = %q, want %q", got, want)
	}
	if version.Stderr != "" {
		t.Fatalf("version Stderr = %q, want empty", version.Stderr)
	}
}

func b2Hex(data []byte) string {
	return b2HexSized(data, blake2b.Size)
}

func b2HexSized(data []byte, size int) string {
	h, err := blake2b.New(size, nil)
	if err != nil {
		panic(err)
	}
	_, _ = h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return fmt.Sprintf("%x", sum)
}

func sha1Hex(data []byte) string {
	sum := sha1.Sum(data)
	return fmt.Sprintf("%x", sum)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func sha224Hex(data []byte) string {
	sum := sha256.Sum224(data)
	return fmt.Sprintf("%x", sum)
}

func sha384Hex(data []byte) string {
	sum := sha512.Sum384(data)
	return fmt.Sprintf("%x", sum)
}

func sha512Hex(data []byte) string {
	sum := sha512.Sum512(data)
	return fmt.Sprintf("%x", sum)
}
