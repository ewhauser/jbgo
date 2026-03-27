package builtins_test

import (
	"context"
	"strings"
	"testing"
)

func TestMVSupportsParityFlagsIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p /tmp/dst\n" +
			"echo keep > /tmp/dst/src.txt\n" +
			"echo src > /tmp/src.txt\n" +
			"echo move > /tmp/move.txt\n" +
			"echo force > /tmp/force.txt\n" +
			"echo occupied > /tmp/occupied.txt\n" +
			"echo skip > /tmp/skip.txt\n" +
			"mv -n /tmp/src.txt /tmp/dst/src.txt\n" +
			"cat /tmp/dst/src.txt\n" +
			"mv --verbose /tmp/move.txt /tmp/dst\n" +
			"cat /tmp/dst/move.txt\n" +
			"mv -f /tmp/force.txt /tmp/forced.txt\n" +
			"cat /tmp/forced.txt\n" +
			"mv --force --no-clobber /tmp/skip.txt /tmp/occupied.txt\n" +
			"cat /tmp/occupied.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "keep\nrenamed '/tmp/move.txt' -> '/tmp/dst/move.txt'\nmove\nforce\noccupied\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVPromptPrecedenceAndUpdateSkipIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "printf 'a' > a\n" +
			"printf 'b' > b\n" +
			"printf 'n\\n' | mv -i a b\n" +
			"printf 'status1=%s\\n' \"$?\"\n" +
			"[ -e a ] && echo a-kept || echo a-moved\n" +
			"printf 'c' > c\n" +
			"printf 'd' > d\n" +
			"printf 'y\\n' | mv -if c d\n" +
			"printf 'status2=%s\\n' \"$?\"\n" +
			"[ -e c ] && echo c-kept || echo c-moved\n" +
			"printf 'e' > e\n" +
			"printf 'f' > f\n" +
			"printf 'y\\n' | mv -fi e f\n" +
			"printf 'status3=%s\\n' \"$?\"\n" +
			"[ -e e ] && echo e-kept || echo e-moved\n" +
			"echo old > old\n" +
			"touch -d yesterday old\n" +
			"echo new > new\n" +
			"printf 'n\\n' | mv -iu old new\n" +
			"printf 'status4=%s\\n' \"$?\"\n" +
			"cat new\n" +
			"cat old\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status1=1\na-kept\nstatus2=0\nc-moved\nstatus3=0\ne-moved\nstatus4=0\nnew\nold\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := strings.Count(result.Stderr, "mv: overwrite "); got != 2 {
		t.Fatalf("prompt count = %d, want 2; stderr=%q", got, result.Stderr)
	}
	if strings.Contains(result.Stderr, "'d'?") {
		t.Fatalf("Stderr = %q, want no prompt when -f overrides -i", result.Stderr)
	}
	if strings.Contains(result.Stderr, "'new'?") {
		t.Fatalf("Stderr = %q, want no prompt when --update skips replacement", result.Stderr)
	}
}

func TestMVBackupAndSameFileHandlingIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir A B\n" +
			"mv --verbose --backup=numbered -T A B\n" +
			"printf 'status1=%s\\n' \"$?\"\n" +
			"[ -d B ] && echo dir-moved\n" +
			"[ -d 'B.~1~' ] && echo dir-backup\n" +
			"touch a\n" +
			"ln a b\n" +
			"mv a b\n" +
			"printf 'status2=%s\\n' \"$?\"\n" +
			"[ -e a ] && echo a-still-there\n" +
			"[ -e b ] && echo b-still-there\n" +
			"mv --backup=simple a b\n" +
			"printf 'status3=%s\\n' \"$?\"\n" +
			"[ -e a ] && echo a-after-backup || echo a-gone-after-backup\n" +
			"[ -e b ] && echo b-after-backup\n" +
			"[ -e b~ ] && echo backup-file\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "renamed 'A' -> 'B' (backup: 'B.~1~')\nstatus1=0\ndir-moved\ndir-backup\nstatus2=1\na-still-there\nb-still-there\nstatus3=0\na-gone-after-backup\nb-after-backup\nbackup-file\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "mv: 'a' and 'b' are the same file\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestMVUpdateModesIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo src > src-none\n" +
			"echo dst > dst-none\n" +
			"mv --update=none src-none dst-none\n" +
			"printf 'status1=%s\\n' \"$?\"\n" +
			"cat src-none\n" +
			"cat dst-none\n" +
			"echo src > src-fail\n" +
			"echo dst > dst-fail\n" +
			"mv --update=none-fail src-fail dst-fail\n" +
			"printf 'status2=%s\\n' \"$?\"\n" +
			"[ -e src-fail ] && echo src-fail-exists\n" +
			"cat dst-fail\n" +
			"echo same > same\n" +
			"mv --update=none same same\n" +
			"printf 'status3=%s\\n' \"$?\"\n" +
			"cat same\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status1=0\nsrc\ndst\nstatus2=1\nsrc-fail-exists\ndst\nstatus3=0\nsame\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "mv: not replacing") {
		t.Fatalf("Stderr = %q, want update=none-fail diagnostic", result.Stderr)
	}
	if strings.Contains(result.Stderr, "same file") {
		t.Fatalf("Stderr = %q, want update=none same-file case to skip quietly", result.Stderr)
	}
}

