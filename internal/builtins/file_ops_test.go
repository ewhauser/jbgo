package builtins_test

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestCPCopiesRecursiveDirectoryTree(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p /src/nested\n echo root > /src/root.txt\n echo leaf > /src/nested/leaf.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "cp -r /src /dst\n find /dst -type f\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{"/dst/nested/leaf.txt", "/dst/root.txt"} {
		if !containsLine(strings.Split(strings.TrimSpace(result.Stdout), "\n"), want) {
			t.Fatalf("Stdout missing %q: %q", want, result.Stdout)
		}
	}
}

func TestCPCopiesBinaryFileBytes(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/src.bin", []byte{0x00, 0xff, 0x41, 0x42, 0x00})

	result := mustExecSession(t, session, "cp /src.bin /dst.bin\n wc -c /dst.bin\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "5 /dst.bin") {
		t.Fatalf("Stdout = %q, want copied byte count", result.Stdout)
	}
}

func TestCPRejectsDirectoryWithoutRecursiveFlag(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p /src\n echo hi > /src/file.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "cp /src /dst\n")
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "omitting directory") {
		t.Fatalf("Stderr = %q, want directory omission error", result.Stderr)
	}
}

func TestRMSupportsGroupedForceDirFlags(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /tmp/empty\necho x > /tmp/out\nrm -fd /tmp/empty /tmp/out\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}

	for _, name := range []string{"/tmp/empty", "/tmp/out"} {
		if _, err := session.FileSystem().Lstat(context.Background(), name); !os.IsNotExist(err) {
			t.Fatalf("Lstat(%q) error = %v, want not exist", name, err)
		}
	}
}

func TestRMForceWithoutOperandsAndContinuesAfterMissingFile(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "rm -f\nprintf 'data' > /tmp/keep.txt\nrm /tmp/missing /tmp/keep.txt\nprintf 'status=%s\\n' \"$?\"\n[ -e /tmp/keep.txt ] && echo exists || echo gone\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\ngone\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "cannot remove '/tmp/missing': No such file or directory") {
		t.Fatalf("Stderr = %q, want missing-file diagnostic", result.Stderr)
	}
}

func TestRMVerboseRecursiveAndDirectoryRemoval(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir -p /tmp/tree/sub\nprintf 'leaf' > /tmp/tree/sub/file.txt\nrm -rv /tmp/tree\nmkdir /tmp/empty\nrm -dv /tmp/empty\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	want := "removed '/tmp/tree/sub/file.txt'\nremoved directory '/tmp/tree/sub'\nremoved directory '/tmp/tree'\nremoved directory '/tmp/empty'\n"
	if got := result.Stdout; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestRMPreserveRootAndRejectsAbbreviatedOverride(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	rootResult := mustExecSession(t, session, "rm -r --preserve-root /\n")
	if rootResult.ExitCode != 1 {
		t.Fatalf("preserve-root ExitCode = %d, want 1; stderr=%q", rootResult.ExitCode, rootResult.Stderr)
	}
	wantRootErr := "rm: it is dangerous to operate recursively on '/'\nrm: use --no-preserve-root to override this failsafe\n"
	if got := rootResult.Stderr; got != wantRootErr {
		t.Fatalf("preserve-root Stderr = %q, want %q", got, wantRootErr)
	}

	abbrResult := mustExecSession(t, session, "rm -r --no-preserve-r /tmp/missing\n")
	if abbrResult.ExitCode != 1 {
		t.Fatalf("abbreviation ExitCode = %d, want 1; stderr=%q", abbrResult.ExitCode, abbrResult.Stderr)
	}
	if !strings.Contains(abbrResult.Stderr, "may not abbreviate the --no-preserve-root option") {
		t.Fatalf("abbreviation Stderr = %q, want no-preserve-root abbreviation diagnostic", abbrResult.Stderr)
	}
}

func TestRMRefusesCurrentAndParentDirectoryOperands(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "mkdir d\nprintf 'keep' > d/file\nrm -rf d/. . ..\nprintf 'status=%s\\n' \"$?\"\n[ -e d/file ] && echo kept || echo removed\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\nkept\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	for _, want := range []string{
		"skipping 'd/.'",
		"skipping '.'",
		"skipping '..'",
	} {
		if !strings.Contains(result.Stderr, want) {
			t.Fatalf("Stderr = %q, want %q", result.Stderr, want)
		}
	}
}

