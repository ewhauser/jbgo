package python

import (
	"slices"
	"strings"
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

func TestRewritePrintCallsRewritesBarePrintOnly(t *testing.T) {
	t.Parallel()

	source := "" +
		"print('top')\n" +
		"message = \"print('inside string')\"\n" +
		"obj.print('method')\n"

	rewritten, didRewrite := rewritePrintCalls(source)
	if !didRewrite {
		t.Fatalf("rewritePrintCalls() did not rewrite %q", source)
	}
	if !strings.Contains(rewritten, "__gbash_print('top')") {
		t.Fatalf("rewritePrintCalls() = %q, want bare print rewritten", rewritten)
	}
	if strings.Contains(rewritten, "obj.__gbash_print") {
		t.Fatalf("rewritePrintCalls() = %q, want method access preserved", rewritten)
	}
	if !strings.Contains(rewritten, "\"print('inside string')\"") {
		t.Fatalf("rewritePrintCalls() = %q, want string literal preserved", rewritten)
	}
}

func TestRewritePrintCallsSkipsReboundPrint(t *testing.T) {
	t.Parallel()

	source := "" +
		"print = logger.info\n" +
		"print('msg')\n"

	rewritten, didRewrite := rewritePrintCalls(source)
	if didRewrite {
		t.Fatalf("rewritePrintCalls() unexpectedly rewrote %q into %q", source, rewritten)
	}
	if rewritten != source {
		t.Fatalf("rewritePrintCalls() = %q, want original source", rewritten)
	}
}

func TestInstrumentSourceForPrintKeepsPrefixedDocstringsBeforeFutureImports(t *testing.T) {
	t.Parallel()

	source := "r\"\"\"docs\"\"\"\nfrom __future__ import annotations\nprint('x')\n"

	instrumented := instrumentSourceForPrint(source)
	prefix := "r\"\"\"docs\"\"\"\nfrom __future__ import annotations\n"
	if !strings.HasPrefix(instrumented, prefix) {
		t.Fatalf("instrumentSourceForPrint() = %q, want prefix %q", instrumented, prefix)
	}
	if !strings.Contains(instrumented, pythonPrintPrelude) {
		t.Fatalf("instrumentSourceForPrint() = %q, want injected prelude", instrumented)
	}
}
