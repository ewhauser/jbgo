package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	htmltemplate "html/template"
	"math"
	"os"
	"path/filepath"
	"strings"
	texttemplate "text/template"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var indexTemplate = htmltemplate.Must(htmltemplate.New("index.html.tmpl").Funcs(htmltemplate.FuncMap{
	"formatPercent":   formatPercent,
	"testStatusClass": testStatusClass,
}).ParseFS(templateFS, "templates/index.html.tmpl"))

var badgeTemplate = texttemplate.Must(texttemplate.New("badge.svg.tmpl").ParseFS(templateFS, "templates/badge.svg.tmpl"))

type options struct {
	summaryPath string
	outputDir   string
}

type runSummary struct {
	GNUVersion     string              `json:"gnu_version"`
	GeneratedAt    string              `json:"generated_at"`
	WorkDir        string              `json:"work_dir,omitempty"`
	ResultsDir     string              `json:"results_dir,omitempty"`
	Overall        testSummary         `json:"overall"`
	UtilitySummary utilityTotals       `json:"utility_summary"`
	Utilities      []utilityResult     `json:"utilities"`
	Suite          suiteSummary        `json:"suite"`
	Categories     []categoryResult    `json:"categories"`
	Commands       []commandCoverage   `json:"commands"`
	Coverage       coverageDebtSummary `json:"coverage"`
	ExtraResults   []testResult        `json:"extra_results,omitempty"`
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

type utilityTotals struct {
	Total           int     `json:"total"`
	Passed          int     `json:"passed"`
	Failed          int     `json:"failed"`
	NoRunnableTests int     `json:"no_runnable_tests"`
	PassPctTotal    float64 `json:"pass_pct_total"`
	PassPctRunnable float64 `json:"pass_pct_runnable"`
}

type utilityResult struct {
	Name         string       `json:"name"`
	Tests        []string     `json:"tests,omitempty"`
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

type testResult struct {
	Name                  string   `json:"name"`
	Status                string   `json:"status"`
	ReportedAs            []string `json:"reported_as,omitempty"`
	ExpectedFailureReason string   `json:"expected_failure_reason,omitempty"`
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
	Name     string            `json:"name"`
	Summary  coverageBucket    `json:"summary"`
	Tests    []suiteTest       `json:"tests,omitempty"`
	Counts   string            `json:"-"`
	RowNote  string            `json:"-"`
	Segments []progressSegment `json:"-"`
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

type indexView struct {
	Summary        runSummary
	InScopePassPct float64
	SelectedDetail string
	RunnableDetail string
}

type progressSegment struct {
	Class string
	Width string
}

type badgeView struct {
	TotalWidth   int
	LabelWidth   int
	MessageWidth int
	Label        string
	Message      string
	Color        string
	LabelX       int
	MessageX     int
}

func main() {
	opts, err := parseOptions()
	if err != nil {
		fatalf("parse options: %v", err)
	}
	summary, err := loadSummary(opts.summaryPath)
	if err != nil {
		fatalf("load summary: %v", err)
	}
	if err := writeReport(opts.outputDir, summary); err != nil {
		fatalf("write report: %v", err)
	}
}

func parseOptions() (options, error) {
	var opts options
	fs := flag.NewFlagSet("compat-report", flag.ContinueOnError)
	fs.StringVar(&opts.summaryPath, "summary", "", "path to GNU compat summary.json")
	fs.StringVar(&opts.outputDir, "output", "", "directory for index.html and badge.svg")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if strings.TrimSpace(opts.summaryPath) == "" {
		return options{}, fmt.Errorf("--summary is required")
	}
	if strings.TrimSpace(opts.outputDir) == "" {
		return options{}, fmt.Errorf("--output is required")
	}
	return opts, nil
}

func loadSummary(path string) (*runSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var summary runSummary
	if err := decoder.Decode(&summary); err != nil {
		return nil, err
	}
	prepareSummaryView(&summary)
	return &summary, nil
}

func writeReport(outputDir string, summary *runSummary) error {
	prepareSummaryView(summary)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	indexData, err := renderIndex(summary)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "index.html"), indexData, 0o644); err != nil {
		return err
	}
	badgeData, err := renderBadge(summary)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "badge.svg"), badgeData, 0o644); err != nil {
		return err
	}
	return nil
}

