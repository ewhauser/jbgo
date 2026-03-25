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