func TestRMCurrentOrParentDirectoryNonRecursiveUsesNormalDirectoryErrors(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"mkdir d\n"+
			"printf 'keep' > d/file\n"+
			"rm d/.\n"+
			"printf 'plain=%s\\n' \"$?\"\n"+
			"rm -d d/.\n"+
			"printf 'dir=%s\\n' \"$?\"\n"+
			"[ -e d/file ] && echo kept || echo removed\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "plain=1\ndir=1\nkept\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	for _, want := range []string{
		"rm: cannot remove 'd/.': Is a directory",
		"rm: cannot remove 'd/.': Directory not empty",
	} {
		if !strings.Contains(result.Stderr, want) {
			t.Fatalf("Stderr = %q, want %q", result.Stderr, want)
		}
	}
	if strings.Contains(result.Stderr, "refusing to remove '.' or '..'") {
		t.Fatalf("Stderr = %q, want non-recursive directory errors instead of refusal", result.Stderr)
	}
}

func TestRMPromptOrderAndPresumeInputTTY(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"printf 'one' > /tmp/one\n"+
			"printf 'y\\n' | rm -fi /tmp/one\n"+
			"[ -e /tmp/one ] && echo first-kept || echo first-removed\n"+
			"printf 'two' > /tmp/two\n"+
			"printf 'y\\n' | rm -if /tmp/two\n"+
			"[ -e /tmp/two ] && echo second-kept || echo second-removed\n"+
			"printf 'guard' > /tmp/protected\n"+
			"chmod 400 /tmp/protected\n"+
			"printf 'n\\n' | rm ---presume-input-tty /tmp/protected\n"+
			"[ -e /tmp/protected ] && echo third-kept || echo third-removed\n"+
			"printf 'fallback' > /tmp/plain\n"+
			"chmod 400 /tmp/plain\n"+
			"printf 'n\\n' | rm /tmp/plain\n"+
			"[ -e /tmp/plain ] && echo fourth-kept || echo fourth-removed\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "first-removed\nsecond-removed\nthird-kept\nfourth-removed\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "rm: remove file '/tmp/one'? ") {
		t.Fatalf("Stderr = %q, want prompt for -fi order", result.Stderr)
	}
	if strings.Contains(result.Stderr, "/tmp/two") {
		t.Fatalf("Stderr = %q, want no prompt for -if order", result.Stderr)
	}
	if !strings.Contains(result.Stderr, "rm: remove write-protected regular file '/tmp/protected'? ") {
		t.Fatalf("Stderr = %q, want write-protected prompt when presume-input-tty is set", result.Stderr)
	}
}

func TestRMPromptConsumesBufferedInputAcrossMultipleTargets(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"printf 'one' > /tmp/one\n"+
			"printf 'two' > /tmp/two\n"+
			"printf 'y\\ny\\n' | rm -i /tmp/one /tmp/two\n"+
			"[ -e /tmp/one ] && echo one-kept || echo one-removed\n"+
			"[ -e /tmp/two ] && echo two-kept || echo two-removed\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "one-removed\ntwo-removed\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	for _, want := range []string{
		"rm: remove file '/tmp/one'? ",
		"rm: remove file '/tmp/two'? ",
	} {
		if !strings.Contains(result.Stderr, want) {
			t.Fatalf("Stderr = %q, want prompt %q", result.Stderr, want)
		}
	}
}

