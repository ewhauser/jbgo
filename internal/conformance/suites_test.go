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
	}{
		{
			name: "bash",
			build: func(t *testing.T) SuiteConfig {
				t.Helper()
				return SuiteConfig{
					Name:         "bash",
					SpecDir:      "oils",
					BinDir:       "bin",
					FixtureDirs:  []string{"fixtures/spec"},
					ManifestPath: "manifest.json",
					OracleMode:   OracleBash,
				}
			},
		},
		{
			name:  "curl",
			build: newCurlSuiteConfig,
		},
	}

	for _, suite := range suites {
		t.Run(suite.name, func(t *testing.T) {
			t.Parallel()
			cfg := suite.build(t)
			RunSuite(t, &cfg)
		})
	}
}
