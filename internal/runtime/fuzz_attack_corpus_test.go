package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type knownAttack struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Script string `json:"script"`
}

func loadKnownAttacks(tb testing.TB) []knownAttack {
	tb.Helper()

	path := filepath.Join("testdata", "fuzz", "known_attacks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var attacks []knownAttack
	if err := json.Unmarshal(data, &attacks); err != nil {
		tb.Fatalf("Unmarshal(%q) error = %v", path, err)
	}
	if len(attacks) == 0 {
		tb.Fatalf("no known attacks loaded from %q", path)
	}
	return attacks
}

func TestKnownAttackCorpus(t *testing.T) {
	rt := newFuzzRuntime(t)
	attacks := loadKnownAttacks(t)

	for _, attack := range attacks {
		t.Run(attack.Name, func(t *testing.T) {
			result, err := runFuzzScript(t, rt, []byte(attack.Script))
			assertSecureFuzzOutcome(t, []byte(attack.Script), result, err)
		})
	}
}

func FuzzAttackMutations(f *testing.F) {
	attacks := loadKnownAttacks(f)
	for _, attack := range attacks {
		f.Add([]byte(attack.Name))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		rt := newFuzzRuntime(t)
		cursor := newFuzzCursor(raw)
		attack := attacks[cursor.Intn(len(attacks))]
		mutated := mutateAttackScript(attack.Script, cursor)
		result, err := runFuzzScript(t, rt, []byte(mutated))
		assertSecureFuzzOutcome(t, []byte(mutated), result, err)
	})
}

func mutateAttackScript(base string, cursor *fuzzCursor) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "cat /etc/passwd"
	}

	switch cursor.Intn(4) {
	case 0:
		return base + "\n"
	case 1:
		return "if true; then " + base + "; fi\n"
	case 2:
		return "(" + base + ")\n"
	default:
		return "printf 'seed\\n' >/tmp/attack-seed.txt\n" + base + "\ncat /tmp/attack-seed.txt\n"
	}
}
