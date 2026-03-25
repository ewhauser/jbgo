package python

import (
	"slices"
	"testing"

	gbruntime "github.com/ewhauser/gbash"
)

func TestRegisterAddsPythonAndPython3Commands(t *testing.T) {
	t.Parallel()

	registry := newPythonRegistry(t)
	for _, name := range []string{"python", "python3"} {
		if !slices.Contains(registry.Names(), name) {
			t.Fatalf("Names() missing %q: %v", name, registry.Names())
		}
	}
	for _, name := range []string{"python", "python3"} {
		if slices.Contains(gbruntime.DefaultRegistry().Names(), name) {
			t.Fatalf("DefaultRegistry() unexpectedly contains %q", name)
		}
	}
}

func TestSourceReferencesBarePrintDetectsDirectCalls(t *testing.T) {
	t.Parallel()

	if !sourceReferencesBarePrint("print('x')\n") {
		t.Fatal("sourceReferencesBarePrint() = false, want true for direct print call")
	}
	if !sourceReferencesBarePrint("alias = print\nalias('x')\n") {
		t.Fatal("sourceReferencesBarePrint() = false, want true for bare print reference")
	}
}

func TestSourceReferencesBarePrintIgnoresMethodsStringsAndComments(t *testing.T) {
	t.Parallel()

	source := "" +
		"obj.print('method')\n" +
		"text = \"print('inside string')\"\n" +
		"# print('inside comment')\n"

	if sourceReferencesBarePrint(source) {
		t.Fatalf("sourceReferencesBarePrint() = true, want false for %q", source)
	}
}
