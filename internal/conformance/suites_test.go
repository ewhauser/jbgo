package conformance

import (
	"os"
	"testing"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	if os.Getenv("GBASH_RUN_CONFORMANCE") != "1" {
		t.Skip("set GBASH_RUN_CONFORMANCE=1 to run the full vendored conformance corpus")
	}

	suites := []struct {
		name  string
		build func(*testing.T) SuiteConfig
	}{}

	for _, mode := range []OracleMode{
		OracleBash,
		OracleDash,
		OracleMksh,
		OracleZsh,
		OracleAsh,
		OracleYash,
		OracleOsh,
		OracleKsh,
	} {
		suites = append(suites, struct {
			name  string
			build func(*testing.T) SuiteConfig
		}{
			name: string(mode),
			build: func(t *testing.T) SuiteConfig {
				t.Helper()
				return newShellSuiteConfig(mode)
			},
		})
	}

	suites = append(suites, struct {
		name  string
		build func(*testing.T) SuiteConfig
	}{
		name:  "curl",
		build: newCurlSuiteConfig,
	})

	for _, suite := range suites {
		t.Run(suite.name, func(t *testing.T) {
			t.Parallel()
			cfg := suite.build(t)
			RunSuite(t, &cfg)
		})
	}
}

func newShellSuiteConfig(mode OracleMode) SuiteConfig {
	return SuiteConfig{
		Name:         string(mode),
		SpecDir:      "oils",
		BinDir:       "bin",
		FixtureDirs:  []string{"fixtures/spec"},
		ManifestPath: "manifest.json",
		OracleMode:   mode,
	}
}