func TestMVUpdateOlderDoesNotSkipDirectoryReplacementIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir srcdir dstdir\n" +
			"echo src > srcdir/file\n" +
			"touch -d '2024-01-01 00:00:00 UTC' srcdir\n" +
			"touch -d '2024-01-02 00:00:00 UTC' dstdir\n" +
			"mv -u -T srcdir dstdir\n" +
			"printf 'status=%s\\n' \"$?\"\n" +
			"[ -e srcdir ] && echo srcdir-exists || echo srcdir-gone\n" +
			"cat dstdir/file\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=0\nsrcdir-gone\nsrc\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty stderr", result.Stderr)
	}
}

func TestMVDoesNotOverwriteJustCreatedDestinationIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir a b c\n" +
			"printf 'a\\n' > a/f\n" +
			"printf 'b\\n' > b/f\n" +
			"mv a/f b/f c\n" +
			"printf 'status=%s\\n' \"$?\"\n" +
			"[ -e a/f ] && echo a-still || echo a-gone\n" +
			"[ -e b/f ] && echo b-still || echo b-gone\n" +
			"cat c/f\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\na-gone\nb-still\na\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "mv: will not overwrite just-created 'c/f' with 'b/f'\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestMVAllowsNumberedBackupOfJustCreatedDestinationIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir a b c\n" +
			"printf 'a\\n' > a/f\n" +
			"printf 'b\\n' > b/f\n" +
			"mv --backup=numbered a/f b/f c\n" +
			"printf 'status=%s\\n' \"$?\"\n" +
			"[ -e a/f ] && echo a-still || echo a-gone\n" +
			"[ -e b/f ] && echo b-still || echo b-gone\n" +
			"cat c/f\n" +
			"cat c/f.~1~\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=0\na-gone\nb-gone\nb\na\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty stderr", result.Stderr)
	}
}

func TestMVChildproofSequenceIsolated(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir a b c\n" +
			"printf 'a\\n' > a/f\n" +
			"printf 'b\\n' > b/f\n" +
			"mv a/f b/f c 2>/dev/null\n" +
			"printf 'status1=%s\\n' \"$?\"\n" +
			"[ -e a/f ] && echo a1-still || echo a1-gone\n" +
			"[ -e b/f ] && echo b1-still || echo b1-gone\n" +
			"cat c/f\n" +
			"rm -f c/f* b/f\n" +
			"touch a/f\n" +
			"ln a/f b/g\n" +
			"mv a/f b/g c\n" +
			"printf 'status2=%s\\n' \"$?\"\n" +
			"[ -e a/f ] && echo a2-still || echo a2-gone\n" +
			"[ -e b/g ] && echo b2-still || echo b2-gone\n" +
			"[ -f c/f ] && echo c-f-present\n" +
			"[ -f c/g ] && echo c-g-present\n" +
			"touch a/f b/f b/g\n" +
			"mv a/f b/f b/g c 2>/dev/null\n" +
			"printf 'status3=%s\\n' \"$?\"\n" +
			"[ -e a/f ] && echo a3-still || echo a3-gone\n" +
			"[ -e b/f ] && echo b3-still || echo b3-gone\n" +
			"[ -e b/g ] && echo b4-still || echo b4-gone\n" +
			"[ -f c/f ] && echo c-f-still-present\n" +
			"[ -f c/g ] && echo c-g-still-present\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status1=1\na1-gone\nb1-still\na\nstatus2=0\na2-gone\nb2-gone\nc-f-present\nc-g-present\nstatus3=1\na3-gone\nb3-still\nb4-gone\nc-f-still-present\nc-g-still-present\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty stderr due to redirected diagnostics", result.Stderr)
	}
}
