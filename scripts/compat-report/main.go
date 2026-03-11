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
	"formatPercent":     formatPercent,
	"formatPercentOrNA": formatPercentOrNA,
}).ParseFS(templateFS, "templates/index.html.tmpl"))

var badgeTemplate = texttemplate.Must(texttemplate.New("badge.svg.tmpl").ParseFS(templateFS, "templates/badge.svg.tmpl"))

type options struct {
	summaryPath string
	outputDir   string
}

type runSummary struct {
	GNUVersion     string          `json:"gnu_version"`
	GeneratedAt    string          `json:"generated_at"`
	Overall        testSummary     `json:"overall"`
	UtilitySummary utilityTotals   `json:"utility_summary"`
	Utilities      []utilityResult `json:"utilities"`
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
	Name     string      `json:"name"`
	Inactive bool        `json:"inactive,omitempty"`
	Skipped  []string    `json:"skipped,omitempty"`
	Summary  testSummary `json:"summary"`
	LogFile  string      `json:"log_file,omitempty"`
	Reason   string      `json:"reason,omitempty"`
}

type indexView struct {
	Summary              runSummary
	Rows                 []indexUtilityRow
	CommandSummary       renderedUtilityTotals
	OverallDetail        string
	RunnableDetail       string
	CommandDetail        string
	FilteredDetail       string
	CommandPassPercent   float64
	CommandPassPercentNA bool
}

type renderedUtilityTotals struct {
	RunnablePassed int
	RunnableFailed int
	SkipOnly       int
	Empty          int
	PassPct        float64
}

type indexUtilityRow struct {
	Name         string
	RowClass     string
	Percent      string
	PercentClass string
	Selected     string
	Pass         string
	Fail         string
	Skip         string
	XFail        string
	XPass        string
	Error        string
	Unreported   string
	LogFile      string
	Notes        string
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
	var summary runSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func writeReport(outputDir string, summary *runSummary) error {
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
	commandSummary := summarizeRenderedUtilities(summary.Utilities)
	view := indexView{
		Summary:              *summary,
		Rows:                 buildUtilityRows(summary.Utilities),
		CommandSummary:       commandSummary,
		OverallDetail:        fmt.Sprintf("%d selected, %d pass, %d fail, %d skip", summary.Overall.SelectedTotal, summary.Overall.Pass, summary.Overall.Fail, summary.Overall.Skip),
		RunnableDetail:       fmt.Sprintf("%d runnable, %d pass, %d fail", summary.Overall.RunnableTotal, summary.Overall.Pass, summary.Overall.Fail),
		CommandDetail:        fmt.Sprintf("%d passed, %d failed, %d skip-only, %d empty", commandSummary.RunnablePassed, commandSummary.RunnableFailed, commandSummary.SkipOnly, commandSummary.Empty),
		FilteredDetail:       fmt.Sprintf("%d manifest-level skips, %d extra reported results", summary.Overall.FilteredSkipTotal, summary.Overall.ReportedExtraTotal),
		CommandPassPercent:   commandSummary.PassPct,
		CommandPassPercentNA: commandSummary.RunnablePassed+commandSummary.RunnableFailed == 0,
	}
	if err := indexTemplate.ExecuteTemplate(&buf, "index.html.tmpl", view); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
	message := formatPercent(summary.Overall.PassPctSelected)
	if summary.Overall.SelectedTotal == 0 {
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

func summarizeRenderedUtilities(results []utilityResult) renderedUtilityTotals {
	var totals renderedUtilityTotals
	for i := range results {
		summary := results[i].Summary
		switch {
		case results[i].Inactive:
			continue
		case summary.SelectedTotal == 0:
			totals.Empty++
		case summary.RunnableTotal == 0:
			totals.SkipOnly++
		case summary.Pass == summary.RunnableTotal && summary.Fail == 0 && summary.XPass == 0 && summary.Error == 0 && summary.Unreported == 0:
			totals.RunnablePassed++
		default:
			totals.RunnableFailed++
		}
	}
	totals.PassPct = percentage(float64(totals.RunnablePassed), float64(totals.RunnablePassed+totals.RunnableFailed))
	return totals
}

func buildUtilityRows(results []utilityResult) []indexUtilityRow {
	rows := make([]indexUtilityRow, 0, len(results))
	for i := range results {
		result := &results[i]
		rows = append(rows, indexUtilityRow{
			Name:         result.Name,
			RowClass:     utilityRowClass(result.Inactive),
			Percent:      utilityPercentText(result),
			PercentClass: utilityPercentClass(result),
			Selected:     countOrDash(result.Summary.SelectedTotal, result.Inactive),
			Pass:         countOrDash(result.Summary.Pass, result.Inactive),
			Fail:         countOrDash(result.Summary.Fail, result.Inactive),
			Skip:         countOrDash(result.Summary.Skip, result.Inactive),
			XFail:        countOrDash(result.Summary.XFail, result.Inactive),
			XPass:        countOrDash(result.Summary.XPass, result.Inactive),
			Error:        countOrDash(result.Summary.Error, result.Inactive),
			Unreported:   countOrDash(result.Summary.Unreported, result.Inactive),
			LogFile:      result.LogFile,
			Notes:        utilityNoteText(result),
		})
	}
	return rows
}

func utilityPercentText(result *utilityResult) string {
	if result.Inactive {
		return "-"
	}
	if result.Summary.RunnableTotal == 0 {
		return "n/a"
	}
	return formatPercent(result.Summary.PassPctRunnable)
}

func utilityPercentClass(result *utilityResult) string {
	if result.Inactive {
		return "na"
	}
	return percentageClass(result.Summary.RunnableTotal, result.Summary.PassPctRunnable)
}

func utilityRowClass(inactive bool) string {
	if inactive {
		return "inactive"
	}
	return ""
}

func utilityNoteText(result *utilityResult) string {
	var notes []string
	if result.Reason != "" {
		notes = append(notes, result.Reason)
	}
	if result.Inactive {
		return strings.Join(notes, "; ")
	}
	if result.Summary.SelectedTotal > 0 && result.Summary.RunnableTotal == 0 {
		notes = append(notes, "all selected tests skipped")
	}
	if len(result.Skipped) > 0 {
		notes = append(notes, fmt.Sprintf("%d filtered skips", len(result.Skipped)))
	}
	return strings.Join(notes, "; ")
}

func badgeTextWidth(text string) int {
	return 10 + (len(text) * 7)
}

func badgeColor(summary *runSummary) string {
	if summary.Overall.SelectedTotal == 0 {
		return "#9f9f9f"
	}
	switch {
	case summary.Overall.PassPctSelected >= 90:
		return "#4c1"
	case summary.Overall.PassPctSelected >= 70:
		return "#dfb317"
	default:
		return "#e05d44"
	}
}

func percentageClass(selected int, percent float64) string {
	if selected == 0 {
		return "na"
	}
	switch {
	case percent >= 90:
		return "good"
	case percent >= 70:
		return "warn"
	default:
		return "bad"
	}
}

func formatPercentOrNA(value float64, na bool) string {
	if na {
		return "n/a"
	}
	return formatPercent(value)
}

func countOrDash(value int, inactive bool) string {
	if inactive {
		return "-"
	}
	return fmt.Sprintf("%d", value)
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

func percentage(numerator, denominator float64) float64 {
	if denominator == 0 {
		return 0
	}
	return math.Round((numerator/denominator)*10000) / 100
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "compat-report: "+format+"\n", args...)
	os.Exit(1)
}