func TestRMBareInteractiveOverridesPriorInteractiveValue(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"printf 'data' > /tmp/target\n"+
			"printf 'n\\n' | rm --interactive=never --interactive /tmp/target\n"+
			"[ -e /tmp/target ] && echo kept || echo removed\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "kept\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "rm: remove file '/tmp/target'? ") {
		t.Fatalf("Stderr = %q, want trailing bare --interactive prompt", result.Stderr)
	}
}

func TestRMLaterInteractiveStopsForceIgnoringMissingFiles(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"rm -f -i /tmp/missing\n"+
			"printf 'status=%s\\n' \"$?\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "cannot remove '/tmp/missing': No such file or directory") {
		t.Fatalf("Stderr = %q, want missing-file diagnostic", result.Stderr)
	}
	if strings.Contains(result.Stderr, "? ") {
		t.Fatalf("Stderr = %q, want no prompt for missing file", result.Stderr)
	}
}

func TestRMInteractiveNeverPreservesForceIgnoringMissingFiles(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"rm -f --interactive=never /tmp/missing\n"+
			"printf 'status=%s\\n' \"$?\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=0\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if result.Stderr != "" {
		t.Fatalf("Stderr = %q, want empty when force is preserved", result.Stderr)
	}
}

func TestRMInteractiveOnceSkipsPerFileProtectedPrompt(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"printf 'guard' > /tmp/protected\n"+
			"chmod 400 /tmp/protected\n"+
			"printf 'n\\n' | rm -I /tmp/protected\n"+
			"printf 'status=%s\\n' \"$?\"\n"+
			"[ -e /tmp/protected ] && echo kept || echo removed\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=0\nremoved\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if strings.Contains(result.Stderr, "write-protected") {
		t.Fatalf("Stderr = %q, want no per-file write-protected prompt in -I mode", result.Stderr)
	}
}

func TestRMDecliningNestedDescentLeavesTreeUntouchedWithoutError(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"mkdir -p /tmp/tree/sub\n"+
			"printf 'leaf' > /tmp/tree/sub/file\n"+
			"printf 'y\\nn\\n' | rm -ri /tmp/tree\n"+
			"printf 'status=%s\\n' \"$?\"\n"+
			"[ -e /tmp/tree/sub/file ] && echo kept || echo removed\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=0\nkept\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	for _, want := range []string{
		"rm: descend into directory '/tmp/tree'? ",
		"rm: descend into directory '/tmp/tree/sub'? ",
	} {
		if !strings.Contains(result.Stderr, want) {
			t.Fatalf("Stderr = %q, want prompt %q", result.Stderr, want)
		}
	}
	if strings.Contains(result.Stderr, "Directory not empty") {
		t.Fatalf("Stderr = %q, want no synthetic directory-not-empty error", result.Stderr)
	}
}

func TestRMDefaultProtectedModePromptsBeforeDescendingWriteProtectedDirectory(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session,
		"mkdir -p /tmp/tree/sub\n"+
			"printf 'leaf' > /tmp/tree/sub/file\n"+
			"chmod 500 /tmp/tree\n"+
			"printf 'n\\n' | rm ---presume-input-tty -r /tmp/tree\n"+
			"printf 'status=%s\\n' \"$?\"\n"+
			"[ -e /tmp/tree/sub/file ] && echo kept || echo removed\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=0\nkept\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if !strings.Contains(result.Stderr, "rm: descend into directory '/tmp/tree'? ") {
		t.Fatalf("Stderr = %q, want pre-descent prompt for protected directory", result.Stderr)
	}
	for _, unwanted := range []string{
		"/tmp/tree/sub/file",
		"remove write-protected directory '/tmp/tree'",
	} {
		if strings.Contains(result.Stderr, unwanted) {
			t.Fatalf("Stderr = %q, want no later traversal/removal prompt %q after declining descent", result.Stderr, unwanted)
		}
	}
}

