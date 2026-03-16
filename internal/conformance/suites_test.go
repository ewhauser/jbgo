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

	suites := []SuiteConfig{
		{
			Name:         "bash",
			SpecDir:      "oils",
			BinDir:       "bin",
			ManifestPath: "manifest.json",
			OracleMode:   OracleBash,
		},
		{
			Name:         "posix",
			SpecDir:      "oils",
			SpecFiles:    []string{"posix.test.sh"},
			BinDir:       "bin",
			FixtureDirs:  []string{"fixtures"},
			ManifestPath: "manifest.json",
			OracleMode:   OracleBashPosix,
		},
	}

	for _, cfg := range suites {
		t.Run(cfg.Name, func(t *testing.T) {
			t.Parallel()
			RunSuite(t, &cfg)
		})
	}
}
