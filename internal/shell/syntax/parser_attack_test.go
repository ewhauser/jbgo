package syntax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const (
	parserAttackMaxScriptBytes = 4 << 10
	parserAttackHelperTimeout  = 2 * time.Second
)

type knownParserAttack struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Script string `json:"script"`
}

type parserAttackMode struct {
	name          string
	recoverErrors int
}

func loadKnownParserAttacks(tb testing.TB) []knownParserAttack {
	tb.Helper()

	path := filepath.Join("testdata", "fuzz", "known_attacks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("ReadFile(%q) error = %v", path, err)
	}

	var attacks []knownParserAttack
	if err := json.Unmarshal(data, &attacks); err != nil {
		tb.Fatalf("Unmarshal(%q) error = %v", path, err)
	}
	if len(attacks) == 0 {
		tb.Fatalf("no known parser attacks loaded from %q", path)
	}
	return attacks
}

func TestKnownParserAttackCorpus(t *testing.T) {
	t.Parallel()

	attacks := loadKnownParserAttacks(t)
	modes := []parserAttackMode{
		{name: "default"},
		{name: "recover-errors-3", recoverErrors: 3},
	}

	for _, attack := range attacks {
		attack := attack
		t.Run(attack.Name, func(t *testing.T) {
			for lang := range langResolvedVariants.bits() {
				lang := lang
				t.Run(lang.String(), func(t *testing.T) {
					for _, mode := range modes {
						mode := mode
						t.Run(mode.name, func(t *testing.T) {
							runParserAttackHelper(t, []byte(attack.Script), lang, false, mode.recoverErrors)
						})
					}
				})
			}
		})
	}
}

func TestParserAttackHelperProcess(t *testing.T) {
	if os.Getenv("GBASH_PARSER_ATTACK_HELPER") == "" {
		t.Skip("helper process only")
	}

	script, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatalf("ReadAll(stdin) error = %v", err)
	}
	lang, err := parserAttackHelperVariant()
	if err != nil {
		t.Fatal(err)
	}
	recoverErrors, err := parserAttackHelperRecoverErrors()
	if err != nil {
		t.Fatal(err)
	}
	keepComments := os.Getenv("GBASH_PARSER_ATTACK_KEEP_COMMENTS") == "1"

	if err := exerciseParserAttack(script, lang, keepComments, recoverErrors); err != nil {
		t.Fatal(err)
	}
}

func runParserAttackHelper(t *testing.T, script []byte, lang LangVariant, keepComments bool, recoverErrors int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), parserAttackHelperTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestParserAttackHelperProcess$") //nolint:noctx // test helper
	cmd.Env = append(os.Environ(),
		"GBASH_PARSER_ATTACK_HELPER=1",
		"GBASH_PARSER_ATTACK_VARIANT="+lang.String(),
		"GBASH_PARSER_ATTACK_RECOVER="+strconv.Itoa(recoverErrors),
	)
	if keepComments {
		cmd.Env = append(cmd.Env, "GBASH_PARSER_ATTACK_KEEP_COMMENTS=1")
	}
	cmd.Stdin = bytes.NewReader(script)

	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("parser helper timed out for %s recover=%d\nscript=%q\noutput=%s", lang, recoverErrors, script, output)
	}
	if err != nil {
		t.Fatalf("parser helper failed for %s recover=%d: %v\nscript=%q\noutput=%s", lang, recoverErrors, err, script, output)
	}
}

func parserAttackHelperVariant() (LangVariant, error) {
	variantText := os.Getenv("GBASH_PARSER_ATTACK_VARIANT")
	if variantText == "" {
		return LangBash, nil
	}

	var lang LangVariant
	if err := lang.Set(variantText); err != nil {
		return 0, fmt.Errorf("invalid helper variant %q: %w", variantText, err)
	}
	if lang == LangAuto {
		return 0, fmt.Errorf("LangAuto is not supported in parser attack helper")
	}
	return lang, nil
}

func parserAttackHelperRecoverErrors() (int, error) {
	value := os.Getenv("GBASH_PARSER_ATTACK_RECOVER")
	if value == "" {
		return 0, nil
	}
	recoverErrors, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid helper recover setting %q: %w", value, err)
	}
	if recoverErrors < 0 {
		return 0, fmt.Errorf("invalid helper recover setting %q: must be >= 0", value)
	}
	return recoverErrors, nil
}

func exerciseParserAttack(script []byte, lang LangVariant, keepComments bool, recoverErrors int) error {
	opts := []ParserOption{Variant(lang), KeepComments(keepComments)}
	if recoverErrors > 0 {
		opts = append(opts, RecoverErrors(recoverErrors))
	}

	prog, parseErr := NewParser(opts...).Parse(bytes.NewReader(script), "")
	if parseErr == nil {
		if prog == nil {
			return fmt.Errorf("nil program without parse error")
		}

		Walk(prog, func(Node) bool { return true })
		if err := NewPrinter().Print(io.Discard, prog); err != nil {
			return fmt.Errorf("printer failed: %w\nscript=%q", err, script)
		}
	}
	return nil
}
