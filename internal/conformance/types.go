package conformance

import "strings"

type SpecFile struct {
	Path     string
	Metadata map[string]string
	Cases    []SpecCase
}

type SpecCase struct {
	Name      string
	Script    string
	StartLine int
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
	OracleBash      OracleMode = "bash"
	OracleBashPosix OracleMode = "bash-posix"
)

type SuiteConfig struct {
	Name         string
	SpecDir      string
	SpecFiles    []string
	BinDir       string
	FixtureDirs  []string
	ManifestPath string
	OracleMode   OracleMode
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
