package runtime

import (
	"context"
	"testing"
)

func FuzzRuntimeScript(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("echo hi\n"),
		[]byte("printf 'alpha\\n' > /tmp/a\ncat /tmp/a\n"),
		[]byte("touch /tmp/note\ncp /tmp/note /tmp/note.copy\nmv /tmp/note.copy /tmp/note.done\n"),
		[]byte("for i in 1 2 3; do echo $i; done\n"),
		[]byte("if true; then echo ok; fi\n"),
		[]byte("echo $(echo nested)\n"),
		[]byte("printf 'a\\nb\\n' | head -n 1\n"),
		[]byte("printf 'a\\0b\\0c\\0' | head -z -n 2\n"),
		[]byte("printf 'alpha\\nbeta\\ngamma\\n' | head -n -1\n"),
		[]byte("printf 'abcdef' | head -c -2\n"),
		[]byte("printf 'alpha\\nbeta\\n' | sort | uniq\n"),
		[]byte("env -i ONLY=value printenv ONLY\n"),
		[]byte("printf 'x y\\n' | xargs -n 1 echo\n"),
		[]byte("bash -c 'echo child'\n"),
		[]byte(">&0\n"),
		[]byte(">&000000000000000000\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, script []byte) {
		result, err := runFuzzScript(t, rt, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}

func FuzzMalformedScript(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := [][]byte{
		[]byte("echo 'unterminated\n"),
		[]byte("if true; then\n"),
		[]byte("$(\n"),
		[]byte("${foo\n"),
		[]byte("for do done\n"),
		[]byte("echo hi\x00there\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, script []byte) {
		result, err := runFuzzScript(t, rt, script)
		assertSecureFuzzOutcome(t, script, result, err)
	})
}

func FuzzSessionSequence(f *testing.F) {
	rt := newFuzzRuntime(f)

	seeds := []struct {
		first  []byte
		second []byte
	}{
		{[]byte("echo hi > /tmp/a\n"), []byte("cat /tmp/a\n")},
		{[]byte("mkdir -p /tmp/d\n"), []byte("ls /tmp\n")},
		{[]byte("echo first > /tmp/a\n"), []byte("echo second >> /tmp/a\ncat /tmp/a\n")},
		{[]byte("cd /tmp\n"), []byte("pwd\n")},
		{[]byte("bash -c 'echo child > /tmp/c'\n"), []byte("cat /tmp/c\n")},
		{[]byte("printf 'alpha\\nbeta\\n' > /tmp/in.txt\nsort /tmp/in.txt > /tmp/out.txt\n"), []byte("cat /tmp/out.txt\n")},
	}
	for _, seed := range seeds {
		f.Add(seed.first, seed.second)
	}

	f.Fuzz(func(t *testing.T, first []byte, second []byte) {
		if len(first)+len(second) > fuzzMaxScriptBytes {
			t.Skip()
		}

		session, err := rt.NewSession(context.Background())
		if err != nil {
			t.Fatalf("NewSession() error = %v", err)
		}

		firstResult, firstErr := runFuzzSessionScript(t, session, first)
		assertSecureFuzzOutcome(t, first, firstResult, firstErr)

		secondResult, secondErr := runFuzzSessionScript(t, session, second)
		assertSecureFuzzOutcome(t, second, secondResult, secondErr)
	})
}
