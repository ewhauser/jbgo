package main

type manifest struct {
	GNUVersion    string            `json:"gnu_version"`
	TarballURL    string            `json:"tarball_url"`
	TarballSHA256 string            `json:"tarball_sha256"`
	Utilities     []utilityManifest `json:"utilities"`
	SkipPatterns  []skipPattern     `json:"skip_patterns"`
}

type utilityManifest struct {
	Name     string   `json:"name"`
	Patterns []string `json:"patterns"`
	Skips    []string `json:"skips,omitempty"`
}

type skipPattern struct {
	Pattern string `json:"pattern"`
	Reason  string `json:"reason"`
}

type options struct {
	cacheDir                  string
	gbashBin                  string
	utils                     string
	tests                     string
	resultsDir                string
	preparedBuildArchive      string
	writePreparedBuildArchive string
	setupOnly                 bool
	keepWorkdir               bool
}

type testResult struct {
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	ReportedAs []string `json:"reported_as,omitempty"`
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
	Inactive     bool         `json:"inactive,omitempty"`
	Tests        []string     `json:"tests"`
	Skipped      []string     `json:"skipped,omitempty"`
	TestResults  []testResult `json:"test_results,omitempty"`
	ExtraResults []testResult `json:"extra_results,omitempty"`
	Summary      testSummary  `json:"summary"`
	ExitCode     int          `json:"exit_code"`
	Passed       bool         `json:"passed"`
	LogFile      string       `json:"log_file,omitempty"`
	LogPath      string       `json:"log_path,omitempty"`
	Reason       string       `json:"reason,omitempty"`
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
	GNUVersion     string          `json:"gnu_version"`
	GeneratedAt    string          `json:"generated_at"`
	WorkDir        string          `json:"work_dir"`
	ResultsDir     string          `json:"results_dir"`
	Overall        testSummary     `json:"overall"`
	UtilitySummary utilityTotals   `json:"utility_summary"`
	Utilities      []utilityResult `json:"utilities"`
}

type makeCheckResult struct {
	ExitCode int
	Output   []byte
}

type utilityRun struct {
	Utility utilityManifest
	Tests   []string
	Skipped []string
}

const sourceCacheVersion = "2"
