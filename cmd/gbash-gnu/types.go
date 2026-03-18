package main

type manifest struct {
	GNUVersion          string                `json:"gnu_version"`
	TarballURL          string                `json:"tarball_url"`
	TarballSHA256       string                `json:"tarball_sha256"`
	UtilityOverrides    []utilityAttribution  `json:"utility_overrides"`
	UtilityDisplayNames []utilityNameAlias    `json:"utility_display_names,omitempty"`
	AttributionRules    []testAttributionRule `json:"attribution_rules,omitempty"`
	ExpectedFailures    map[string]xfailEntry `json:"expected_failures,omitempty"`
	SkipPatterns        []skipPattern         `json:"skip_patterns"`
}

type xfailEntry struct {
	Reason string   `json:"reason"`
	GOOS   []string `json:"goos,omitempty"`
}

type utilityAttribution struct {
	Name     string   `json:"name"`
	Patterns []string `json:"patterns"`
	Skips    []string `json:"skips,omitempty"`
}

type utilityNameAlias struct {
	Name  string `json:"name"`
	Alias string `json:"alias"`
}

type testAttributionRule struct {
	Patterns []string `json:"patterns"`
	Commands []string `json:"commands"`
	Kind     string   `json:"kind"`
}

type skipPattern struct {
	Pattern string `json:"pattern"`
	Reason  string `json:"reason"`
}

type options struct {
	workDir    string
	utils      string
	tests      string
	resultsDir string
	logPath    string
	printTests bool
	exitCode   int
}

type testResult struct {
	Name                  string   `json:"name"`
	Status                string   `json:"status"`
	ReportedAs            []string `json:"reported_as,omitempty"`
	ExpectedFailureReason string   `json:"expected_failure_reason,omitempty"`
}

type testSummary struct {
	SelectedTotal      int     `json:"selected_total"`
	FilteredSkipTotal  int     `json:"filtered_skip_total,omitempty"`
	ReportedExtraTotal int     `json:"reported_extra_total,omitempty"`
	Pass               int     `json:"pass"`
	Fail               int     `json:"fail"`
	Skip               int     `json:"skip"`
	XFail              int     `json:"xfail"`
	XPass              int     `json:"xpass"`
	Error              int     `json:"error"`
	Unreported         int     `json:"unreported"`
	RunnableTotal      int     `json:"runnable_total"`
	PassPctSelected    float64 `json:"pass_pct_selected"`
	PassPctRunnable    float64 `json:"pass_pct_runnable"`
}

type utilityResult struct {
	Name         string       `json:"name"`
	Tests        []string     `json:"tests"`
	Skipped      []string     `json:"skipped,omitempty"`
	TestResults  []testResult `json:"test_results,omitempty"`
	ExtraResults []testResult `json:"extra_results,omitempty"`
	Summary      testSummary  `json:"summary"`
	ExitCode     int          `json:"exit_code"`
	Passed       bool         `json:"passed"`
	LogFile      string       `json:"log_file,omitempty"`
	LogPath      string       `json:"log_path,omitempty"`
}

type utilityTotals struct {
	Total           int     `json:"total"`
	Passed          int     `json:"passed"`
	Failed          int     `json:"failed"`
	NoRunnableTests int     `json:"no_runnable_tests"`
	PassPctTotal    float64 `json:"pass_pct_total"`
	PassPctRunnable float64 `json:"pass_pct_runnable"`
}

type runSummary struct {
	GNUVersion     string              `json:"gnu_version"`
	GeneratedAt    string              `json:"generated_at"`
	WorkDir        string              `json:"work_dir"`
	ResultsDir     string              `json:"results_dir"`
	Overall        testSummary         `json:"overall"`
	UtilitySummary utilityTotals       `json:"utility_summary"`
	Utilities      []utilityResult     `json:"utilities"`
	Suite          suiteSummary        `json:"suite"`
	Categories     []categoryResult    `json:"categories"`
	Commands       []commandCoverage   `json:"commands"`
	Coverage       coverageDebtSummary `json:"coverage"`
	ExtraResults   []testResult        `json:"extra_results,omitempty"`
}