func TestCPSupportsNoClobberPreserveAndVerbose(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "echo new > /src.txt\necho old > /dst.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "cp --no-clobber --preserve --verbose /src.txt /dst.txt\ncat /dst.txt\ncp -pv /src.txt /fresh.txt\ncat /fresh.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "old\n'/src.txt' -> '/fresh.txt'\nnew\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVCanMoveDirectoryIntoExistingDirectory(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p /src/sub /dst\n echo hi > /src/sub/file.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "mv /src /dst\n ls /dst/src/sub\n cat /dst/src/sub/file.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "file.txt\nhi\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVOverwritesExistingDestinationFile(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "echo new > /src.txt\n echo old > /dst.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "mv /src.txt /dst.txt\n cat /dst.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "new\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVPreservesTraversalForLaterFindCommands(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p /src/sub /dst\n echo hi > /src/sub/file.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "mv /src /dst\n find /dst -type f\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "/dst/src/sub/file.txt"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVRejectsMissingSource(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mv /missing.txt /dst.txt\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = %d, want non-zero", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "cannot stat") {
		t.Fatalf("Stderr = %q, want missing-source error", result.Stderr)
	}
}

func TestMVSupportsNoClobberVerboseAndMovingFileIntoDirectory(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	setup := mustExecSession(t, session, "mkdir -p /dst\necho src > /src.txt\necho keep > /dst/src.txt\necho move > /move.txt\n")
	if setup.ExitCode != 0 {
		t.Fatalf("setup ExitCode = %d, want 0", setup.ExitCode)
	}

	result := mustExecSession(t, session, "mv --no-clobber /src.txt /dst/src.txt\ncat /dst/src.txt\nmv -v /move.txt /dst\ncat /dst/move.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "keep\nrenamed '/move.txt' -> '/dst/move.txt'\nmove\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVNoTargetDirectoryHandlesEmptyAndRejectsInvalidTargets(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p d/sub empty src d2/sub e2\n" +
			"touch f\n" +
			"mv -fT d empty\n" +
			"printf 'status1=%s\\n' \"$?\"\n" +
			"[ -d empty/sub ] && echo moved-empty\n" +
			"mv -fT src d2 2>/dev/null\n" +
			"printf 'status2=%s\\n' \"$?\"\n" +
			"mv -fT f e2 2>/dev/null\n" +
			"printf 'status3=%s\\n' \"$?\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status1=0\nmoved-empty\nstatus2=1\nstatus3=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVRejectsDirectoryFileMismatches(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p dir/file\n" +
			": > file\n" +
			"mv dir file 2>/dev/null\n" +
			"printf 'status1=%s\\n' \"$?\"\n" +
			"mv file dir 2>/dev/null\n" +
			"printf 'status2=%s\\n' \"$?\"\n" +
			"[ -d dir ] && echo dir-exists\n" +
			"[ -d dir/file ] && echo dir-file-exists\n" +
			"[ -f file ] && echo file-exists\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status1=1\nstatus2=1\ndir-exists\ndir-file-exists\nfile-exists\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVContinuesAfterSelfSubdirectoryError(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "rm -rf toself file\n" +
			"mkdir -p toself/a\n" +
			": > file\n" +
			"mv toself file toself\n" +
			"printf 'status=%s\\n' \"$?\"\n" +
			"[ -d toself ] && echo dir-exists\n" +
			"[ ! -d toself/toself ] && echo no-self-copy\n" +
			"[ -f toself/file ] && echo file-moved\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\ndir-exists\nno-self-copy\nfile-moved\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "mv: cannot move 'toself' to a subdirectory of itself, 'toself/toself'\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestMVDuplicateDirectoryWarningAfterBlockedMove(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "rm -rf b\n" +
			"mkdir b\n" +
			"mv --verbose ./b b b/\n" +
			"printf 'status=%s\\n' \"$?\"\n" +
			"[ -d b ] && echo dir-still-exists\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\ndir-still-exists\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; !strings.Contains(got, "mv: cannot move './b' to a subdirectory of itself, 'b/b'\n") || !strings.Contains(got, "mv: warning: source directory 'b' specified more than once\n") {
		t.Fatalf("Stderr = %q, want self-subdir error plus duplicate warning", got)
	}
}

func TestMVReportsPermissionDeniedOnce(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p no-write/dir\n" +
			"chmod ug-w no-write\n" +
			"mv no-write/dir .\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stderr, "mv: cannot move 'no-write/dir' to './dir': Permission denied\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestMVSymlinkOntoHardlinkToSameFileFails(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "touch f\n" +
			"ln f h\n" +
			"ln -s f s\n" +
			"mv s f\n" +
			"printf 'status=%s\\n' \"$?\"\n" +
			"[ -L s ] && echo symlink-still-there\n" +
			"[ -f f ] && echo file-still-there\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\nsymlink-still-there\nfile-still-there\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "mv: 's' and 'f' are the same file\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestMVRejectsTrailingSlashMissingFileTargets(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: ": > plain\n" +
			"mv plain missing/ 2>/dev/null\n" +
			"printf 'status1=%s\\n' \"$?\"\n" +
			"[ -f plain ] && echo plain-still-there\n" +
			": > plain2\n" +
			"mv -T plain2 missing2/ 2>/dev/null\n" +
			"printf 'status2=%s\\n' \"$?\"\n" +
			"[ -f plain2 ] && echo plain2-still-there\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status1=1\nplain-still-there\nstatus2=1\nplain2-still-there\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestMVTargetDirectoryIsValidatedOnceBeforeSources(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: ": > src1\n" +
			": > src2\n" +
			"mv -t missing src1 src2\n" +
			"printf 'status=%s\\n' \"$?\"\n" +
			"[ -f src1 ] && echo src1-still-there\n" +
			"[ -f src2 ] && echo src2-still-there\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\nsrc1-still-there\nsrc2-still-there\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "mv: target directory 'missing': No such file or directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestMVBackupRefusesToDeleteDirectoryCollision(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "echo src > x\n" +
			"mkdir d\n" +
			"echo dst > d/x\n" +
			"mkdir d/x~\n" +
			"mv --backup=simple x d/\n" +
			"printf 'status=%s\\n' \"$?\"\n" +
			"[ -f x ] && echo src-still-there\n" +
			"[ -f d/x ] && echo dst-still-there\n" +
			"[ -d d/x~ ] && echo backup-dir-still-there\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "status=1\nsrc-still-there\ndst-still-there\nbackup-dir-still-there\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got, want := result.Stderr, "mv: cannot backup 'd/x': Is a directory\n"; got != want {
		t.Fatalf("Stderr = %q, want %q", got, want)
	}
}

func TestFindSupportsRelativeRootAndNameFilter(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p docs src\n echo readme > docs/README.md\n echo note > docs/notes.txt\n find . -name \"*.md\"\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "./docs/README.md"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFindSupportsTypeAndMaxDepth(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p src/sub\n echo one > src/one.txt\n echo two > src/sub/two.txt\n find /home/agent/src -maxdepth 1 -type f\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := strings.TrimSpace(result.Stdout), "/home/agent/src/one.txt"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestFindTypeFilterTraversesNestedFiles(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p src/sub\n echo one > src/one.txt\n echo two > src/sub/two.txt\n find /home/agent/src -type f\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	for _, want := range []string{"/home/agent/src/one.txt", "/home/agent/src/sub/two.txt"} {
		if !containsLine(lines, want) {
			t.Fatalf("Stdout missing %q: %q", want, result.Stdout)
		}
	}
}

func TestFindReturnsPartialResultsWhenOneRootIsMissing(t *testing.T) {
	t.Parallel()
	rt := newRuntime(t, &Config{})

	result, err := rt.Run(context.Background(), &ExecutionRequest{
		Script: "mkdir -p a\n echo one > a/one.txt\n find /home/agent/a /missing -type f\n",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "/home/agent/a/one.txt") {
		t.Fatalf("Stdout = %q, want partial success output", result.Stdout)
	}
	if !strings.Contains(result.Stderr, "/missing") {
		t.Fatalf("Stderr = %q, want missing-root error", result.Stderr)
	}
}
