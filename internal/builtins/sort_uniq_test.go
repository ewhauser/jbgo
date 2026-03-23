package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestSortSupportsKeySortingWithCustomDelimiter(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo zebra,10 > /tmp/in.csv\n echo alpha,2 >> /tmp/in.csv\n echo beta,1 >> /tmp/in.csv\n sort -t, -k2,2n /tmp/in.csv\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "beta,1\nalpha,2\nzebra,10\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsNumericReverseUniquePipeline(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo 10 > /tmp/in.txt\n echo 2 >> /tmp/in.txt\n echo 2 >> /tmp/in.txt\n echo 1 >> /tmp/in.txt\n cat /tmp/in.txt | sort -nru\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "10\n2\n1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsCaseInsensitiveUnique(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo Apple > /tmp/in.txt\n echo apple >> /tmp/in.txt\n echo Banana >> /tmp/in.txt\n echo banana >> /tmp/in.txt\n sort -fu /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "Apple\nBanana\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortReturnsErrorForMissingFile(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "sort /missing.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "/missing.txt") {
		t.Fatalf("Stderr = %q, want missing-file error", result.Stderr)
	}
}

func TestSortSupportsCompactKeyAndGeneralNumericFlags(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a 1e2\\nb 2e1\\n' > /tmp/in.txt\nsort -gk2,2 /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "b 2e1\na 1e2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsQuietCheckFlag(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\nc\\nb\\n' | sort -C\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestSortSupportsZeroTerminatedRecords(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'b\\000a\\000' | sort -z\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\x00b\x00"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsFiles0FromStdin(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'b\\n' > /tmp/b\nprintf 'a\\n' > /tmp/a\nprintf '/tmp/b\\000/tmp/a\\000' | sort --files0-from=-\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsMergeVersionAndResourceFlags(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'pkg-1.2\\npkg-1.10\\n' > /tmp/a\nprintf 'pkg-2\\npkg-10\\n' > /tmp/b\nsort -m --sort=version --parallel=2 --batch-size=2 -S 1M -T /tmp /tmp/a /tmp/b\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "pkg-1.2\npkg-1.10\npkg-2\npkg-10\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsDebugAndCompressProgramFlags(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'b\\na\\n' > /tmp/in.txt\nsort --compress-program=cat --debug /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\n_\n_\nb\n_\n_\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "text ordering performed using simple byte comparison") {
		t.Fatalf("Stderr = %q, want debug output", result.Stderr)
	}
}

func TestSortCompressProgramIsIgnoredWithoutTemporaryFiles(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'b\\na\\n' > /tmp/in.txt\nsort --compress-program=does-not-exist /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsIgnoreNonprintingFlag(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '\\001b\\nb\\na\\n' > /tmp/in.txt\nsort -iu /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\n\x01b\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortSupportsVersionFlag(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "sort --version\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "sort (gbash)") {
		t.Fatalf("Stdout = %q, want version banner", result.Stdout)
	}
}

func TestSortRandomSortUsesSeedAndGroupsEqualKeys(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '0123456789abcdef' > /tmp/random\n" +
			"printf '2 z\\n1 a\\n3 z\\n2 a\\n' > /tmp/in.txt\n" +
			"sort -R -k2,2 --random-source=/tmp/random /tmp/in.txt > /tmp/out1\n" +
			"sort -R -k2,2 --random-source=/tmp/random /tmp/in.txt > /tmp/out2\n" +
			"cat /tmp/out1\n" +
			"printf -- '---\\n'\n" +
			"cat /tmp/out2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	got := result.Stdout
	const aThenZ = "1 a\n2 a\n2 z\n3 z\n---\n1 a\n2 a\n2 z\n3 z\n"
	const zThenA = "2 z\n3 z\n1 a\n2 a\n---\n2 z\n3 z\n1 a\n2 a\n"
	if got != aThenZ && got != zThenA {
		t.Fatalf("Stdout = %q, want a stable grouped random ordering", got)
	}
}

func TestSortRandomSortUsesCanonicalKeyHash(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '0123456789abcdef' > /tmp/random\n" +
			"printf 'Alpha\\nalpha\\nbeta\\n' > /tmp/in1\n" +
			"printf 'alpha\\nAlpha\\nbeta\\n' > /tmp/in2\n" +
			"sort -Rf --random-source=/tmp/random /tmp/in1 > /tmp/out1\n" +
			"sort -Rf --random-source=/tmp/random /tmp/in2 > /tmp/out2\n" +
			"cat /tmp/out1\n" +
			"printf -- '---\\n'\n" +
			"cat /tmp/out2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout = %q, want two outputs", result.Stdout)
	}
	if parts[0] != parts[1] {
		t.Fatalf("outputs differ: %q vs %q", parts[0], parts[1])
	}
	const alphaFirst = "Alpha\nalpha\nbeta\n"
	const betaFirst = "beta\nAlpha\nalpha\n"
	if parts[0] != alphaFirst && parts[0] != betaFirst {
		t.Fatalf("Stdout = %q, want canonical last-resort order within one random group ordering", result.Stdout)
	}
}

