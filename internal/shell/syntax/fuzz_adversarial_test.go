package syntax

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

const parserFuzzFixtureSeedLimit = 96

var parserFuzzRecoverOptions = []int{0, 1, 3}

type parserFuzzCursor struct {
	data []byte
	idx  int
}

func newParserFuzzCursor(data []byte) *parserFuzzCursor {
	if len(data) > parserAttackMaxScriptBytes {
		data = data[:parserAttackMaxScriptBytes]
	}
	return &parserFuzzCursor{data: data}
}

func (c *parserFuzzCursor) next() byte {
	if len(c.data) == 0 {
		c.idx++
		return byte((c.idx*29 + 11) % 251)
	}
	b := c.data[c.idx%len(c.data)]
	c.idx++
	return b
}

func (c *parserFuzzCursor) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(c.next()) % n
}

func (c *parserFuzzCursor) Bool() bool {
	return c.Intn(2) == 1
}

func (c *parserFuzzCursor) token() string {
	tokens := []string{
		"(", ")", "{", "}", "$(", "$(( ", "${", "[[ ", "]]", "`",
		";", "&&", "||", "|", "\n", "\r\n", "<<EOF\n", "EOF\n",
		"foo", "bar", "x", "1", "$x", "${x}", "$(echo x)", "$((1+2))",
		"'q'", "\"dq\"", "[", "]", "<(", ">(",
	}
	return tokens[c.Intn(len(tokens))]
}

func (c *parserFuzzCursor) chunk(maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	size := 1 + c.Intn(maxTokens)
	var b strings.Builder
	for range size {
		b.WriteString(c.token())
		if c.Bool() {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func FuzzParseAdversarial(f *testing.F) {
	seedParserAttackFuzzer(f)

	f.Fuzz(func(t *testing.T, script []byte, rawVariant uint8, keepComments bool, rawRecover uint8) {
		if len(script) > parserAttackMaxScriptBytes {
			t.Skip()
		}
		if err := exerciseParserAttack(script, parserFuzzVariant(rawVariant), keepComments, parserFuzzRecover(rawRecover)); err != nil {
			t.Fatalf("exerciseParserAttack() error = %v", err)
		}
	})
}

func FuzzParseAttackMutations(f *testing.F) {
	attacks := loadKnownParserAttacks(f)
	for _, attack := range attacks {
		f.Add([]byte(attack.Name), uint8(LangBash), false, uint8(0))
	}
	seedParserAttackFixtures(f, false)

	f.Fuzz(func(t *testing.T, raw []byte, rawVariant uint8, keepComments bool, rawRecover uint8) {
		cursor := newParserFuzzCursor(raw)
		attack := attacks[cursor.Intn(len(attacks))]
		mutated := mutateParserAttackScript(attack.Script, cursor)
		if err := exerciseParserAttack([]byte(mutated), parserFuzzVariant(rawVariant), keepComments, parserFuzzRecover(rawRecover)); err != nil {
			t.Fatalf("exerciseParserAttack() error = %v\nattack=%s\nmutated=%q", err, attack.Name, mutated)
		}
	})
}

func seedParserAttackFuzzer(f *testing.F) {
	attacks := loadKnownParserAttacks(f)
	for _, attack := range attacks {
		f.Add([]byte(attack.Script), uint8(LangBash), false, uint8(0))
		f.Add([]byte(attack.Script), uint8(LangBash), true, uint8(2))
	}
	seedParserAttackFixtures(f, true)
}

func seedParserAttackFixtures(f *testing.F, includeValidFixtures bool) {
	for _, c := range errorCases {
		if len(c.in) == 0 || len(c.in) > parserAttackMaxScriptBytes {
			continue
		}
		f.Add([]byte(c.in), uint8(LangBash), true, uint8(2))
	}

	if !includeValidFixtures {
		return
	}

	step := max(1, len(fileTests)/parserFuzzFixtureSeedLimit)
	added := 0
	for i := 0; i < len(fileTests) && added < parserFuzzFixtureSeedLimit; i += step {
		if len(fileTests[i].inputs) == 0 {
			continue
		}
		in := fileTests[i].inputs[0]
		if len(in) == 0 || len(in) > parserAttackMaxScriptBytes {
			continue
		}
		f.Add([]byte(in), uint8(LangBash), false, uint8(0))
		added++
	}
}

func parserFuzzVariant(raw uint8) LangVariant {
	variants := []LangVariant{
		LangBash,
		LangPOSIX,
		LangMirBSDKorn,
		LangBats,
		LangZsh,
	}
	return variants[int(raw)%len(variants)]
}

func parserFuzzRecover(raw uint8) int {
	return parserFuzzRecoverOptions[int(raw)%len(parserFuzzRecoverOptions)]
}

func mutateParserAttackScript(base string, cursor *parserFuzzCursor) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "echo attack"
	}

	var mutated string
	switch cursor.Intn(10) {
	case 0:
		mutated = "(" + base + "\n)"
	case 1:
		mutated = "{ " + base + "\n; }"
	case 2:
		mutated = "echo $(" + base + "\n)"
	case 3:
		mutated = "echo $(( " + base + " ))\n"
	case 4:
		mutated = "echo ${value:-" + base + "}\n"
	case 5:
		mutated = "[[ " + base + " ]]\n"
	case 6:
		mutated = "echo `" + base + "\n"
	case 7:
		mutated = strings.ReplaceAll(base, "\n", "\r\n") + "\r\n" + cursor.chunk(6)
	case 8:
		chunk := cursor.chunk(8)
		if chunk == "" {
			chunk = ";;"
		}
		mutated = base + "\n" + strings.Repeat(chunk, 1+cursor.Intn(12))
	default:
		mutated = truncateParserAttackAtBoundary(base, cursor)
	}

	if cursor.Bool() {
		mutated = mutated + "\n" + strings.Repeat(cursor.token(), 1+cursor.Intn(8))
	}
	return clampParserAttackScript(mutated)
}

func truncateParserAttackAtBoundary(base string, cursor *parserFuzzCursor) string {
	prefixes := []string{
		"$", "$(", "${", "$(( ", "[[ ", "`", "<<EOF\n",
		"if true; then\n", "case x in\n", "for x in ", "(\n", "{\n",
	}
	prefix := prefixes[cursor.Intn(len(prefixes))]
	if len(base) == 0 {
		return prefix
	}
	cut := cursor.Intn(len(base) + 1)
	return prefix + base[:cut]
}

func clampParserAttackScript(script string) string {
	data := []byte(script)
	if len(data) > parserAttackMaxScriptBytes {
		data = data[:parserAttackMaxScriptBytes]
	}
	return string(bytes.TrimSpace(data)) + "\n"
}

func TestMutateParserAttackScriptBounded(t *testing.T) {
	t.Parallel()

	mutated := mutateParserAttackScript("echo hi\n", newParserFuzzCursor([]byte("mutate")))
	if len(mutated) == 0 {
		t.Fatal("mutated script was empty")
	}
	if len(mutated) > parserAttackMaxScriptBytes+1 {
		t.Fatalf("mutated script exceeded max size: %d", len(mutated))
	}
}

func TestParserFuzzRecoverOptions(t *testing.T) {
	t.Parallel()

	for _, recoverErrors := range parserFuzzRecoverOptions {
		if recoverErrors < 0 {
			t.Fatalf("invalid recover option %d", recoverErrors)
		}
	}
	if got := fmt.Sprint(parserFuzzRecoverOptions); got == "[]" {
		t.Fatal("expected non-empty recover option list")
	}
}
