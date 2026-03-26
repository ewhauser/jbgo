package builtins

import "testing"

func TestCPParseUpdateModeCompatibility(t *testing.T) {
	t.Parallel()

	if got := cpParseUpdateModeValue("older", true); got != cpUpdateOlder {
		t.Fatalf("cpParseUpdateModeValue(\"older\", true) = %v, want %v", got, cpUpdateOlder)
	}
	if got := cpParseUpdateMode(cpParsedUpdate{value: "none-fail", hasExplicitValue: true}); got != cpUpdateNoneFail {
		t.Fatalf("cpParseUpdateMode(cpParsedUpdate{none-fail}) = %v, want %v", got, cpUpdateNoneFail)
	}
	if got := cpParseUpdateMode(cpParsedUpdate{}); got != cpUpdateOlder {
		t.Fatalf("cpParseUpdateMode(cpParsedUpdate{}) = %v, want %v", got, cpUpdateOlder)
	}
}
