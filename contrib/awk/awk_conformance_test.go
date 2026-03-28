package awk

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

func TestAWKConformance(t *testing.T) {
	t.Parallel()

	if os.Getenv("GBASH_RUN_AWK_CONFORMANCE") != "1" {
		t.Skip("set GBASH_RUN_AWK_CONFORMANCE=1 to run the awk parity corpus")
	}

	cfg := newAWKConformanceSuiteConfig(t, awkConformanceOptions{
		specDir:          awkPackagePath("conformance"),
		manifestPath:     awkPackagePath("conformance", "manifest.json"),
		includeAWKOracle: true,
	})
	conformance.RunSuite(t, &cfg)
}

func TestAWKConformanceManifestSkipAndXFail(t *testing.T) {
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
    "awk": {
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

	cfg := newAWKConformanceSuiteConfig(t, awkConformanceOptions{
		specDir:      specDir,
		manifestPath: manifestPath,
	})
	if !t.Run("suite", func(t *testing.T) {
		conformance.RunSuite(t, &cfg)
	}) {
		t.Fatal("awk suite should honor local manifest skip and xfail entries")
	}
}

type awkConformanceOptions struct {
	specDir          string
	manifestPath     string
	includeAWKOracle bool
}

func newAWKConformanceSuiteConfig(tb testing.TB, opts awkConformanceOptions) conformance.SuiteConfig {
	tb.Helper()

	cfg := conformance.SuiteConfig{
		Name:         "awk",
		SpecDir:      opts.specDir,
		ManifestPath: opts.manifestPath,
		OracleMode:   conformance.OracleBash,
		GBashConfig: &internalruntime.Config{
			Registry: newAWKRegistry(tb),
		},
	}
	if opts.includeAWKOracle {
		cfg.ExtraBinaries = map[string]string{
			"awk": testutil.RequireNixAWK(tb),
		}
	}
	return cfg
}

func awkPackagePath(parts ...string) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join(parts...)
	}

	pathParts := append([]string{filepath.Dir(file)}, parts...)
	return filepath.Join(pathParts...)
}