type suiteSummary struct {
	SelectedTotal   int         `json:"selected_total"`
	FilteredTotal   int         `json:"filtered_total"`
	Pass            int         `json:"pass"`
	Fail            int         `json:"fail"`
	Skip            int         `json:"skip"`
	XFail           int         `json:"xfail"`
	XPass           int         `json:"xpass"`
	Error           int         `json:"error"`
	Unreported      int         `json:"unreported"`
	RunnableTotal   int         `json:"runnable_total"`
	PassPctSelected float64     `json:"pass_pct_selected"`
	PassPctRunnable float64     `json:"pass_pct_runnable"`
	Tests           []suiteTest `json:"tests"`
}

type suiteTest struct {
	Path                  string            `json:"path"`
	Category              string            `json:"category"`
	Status                string            `json:"status"`
	Filtered              bool              `json:"filtered,omitempty"`
	FilterReason          string            `json:"filter_reason,omitempty"`
	ReportedAs            []string          `json:"reported_as,omitempty"`
	ExpectedFailureReason string            `json:"expected_failure_reason,omitempty"`
	Attributions          []testAttribution `json:"attributions,omitempty"`
}

type testAttribution struct {
	Command string `json:"command"`
	Kind    string `json:"kind"`
}

type coverageBucket struct {
	SelectedTotal   int     `json:"selected_total"`
	FilteredTotal   int     `json:"filtered_total"`
	Pass            int     `json:"pass"`
	Fail            int     `json:"fail"`
	Skip            int     `json:"skip"`
	XFail           int     `json:"xfail"`
	XPass           int     `json:"xpass"`
	Error           int     `json:"error"`
	Unreported      int     `json:"unreported"`
	RunnableTotal   int     `json:"runnable_total"`
	PassPctSelected float64 `json:"pass_pct_selected"`
	PassPctRunnable float64 `json:"pass_pct_runnable"`
}

type categoryResult struct {
	Name    string         `json:"name"`
	Summary coverageBucket `json:"summary"`
	Tests   []suiteTest    `json:"tests,omitempty"`
}

type commandCoverage struct {
	Name          string           `json:"name"`
	CoverageState string           `json:"coverage_state"`
	Primary       coverageBucket   `json:"primary"`
	Shared        coverageBucket   `json:"shared"`
	Tests         []commandTestRef `json:"tests,omitempty"`
}

type commandTestRef struct {
	Path     string `json:"path"`
	Status   string `json:"status"`
	Kind     string `json:"kind"`
	Filtered bool   `json:"filtered,omitempty"`
}

type coverageDebtSummary struct {
	CommandTotal           int     `json:"command_total"`
	PrimaryCoveredCommands int     `json:"primary_covered_commands"`
	PrimaryPassingCommands int     `json:"primary_passing_commands"`
	SharedOnlyCommands     int     `json:"shared_only_commands"`
	FilteredOnlyCommands   int     `json:"filtered_only_commands"`
	EmptyCommands          int     `json:"empty_commands"`
	PrimaryPassPct         float64 `json:"primary_pass_pct"`
	UnmappedSelectedTests  int     `json:"unmapped_selected_tests"`
	UnmappedRunnableTests  int     `json:"unmapped_runnable_tests"`
	MultiDirectTests       int     `json:"multi_direct_tests"`
	ExtraReportedTotal     int     `json:"extra_reported_total"`
}

type makeCheckResult struct {
	ExitCode int
	Output   []byte
}

type utilityRun struct {
	Utility attributedUtility
	Tests   []string
	Skipped []string
}

type attributedUtility struct {
	Name     string
	Patterns []string
	Skips    []string
}

type runPlan struct {
	runs            []utilityRun
	selectedTests   []string
	filteredEntries map[string]string
}
