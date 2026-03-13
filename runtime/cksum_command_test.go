package runtime

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"testing"

	sm3hash "github.com/emmansun/gmsm/sm3"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/sha3"
)

func TestCksumDefaultCRCMatchesKnownValues(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/tmp/empty.txt", nil)
	writeSessionFile(t, session, "/tmp/chars.txt", []byte("123456789"))

	tests := []struct {
		script string
		want   string
	}{
		{"cksum /tmp/empty.txt\n", "4294967295 0 /tmp/empty.txt\n"},
		{"cksum /tmp/chars.txt\n", "930766865 9 /tmp/chars.txt\n"},
		{"printf '' | cksum\n", "4294967295 0\n"},
	}

	for _, tc := range tests {
		result := mustExecSession(t, session, tc.script)
		if result.ExitCode != 0 {
			t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
		}
		if result.Stdout != tc.want {
			t.Fatalf("Stdout = %q, want %q", result.Stdout, tc.want)
		}
	}
}

func TestCksumModernOutputModesAndLengths(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("mode-data")
	writeSessionFile(t, session, "/tmp/input.txt", data)

	sm3sum := sm3hash.Sum(data)
	shake128 := sha3.NewShake128()
	_, _ = shake128.Write(data)
	shakeOut := make([]byte, 20)
	_, _ = shake128.Read(shakeOut)
	shakeOut[len(shakeOut)-1] &= 0x0f

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{"md5-tagged", "cksum -a md5 /tmp/input.txt\n", fmt.Sprintf("MD5 (/tmp/input.txt) = %s\n", md5Hex(data))},
		{"md5-untagged-binary", "cksum -a md5 --untagged --binary /tmp/input.txt\n", fmt.Sprintf("%s */tmp/input.txt\n", md5Hex(data))},
		{"md5-base64", "cksum -a md5 --base64 /tmp/input.txt\n", fmt.Sprintf("MD5 (/tmp/input.txt) = %s\n", base64.StdEncoding.EncodeToString(mustHexDecode(t, md5Hex(data))))},
		{"sm3", "cksum -a sm3 /tmp/input.txt\n", fmt.Sprintf("SM3 (/tmp/input.txt) = %x\n", sm3sum)},
		{"sha2-256", "cksum -a sha2 -l 256 /tmp/input.txt\n", fmt.Sprintf("SHA256 (/tmp/input.txt) = %s\n", sha256Hex(data))},
		{"blake2b-128", "cksum -a blake2b -l 128 /tmp/input.txt\n", fmt.Sprintf("BLAKE2b-128 (/tmp/input.txt) = %s\n", b2HexSized(data, 16))},
		{"shake128-156", "cksum -a shake128 -l 156 /tmp/input.txt\n", fmt.Sprintf("SHAKE128 (/tmp/input.txt) = %x\n", shakeOut)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := mustExecSession(t, session, tc.script)
			if result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if result.Stdout != tc.want {
				t.Fatalf("Stdout = %q, want %q", result.Stdout, tc.want)
			}
		})
	}
}

func TestCksumCheckModeAndValidation(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("verify-me")
	writeSessionFile(t, session, "/tmp/input.txt", data)
	writeSessionFile(t, session, "/tmp/md5.sum", fmt.Appendf(nil, "MD5 (/tmp/input.txt) = %s\n", md5Hex(data)))
	writeSessionFile(t, session, "/tmp/sha256.sum", fmt.Appendf(nil, "%s  /tmp/input.txt\n", sha256Hex(data)))
	writeSessionFile(t, session, "/tmp/sha256b64.sum", fmt.Appendf(nil, "SHA256 (/tmp/input.txt) = %s\n", base64.StdEncoding.EncodeToString(mustHexDecode(t, sha256Hex(data)))))

	tests := []struct {
		name   string
		script string
		stdout string
		stderr string
		ok     bool
	}{
		{"tagged", "cksum --check /tmp/md5.sum\n", "/tmp/input.txt: OK\n", "", true},
		{"untagged-cli-algo", "cksum --algorithm=sha256 --check /tmp/sha256.sum\n", "/tmp/input.txt: OK\n", "", true},
		{"base64", "cksum --check /tmp/sha256b64.sum\n", "/tmp/input.txt: OK\n", "", true},
		{"text-without-untagged", "cksum --algorithm=md5 --text /tmp/input.txt\n", "", "cksum: --text mode is only supported with --untagged\n", false},
		{"legacy-check", "cksum --algorithm=crc --check /tmp/input.txt\n", "", "cksum: --check is not supported with --algorithm={bsd,sysv,crc,crc32b}\n", false},
		{"sha2-missing-length", "cksum --algorithm=sha2 /tmp/input.txt\n", "", "cksum: --algorithm=sha2 requires specifying --length 224, 256, 384, or 512\n", false},
		{"raw-multiple", "cksum --algorithm=md5 --raw /tmp/input.txt /tmp/input.txt\n", "", "cksum: the --raw option is not supported with multiple files\n", false},
		{"blake2b-length", "cksum --algorithm=blake2b --length=513 /tmp/input.txt\n", "", "cksum: invalid length: '513'\ncksum: maximum digest length for 'BLAKE2b' is 512 bits\n", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := mustExecSession(t, session, tc.script)
			if tc.ok && result.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
			}
			if !tc.ok && result.ExitCode == 0 {
				t.Fatalf("ExitCode = 0, want failure")
			}
			if result.Stdout != tc.stdout {
				t.Fatalf("Stdout = %q, want %q", result.Stdout, tc.stdout)
			}
			if result.Stderr != tc.stderr {
				t.Fatalf("Stderr = %q, want %q", result.Stderr, tc.stderr)
			}
		})
	}
}

func TestCksumZeroAndRawModes(t *testing.T) {
	session := newSession(t, &Config{})
	data := []byte("zero-raw")
	writeSessionFile(t, session, "/tmp/input.txt", data)

	h, err := blake2b.New(16, nil)
	if err != nil {
		t.Fatalf("blake2b.New: %v", err)
	}
	_, _ = h.Write(data)

	zero := mustExecSession(t, session, "cksum -a md5 --untagged --zero /tmp/input.txt\n")
	if zero.ExitCode != 0 {
		t.Fatalf("zero ExitCode = %d, want 0; stderr=%q", zero.ExitCode, zero.Stderr)
	}
	if got, want := zero.Stdout, fmt.Sprintf("%s  /tmp/input.txt\x00", md5Hex(data)); got != want {
		t.Fatalf("zero Stdout = %q, want %q", got, want)
	}

	raw := mustExecSession(t, session, "cksum -a blake2b -l 128 --raw /tmp/input.txt\n")
	if raw.ExitCode != 0 {
		t.Fatalf("raw ExitCode = %d, want 0; stderr=%q", raw.ExitCode, raw.Stderr)
	}
	if raw.Stdout != string(h.Sum(nil)) {
		t.Fatalf("raw Stdout mismatch: got %x want %x", raw.Stdout, h.Sum(nil))
	}
}

func mustHexDecode(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("hex.DecodeString(%q): %v", value, err)
	}
	return raw
}
