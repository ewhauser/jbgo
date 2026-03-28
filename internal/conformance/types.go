package conformance

import (
	"strings"

	gbruntime "github.com/ewhauser/gbash/internal/runtime"
)

type SpecFile struct {
	Path     string
	Metadata map[string]string
	Cases    []SpecCase
}

type SpecCase struct {
	Name            string
	Script          string
	StartLine       int
	Expectation     ExpectedResult
	OracleOverrides map[OracleMode]OracleOverride
}

type ExpectedResult struct {
	Status *int
	Stdout *string
	Stderr *string
}

type OracleOverrideKind string

const (
	OracleOverrideBug OracleOverrideKind = "BUG"
	OracleOverrideOK  OracleOverrideKind = "OK"
	OracleOverrideNI  OracleOverrideKind = "N-I"
)

type OracleOverride struct {
	Kind   OracleOverrideKind
	Status *int
	Stdout *string
	Stderr *string
}

type EntryMode string

const (
	EntryModeSkip  EntryMode = "skip"
	EntryModeXFail EntryMode = "xfail"
)

type ManifestEntry struct {
	Mode   EntryMode `json:"mode"`
	Reason string    `json:"reason"`
	GOOS   []string  `json:"goos,omitempty"`
}

type Manifest struct {
	Suites map[string]ManifestSuite `json:"suites"`
}

type ManifestSuite struct {
	Entries map[string]ManifestEntry `json:"entries"`
}

type OracleMode string

const (
	OracleBash OracleMode = "bash"
)

type SuiteConfig struct {
	Name          string
	SpecDir       string
	SpecFiles     []string
	BinDir        string
	FixtureDirs   []string
	ManifestPath  string
	OracleMode    OracleMode
	Env           map[string]string
	ExtraBinaries map[string]string
	GBashConfig   *gbruntime.Config
}

type ExecutionResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type ComparisonResult struct {
	GBash ExecutionResult
	Bash  ExecutionResult
}

type CaseOutcome int

const (
	CaseOutcomePass CaseOutcome = iota
	CaseOutcomeSkip
	CaseOutcomeExpectedFailure
	CaseOutcomeUnexpectedPass
	CaseOutcomeFailure
)

func (m EntryMode) valid() bool {
	return m == EntryModeSkip || m == EntryModeXFail
}

func normalizeKey(value string) string {
	return filepathSlash(strings.TrimSpace(value))
}

func filepathSlash(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}