func TestSortRandomSortReverseReversesHashOrder(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '0123456789abcdef' > /tmp/random\n" +
			"printf 'one\\ntwo\\nthree\\n' > /tmp/in.txt\n" +
			"sort -R --random-source=/tmp/random /tmp/in.txt > /tmp/out1\n" +
			"sort -Rr --random-source=/tmp/random /tmp/in.txt > /tmp/out2\n" +
			"cat /tmp/out1\n" +
			"printf -- '---\\n'\n" +
			"cat /tmp/out2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	parts := strings.Split(result.Stdout, "---\n")
	if len(parts) != 2 {
		t.Fatalf("Stdout = %q, want two outputs", result.Stdout)
	}

	forward := strings.Split(strings.TrimSuffix(parts[0], "\n"), "\n")
	reverse := strings.Split(strings.TrimSuffix(parts[1], "\n"), "\n")
	if len(forward) != len(reverse) {
		t.Fatalf("line counts differ: %q vs %q", parts[0], parts[1])
	}
	for i := range forward {
		if reverse[i] != forward[len(forward)-1-i] {
			t.Fatalf("reverse output %q is not the reverse of %q", parts[1], parts[0])
		}
	}
}

func TestSortRandomSortReverseUsesLastResortOrderWithinGroups(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '0123456789abcdef' > /tmp/random\n" +
			"printf 'Alpha\\nalpha\\n' > /tmp/in1\n" +
			"printf 'alpha\\nAlpha\\n' > /tmp/in2\n" +
			"sort -Rrf --random-source=/tmp/random /tmp/in1 > /tmp/out1\n" +
			"sort -Rrf --random-source=/tmp/random /tmp/in2 > /tmp/out2\n" +
			"cat /tmp/out1\n" +
			"printf -- '---\\n'\n" +
			"cat /tmp/out2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	const want = "alpha\nAlpha\n---\nalpha\nAlpha\n"
	if result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

func TestSortRandomSourceRequiresEnoughBytes(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'seed-data' > /tmp/random\nprintf 'b\\na\\n' > /tmp/in.txt\nsort -R --random-source=/tmp/random /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "end of file") {
		t.Fatalf("Stderr = %q, want random-source EOF error", result.Stderr)
	}
}

func TestSortRandomCheckIgnoresDistinctKeyOrder(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '0123456789abcdef' > /tmp/random\nprintf 'b\\na\\n' | sort -Rc --random-source=/tmp/random\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty stderr", result.Stderr)
	}
}

func TestSortRandomCheckPreservesLastResortOrderWithinGroups(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'alpha\\nAlpha\\n' | sort -Rfc\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "disorder: Alpha") {
		t.Fatalf("Stderr = %q, want last-resort disorder report", result.Stderr)
	}
}

func TestSortMergeMaintainsMergeSemantics(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '1\\n4\\n' > /tmp/a\n" +
			"printf '2\\n4\\n' > /tmp/b\n" +
			"printf '3\\n5\\n' > /tmp/c\n" +
			"sort -m /tmp/a /tmp/b /tmp/c\n" +
			"sort -mu /tmp/a /tmp/b /tmp/c\n" +
			"printf '3\\n1\\n' > /tmp/r1\n" +
			"printf '2\\n0\\n' > /tmp/r2\n" +
			"sort -mr /tmp/r1 /tmp/r2\n" +
			"printf '1 x\\n2 x\\n' > /tmp/s1\n" +
			"printf '3 x\\n4 x\\n' > /tmp/s2\n" +
			"sort -ms -k2,2 /tmp/s1 /tmp/s2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "1\n2\n3\n4\n4\n5\n1\n2\n3\n4\n5\n3\n2\n1\n0\n1 x\n2 x\n3 x\n4 x\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestSortDebugAnnotatesSelectedKey(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'zebra,10\\nalpha,2\\n' > /tmp/in.csv\nsort --debug -t, -k2,2n /tmp/in.csv\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "alpha,2\n      _\n_______\nzebra,10\n      __\n________\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "numbers use '.' as a decimal point") {
		t.Fatalf("Stderr = %q, want numeric debug warning", result.Stderr)
	}
}

func TestSortRejectsInvalidBufferSize(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'b\\na\\n' > /tmp/in.txt\nsort -S not-a-size /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, `invalid -S argument "not-a-size"`) {
		t.Fatalf("Stderr = %q, want invalid buffer-size error", result.Stderr)
	}
}

func TestSortRejectsInvalidFieldSeparators(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a\\n' > /tmp/in.txt\nsort -t '' /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "empty tab") {
		t.Fatalf("Stderr = %q, want empty-tab error", result.Stderr)
	}

	result, err = rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a::1\\n' > /tmp/in.txt\nsort -t '::' /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "multi-character tab '::'") {
		t.Fatalf("Stderr = %q, want multi-character separator error", result.Stderr)
	}
}

func TestUniqSupportsCountsAndAdjacentRuns(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo apple > /tmp/in.txt\n echo apple >> /tmp/in.txt\n echo banana >> /tmp/in.txt\n echo banana >> /tmp/in.txt\n echo banana >> /tmp/in.txt\n echo cherry >> /tmp/in.txt\n uniq -c /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "      2 apple\n      3 banana\n      1 cherry\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestUniqWorksWithSortForFullDeduping(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo b > /tmp/in.txt\n echo a >> /tmp/in.txt\n echo b >> /tmp/in.txt\n echo c >> /tmp/in.txt\n sort /tmp/in.txt | uniq\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "a\nb\nc\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestUniqReturnsErrorForMissingFile(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "uniq /missing.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "/missing.txt") {
		t.Fatalf("Stderr = %q, want missing-file error", result.Stderr)
	}
}

func TestUniqSupportsIgnoreCase(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'Apple\\napple\\nBanana\\n' > /tmp/in.txt\nuniq --ignore-case -c /tmp/in.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "      2 Apple\n      1 Banana\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestUniqSupportsCheckChars(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf '0.1\\n0.2\\n0.7\\n1.0\\n' > /tmp/prefix.txt\nuniq -w2 /tmp/prefix.txt\nprintf 'alpha\\nAlps\\nbeta\\n' | uniq --ignore-case --check-chars=2\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "0.1\n1.0\nalpha\nbeta\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
