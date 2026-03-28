package jq

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ewhauser/gbash/internal/conformance"
	internalruntime "github.com/ewhauser/gbash/internal/runtime"
	"github.com/ewhauser/gbash/internal/testutil"
)

func TestJQConformance(t *testing.T) {
	t.Parallel()

	if os.Getenv("GBASH_RUN_JQ_CONFORMANCE") != "1" {
		t.Skip("set GBASH_RUN_JQ_CONFORMANCE=1 to run the jq parity corpus")
	}

	cfg := newJQConformanceSuiteConfig(t, jqConformanceOptions{
		specDir:         jqPackagePath("conformance"),
		manifestPath:    jqPackagePath("conformance", "manifest.json"),
		includeJQOracle: true,
	})
	conformance.RunSuite(t, &cfg)
}

func TestJQConformanceManifestSkipAndXFail(t *testing.T) {
	bashPath := testutil.RequireNixBashOrSkip(t)
	t.Setenv("GBASH_CONFORMANCE_BASH", bashPath)

	specDir := filepath.Join(t.TempDir(), "specs")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", specDir, err)
	}

	specPath := filepath.Join(specDir, "manifest.test.sh")
	if err := os.WriteFile(specPath, []byte(""+
		"#### expected xfail\n"+
		"printf '%s\\n' \"${BASH_VERSION:-}\"\n"+
		"\n"+
		"#### expected skip\n"+
		"printf '%s\\n' \"${BASH_VERSION:-}\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", specPath, err)
	}

	specRelPath := filepath.ToSlash(filepath.Join(filepath.Base(specDir), filepath.Base(specPath)))
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	manifest := fmt.Sprintf(`{
  "suites": {
    "jq": {
      "entries": {
        %q: { "mode": "xfail", "reason": "known mismatch" },
        %q: { "mode": "skip", "reason": "case intentionally skipped" }
      }
    }
  }
}
`, specRelPath+"::expected xfail", specRelPath+"::expected skip")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", manifestPath, err)
	}

	cfg := newJQConformanceSuiteConfig(t, jqConformanceOptions{
		specDir:      specDir,
		manifestPath: manifestPath,
	})
	if !t.Run("suite", func(t *testing.T) {
		conformance.RunSuite(t, &cfg)
	}) {
		t.Fatal("jq suite should honor local manifest skip and xfail entries")
	}
}

type jqConformanceOptions struct {
	specDir         string
	manifestPath    string
	includeJQOracle bool
}

func newJQConformanceSuiteConfig(tb testing.TB, opts jqConformanceOptions) conformance.SuiteConfig {
	tb.Helper()

	cfg := conformance.SuiteConfig{
		Name:         "jq",
		SpecDir:      opts.specDir,
		ManifestPath: opts.manifestPath,
		OracleMode:   conformance.OracleBash,
		GBashConfig: &internalruntime.Config{
			Registry: newJQRegistry(tb),
		},
	}
	if opts.includeJQOracle {
		cfg.ExtraBinaries = map[string]string{
			"jq": testutil.RequireNixJQ(tb),
		}
	}
	return cfg
}

func jqPackagePath(parts ...string) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join(parts...)
	}

	pathParts := append([]string{filepath.Dir(file)}, parts...)
	return filepath.Join(pathParts...)
}