func renderIndex(summary *runSummary) ([]byte, error) {
	var buf bytes.Buffer
	inScopeTotal := suiteInScopeTotal(summary)
	view := indexView{
		Summary:        *summary,
		InScopePassPct: suiteInScopePassPct(summary),
		SelectedDetail: fmt.Sprintf("%d in scope, %d out of scope, %d passing", inScopeTotal, summary.Suite.FilteredTotal, summary.Suite.Pass),
		RunnableDetail: fmt.Sprintf("%d runnable, %d passing, %d skipped, %d failing", summary.Suite.RunnableTotal, summary.Suite.Pass, summary.Suite.Skip+summary.Suite.XFail, summary.Suite.Fail+summary.Suite.Error+summary.Suite.XPass+summary.Suite.Unreported),
	}
	if err := indexTemplate.ExecuteTemplate(&buf, "index.html.tmpl", view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func prepareSummaryView(summary *runSummary) {
	for i := range summary.Categories {
		summary.Categories[i].Counts = bucketCounts(&summary.Categories[i].Summary)
		summary.Categories[i].RowNote = bucketRowNote(&summary.Categories[i].Summary)
		summary.Categories[i].Segments = bucketSegments(&summary.Categories[i].Summary)
	}
}

func renderBadge(summary *runSummary) ([]byte, error) {
	var buf bytes.Buffer
	view := buildBadgeView(summary)
	if err := badgeTemplate.ExecuteTemplate(&buf, "badge.svg.tmpl", view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildBadgeView(summary *runSummary) badgeView {
	label := "compat"
	message := formatPercent(suiteInScopePassPct(summary))
	if suiteInScopeTotal(summary) == 0 {
		message = "n/a"
	}
	labelWidth := badgeTextWidth(label)
	messageWidth := badgeTextWidth(message)
	return badgeView{
		TotalWidth:   labelWidth + messageWidth,
		LabelWidth:   labelWidth,
		MessageWidth: messageWidth,
		Label:        label,
		Message:      message,
		Color:        badgeColor(summary),
		LabelX:       labelWidth / 2,
		MessageX:     labelWidth + messageWidth/2,
	}
}

func badgeTextWidth(text string) int {
	return 10 + (len(text) * 7)
}

func badgeColor(summary *runSummary) string {
	passPct := suiteInScopePassPct(summary)
	if suiteInScopeTotal(summary) == 0 {
		return "#9f9f9f"
	}
	switch {
	case passPct >= 90:
		return "#4c1"
	case passPct >= 70:
		return "#dfb317"
	default:
		return "#e05d44"
	}
}

func bucketCounts(bucket *coverageBucket) string {
	return fmt.Sprintf("%d / %d / %d", bucket.Pass, bucket.Skip+bucket.XFail, bucket.Fail+bucket.Error+bucket.XPass+bucket.Unreported)
}

func bucketRowNote(bucket *coverageBucket) string {
	if bucket.FilteredTotal == 0 {
		return fmt.Sprintf("%d tests", bucket.SelectedTotal)
	}
	return fmt.Sprintf("%d tests, %d out of scope", bucket.SelectedTotal, bucket.FilteredTotal)
}

func bucketSegments(bucket *coverageBucket) []progressSegment {
	total := bucket.SelectedTotal
	if total == 0 {
		return nil
	}
	segments := []struct {
		class string
		count int
	}{
		{class: "good", count: bucket.Pass},
		{class: "oos", count: bucket.FilteredTotal},
		{class: "warn", count: bucket.Skip + bucket.XFail},
		{class: "bad", count: bucket.Fail + bucket.Error + bucket.XPass + bucket.Unreported},
	}
	out := make([]progressSegment, 0, len(segments))
	for _, segment := range segments {
		if segment.count == 0 {
			continue
		}
		out = append(out, progressSegment{
			Class: segment.class,
			Width: fmt.Sprintf("%.4f%%", (float64(segment.count)/float64(total))*100),
		})
	}
	return out
}

func testStatusClass(status string, filtered bool) string {
	if filtered {
		return "oos"
	}
	switch status {
	case "pass":
		return "good"
	case "skip", "xfail":
		return "warn"
	case "filtered":
		return "warn"
	case "fail", "error", "xpass", "unreported":
		return "bad"
	default:
		return "na"
	}
}

func formatPercent(value float64) string {
	if value == 0 {
		return "0%"
	}
	if value == math.Trunc(value) {
		return fmt.Sprintf("%.0f%%", value)
	}
	formatted := fmt.Sprintf("%.2f", value)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	return formatted + "%"
}

func suiteInScopeTotal(summary *runSummary) int {
	total := summary.Suite.SelectedTotal - summary.Suite.FilteredTotal
	if total < 0 {
		return 0
	}
	return total
}

func suiteInScopePassPct(summary *runSummary) float64 {
	total := suiteInScopeTotal(summary)
	if total == 0 {
		return 0
	}
	return math.Round((float64(summary.Suite.Pass)/float64(total))*10000) / 100
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "compat-report: "+format+"\n", args...)
	os.Exit(1)
}
