package runtime

import "testing"

func TestRedirectRegressionSupportsOverwriteAppendAndInputRedirection(t *testing.T) {
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

func TestCommandSubstitutionRegressionFeedsExpandedArgs(t *testing.T) {
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
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "for name in alpha beta; do echo \"item:$name\"; done\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "item:alpha\nitem:beta\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLetRegressionSupportsQuotedExpandedAndRuntimeExpandedArithmeticExpressions(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"expr='x = 1 + 2'\n"+
		"let \"$expr\"\n"+
		"let 'y=4+5' z=6+7\n"+
		"a=b\n"+
		"b=3\n"+
		"let $a+=1\n"+
		"let ${a}+=1\n"+
		"let $a++\n"+
		"let ++$a\n"+
		"printf '%s,%s,%s,%s\\n' \"$x\" \"$y\" \"$z\" \"$b\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "3,9,13,7\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLetRegressionSupportsExpandedAssignmentTargetExpression(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "a=b; b=3; let $a+=1; echo $b;\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "4\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLetRegressionAllowsFunctionShadowingForBareLet(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"a=1\n"+
		"function let { a=99; echo function; }\n"+
		"let a+=1\n"+
		"printf '%s\\n' \"$a\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "function\n99\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLetRegressionSupportsCommandAndBuiltinForms(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"a=1\n"+
		"function let { a=99; echo function; }\n"+
		"command let a+=1\n"+
		"builtin let a+=1\n"+
		"printf '%s\\n' \"$a\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "3\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLetRegressionSupportsSourceAndEval(t *testing.T) {
	session := newSession(t, &Config{})
	writeSessionFile(t, session, "/home/agent/let.sh", []byte("let $a+=1\n"))

	result := mustExecSession(t, session, ""+
		"a=b\n"+
		"b=3\n"+
		"source /home/agent/let.sh\n"+
		"eval 'let ${a}+=1'\n"+
		"printf '%s\\n' \"$b\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "5\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLetRegressionSupportsTrapCallbacks(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"a=b\n"+
		"b=3\n"+
		"trap 'let ++$a; printf \"trap:%s\\\\n\" \"$b\"' EXIT\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "trap:4\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestLetRegressionLeavesLiteralContextsUntouched(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, ""+
		"case let in\n"+
		"  let) echo case-ok ;;\n"+
		"  *) echo case-bad ;;\n"+
		"esac\n"+
		"if ((let=5, let<6)); then echo arith-ok; fi\n"+
		"cat <<'EOF'\n"+
		"let $a+=1\n"+
		"EOF\n"+
		"printf '%s\\n' \"$(printf 'let $a+=1')\"\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "case-ok\narith-ok\nlet $a+=1\nlet $a+=1\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}

func TestArithmeticRegressionDoesNotRewriteLetIdentifiersInsideArithmeticCommands(t *testing.T) {
	session := newSession(t, &Config{})

	result := mustExecSession(t, session, "let=5; if ((let<3)); then echo bad; else echo ok; fi\n")
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if got, want := result.Stdout, "ok\n"; got != want {
		t.Fatalf("Stdout = %q, want %q", got, want)
	}
}
