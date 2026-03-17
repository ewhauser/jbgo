package runtime

import "testing"

func TestRedirectRegressionSupportsOverwriteAppendAndInputRedirection(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "echo first > log.txt\n echo second >> log.txt\n cat < log.txt\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "first\nsecond\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionChainsShellAndRegistryCommands(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "printf 'alpha\nbeta\nbeta\n' > words.txt\n cat words.txt | grep beta | head -n 1\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "beta\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionDoesNotLeakPipedLoopVariableMutations(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"count=0\n"+
		"printf '%s\\n' alpha beta gamma | while IFS= read -r _; do count=$((count + 1)); done\n"+
		"printf 'after-pipeline:%s\\n' \"$count\"\n"+
		"while IFS= read -r _; do count=$((count + 1)); done <<'EOF'\n"+
		"delta\n"+
		"epsilon\n"+
		"EOF\n"+
		"printf 'after-heredoc:%s\\n' \"$count\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "after-pipeline:0\nafter-heredoc:2\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionDoesNotPersistFinalStageReadVariable(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"unset value\n"+
		"printf 'hello\\n' | read -r value\n"+
		"printf 'value:<%s>\\n' \"${value-}\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "value:<>\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionLastpipePersistsFinalStageReadVariable(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"shopt -s lastpipe\n"+
		"unset value\n"+
		"printf 'hello\\n' | read -r value\n"+
		"printf 'value:<%s>\\n' \"${value-}\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "value:<hello>\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionLastpipePersistsMapfileAndLoopState(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"shopt -s lastpipe\n"+
		"seq 2 | mapfile m\n"+
		"seq 3 | readarray r\n"+
		"i=0\n"+
		"seq 3 | while read -r _; do (( i++ )); done\n"+
		"printf 'm=%s r=%s i=%s\\n' \"${#m[@]}\" \"${#r[@]}\" \"$i\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "m=2 r=3 i=3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestPipelineRegressionLastpipeDoesNotUnwrapUserSubshellTail(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"shopt -s lastpipe\n"+
		"unset v\n"+
		"printf 'x\\n' | (read -r v; echo \"inner:$v\")\n"+
		"printf 'outer:<%s>\\n' \"${v-}\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "inner:x\nouter:<>\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestCommandSubstitutionRegressionFeedsExpandedArgs(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/note.txt", []byte("sandbox\n"))

	result := mustExecSession(t, session, "name=$(cat note.txt)\n echo \"hello $name\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello sandbox\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestConditionalRegressionSupportsBuiltinStringPredicates(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "status=ready\n if test \"$status\" = ready; then echo exists; else echo missing; fi\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "exists\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLoopRegressionSupportsForControlFlow(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "for name in alpha beta; do echo \"item:$name\"; done\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "item:alpha\nitem:beta\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLetRegressionSupportsLiteralArithmeticExpressions(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"let y=4+5 z=6+7\n"+
		"printf '%s,%s\\n' \"$y\" \"$z\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "9,13\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestDeclarationRegressionIgnoresPrefixEnvAssignments(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"f() {\n"+
		"  E=env local l=var\n"+
		"  E=env declare d=var\n"+
		"  E=env export x=var\n"+
		"  E=env readonly r=var\n"+
		"  E=env typeset t=var\n"+
		"  printf 'E:<%s> l:%s d:%s x:%s r:%s t:%s\\n' \"${E-}\" \"$l\" \"$d\" \"$x\" \"$r\" \"$t\"\n"+
		"}\n"+
		"f\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "E:<> l:var d:var x:var r:var t:var\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
	if got := result.Stderr; got != "" {
		t.Fatalf("Stderr = %q, want empty", got)
	}
}

func TestProcessSubstitutionRegressionSupportsInputOutputAndPipePredicates(t *testing.T) {
	t.Parallel()
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"cat <(echo hello)\n"+
		"while IFS= read -r line; do printf 'loop:%s\\n' \"$line\"; done < <(printf 'a\\nb\\n')\n"+
		"printf 'hello-out\\n' > >(cat > /tmp/out)\n"+
		"while [ ! -s /tmp/out ]; do sleep 0.01; done\n"+
		"p=<(echo hi)\n"+
		"[ -p \"$p\" ] && cat \"$p\"\n"+
		"q=>(cat > /tmp/out2)\n"+
		"[ -p \"$q\" ] && printf 'write-side\\n' > \"$q\"\n"+
		"while [ ! -s /tmp/out2 ]; do sleep 0.01; done\n"+
		"cat /tmp/out\n"+
		"cat /tmp/out2\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "hello\nloop:a\nloop:b\nhi\nhello-out\nwrite-side\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
