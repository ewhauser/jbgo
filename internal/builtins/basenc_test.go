package builtins_test

import (
	"context"
	"testing"
)

func runBasencScript(t *testing.T, script string) *ExecutionResult {
	t.Helper()

	rt := newRuntime(t, &Config{})
	result, err := rt.Run(context.Background(), &ExecutionRequest{Script: script})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return result
}

func TestBasencSupportsFileOperandBeforeOptions(t *testing.T) {
	result := runBasencScript(t, "printf 'foo' >/tmp/input\nbasenc /tmp/input --base64 --wrap=0\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "Zm9v"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBasencSupportsWrap(t *testing.T) {
	result := runBasencScript(t, "printf 'The quick brown fox jumps over the lazy dog.' | basenc --base64 -w 20\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "VGhlIHF1aWNrIGJyb3du\nIGZveCBqdW1wcyBvdmVy\nIHRoZSBsYXp5IGRvZy4=\n"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func TestBasencBase64URLDecodeSupportsIgnoreGarbageInference(t *testing.T) {
	result := runBasencScript(t, "printf '@dG8-YmU_\\n' | basenc --base64url -d --ignore\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "to>be?"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBasencBase32AutopadsShortQuantum(t *testing.T) {
	result := runBasencScript(t, "printf 'MFRGG' | basenc --base32 --decode\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "abc"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBasencBase32KeepsDecodedPrefixOnInvalidTail(t *testing.T) {
	result := runBasencScript(t, "printf 'MFRGGZDF=' | basenc --base32 --decode\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stdout, "abcde"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "basenc: error: invalid input\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBasencBase32HexSupportsEncodeAndTruncatedDecode(t *testing.T) {
	result := runBasencScript(t, "printf 'nice>base?' | basenc --base32hex --wrap=0\nprintf '\\n'\nprintf 'CPNMUO' | basenc --base32hex -d\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stdout, "DPKM6P9UC9GN6P9V\nfoo"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "basenc: error: invalid input\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBasencBase16DecodeAcceptsLowercase(t *testing.T) {
	result := runBasencScript(t, "printf '48656c6c6f2c20576f726c6421' | basenc --base16 -d\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "Hello, World!"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBasencBase2Encodings(t *testing.T) {
	script := "" +
		"printf 'msbf' | basenc --base2msbf --wrap=0\n" +
		"printf '\\n'\n" +
		"printf '01101101011100110110001001100110' | basenc --base2msbf -d\n" +
		"printf '\\n'\n" +
		"printf 'lsbf' | basenc --base2lsbf --wrap=0\n" +
		"printf '\\n'\n" +
		"printf '00110110110011100100011001100110' | basenc --base2lsbf -d\n"
	result := runBasencScript(t, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "01101101011100110110001001100110\nmsbf\n00110110110011100100011001100110\nlsbf"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func TestBasencZ85RoundTripAndLengthChecks(t *testing.T) {
	result := runBasencScript(t, "printf 'Hello, World' | basenc --z85 --wrap=0\nprintf '\\n'\nprintf 'nm=QNz.92jz/PV8' | basenc --z85 -d\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "nm=QNz.92jz/PV8\nHello, World"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}

	result = runBasencScript(t, "printf '123' | basenc --z85\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stderr, "basenc: error: invalid input (length must be multiple of 4 characters)\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBasencBase58AndLastEncodingWins(t *testing.T) {
	script := "" +
		"printf 'Hello!' | basenc --base64 --base32 --base16 --z85 --base58 --wrap=0\n" +
		"printf '\\n'\n" +
		"printf '72k1xXWG59fYdzSNoA' | basenc --base58 -d\n"
	result := runBasencScript(t, script)
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "d3yC1LKr\nHello, World!"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestBasencReportsDirectoryReadErrors(t *testing.T) {
	result := runBasencScript(t, "basenc --base32 .\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if got, want := result.Stderr, "basenc: read error: Is a directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestBasencRequiresAnEncoding(t *testing.T) {
	result := runBasencScript(t, "printf 'foo' | basenc\n")
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	want := "basenc: missing encoding type\nTry 'basenc --help' for more information.\n"
	if result.Stderr != want {
		t.Fatalf("Stderr = %q, want %q", result.Stderr, want)
	}
}
