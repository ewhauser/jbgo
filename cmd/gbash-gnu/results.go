package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func parseReportedTestResults(logData []byte, selectedTests []string) (selectedResultsOut, extraResultsOut []testResult) {
	aliases := buildTestAliases(selectedTests)
	selectedResults := make(map[string]*testResult, len(selectedTests))
	extraResults := make(map[string]*testResult)

	for rawLine := range strings.SplitSeq(string(logData), "\n") {
		status, reportedName, ok := parseReportedTestStatusLine(rawLine)
		if !ok {
			continue
		}
		reportedName = filepath.ToSlash(reportedName)
		canonicalName := reportedName
		target := extraResults
		if mappedName, ok := aliases[reportedName]; ok {
			canonicalName = mappedName
			target = selectedResults
		}
		recordReportedTestStatus(target, canonicalName, reportedName, status)
	}

	results := make([]testResult, 0, len(selectedTests))
	for _, selectedTest := range selectedTests {
		canonicalName := filepath.ToSlash(selectedTest)
		if result, ok := selectedResults[canonicalName]; ok {
			results = append(results, *result)
			continue
		}
		results = append(results, testResult{Name: canonicalName, Status: "unreported"})
	}

	extraKeys := make([]string, 0, len(extraResults))
	for name := range extraResults {
		extraKeys = append(extraKeys, name)
	}
	sort.Strings(extraKeys)

	extras := make([]testResult, 0, len(extraKeys))
	for _, name := range extraKeys {
		if r := extraResults[name]; r != nil {
			extras = append(extras, *r)
		}
	}
	return results, extras
}

func applyExpectedFailures(results []testResult, mf *manifest) []testResult {
	if mf == nil || len(mf.ExpectedFailures) == 0 {
		return results
	}

	out := make([]testResult, len(results))
	for i, result := range results {
		out[i] = result
		entry, ok := lookupExpectedFailure(mf, result.Name)
		if !ok {
			continue
		}
		out[i].ExpectedFailureReason = entry.Reason
		switch result.Status {
		case "pass":
			out[i].Status = "xpass"
		case "fail", "error":
			out[i].Status = "xfail"
		}
	}
	return out
}

func buildTestAliases(selectedTests []string) map[string]string {
	candidates := make(map[string][]string, len(selectedTests)*2)
	for _, selectedTest := range selectedTests {
		canonicalName := filepath.ToSlash(selectedTest)
		candidates[canonicalName] = append(candidates[canonicalName], canonicalName)
		switch ext := filepath.Ext(canonicalName); ext {
		case ".pl", ".sh", ".xpl":
			alias := strings.TrimSuffix(canonicalName, ext)
			candidates[alias] = append(candidates[alias], canonicalName)
		}
	}

	aliases := make(map[string]string, len(candidates))
	for alias, names := range candidates {
		if len(names) == 1 {
			aliases[alias] = names[0]
		}
	}
	return aliases
}

func parseReportedTestStatusLine(line string) (status, name string, ok bool) {
	line = strings.TrimSpace(line)
	for prefix, statusName := range map[string]string{
		"PASS:":  "pass",
		"FAIL:":  "fail",
		"SKIP:":  "skip",
		"XFAIL:": "xfail",
		"XPASS:": "xpass",
		"ERROR:": "error",
	} {
		if strings.HasPrefix(line, prefix) {
			testName := strings.TrimSpace(line[len(prefix):])
			if testName == "" {
				return "", "", false
			}
			return statusName, testName, true
		}
	}
	return "", "", false
}

func recordReportedTestStatus(results map[string]*testResult, canonicalName, reportedName, status string) {
	existing := results[canonicalName]
	if existing == nil {
		results[canonicalName] = &testResult{
			Name:       canonicalName,
			Status:     status,
			ReportedAs: []string{reportedName},
		}
		return
	}
	if testStatusPrecedence(status) > testStatusPrecedence(existing.Status) {
		existing.Status = status
	}
	if !containsString(existing.ReportedAs, reportedName) {
		existing.ReportedAs = append(existing.ReportedAs, reportedName)
	}
}

func testStatusPrecedence(status string) int {
	switch status {
	case "error":
		return 6
	case "xpass":
		return 5
	case "fail":
		return 4
	case "xfail":
		return 3
	case "skip":
		return 2
	case "pass":
		return 1
	default:
		return 0
	}
}

func summarizeTestResults(results []testResult, filteredSkipTotal, reportedExtraTotal int) testSummary {
	summary := testSummary{
		SelectedTotal:      len(results),
		FilteredSkipTotal:  filteredSkipTotal,
		ReportedExtraTotal: reportedExtraTotal,
	}
	for _, result := range results {
		switch result.Status {
		case "pass":
			summary.Pass++
		case "fail":
			summary.Fail++
		case "skip":
			summary.Skip++
		case "xfail":
			summary.XFail++
		case "xpass":
			summary.XPass++
		case "error":
			summary.Error++
		default:
			summary.Unreported++
		}
	}
	summary.RunnableTotal = summary.Pass + summary.Fail + summary.XFail + summary.XPass + summary.Error
	summary.PassPctSelected = percentage(summary.Pass, summary.SelectedTotal)
	summary.PassPctRunnable = percentage(summary.Pass, summary.RunnableTotal)
	return summary
}

func summarizeOverall(results []utilityResult) testSummary {
	overall := testSummary{}
	for i := range results {
		utility := &results[i]
		overall.SelectedTotal += utility.Summary.SelectedTotal
		overall.FilteredSkipTotal += utility.Summary.FilteredSkipTotal
		overall.ReportedExtraTotal += utility.Summary.ReportedExtraTotal
		overall.Pass += utility.Summary.Pass
		overall.Fail += utility.Summary.Fail
		overall.Skip += utility.Summary.Skip
		overall.XFail += utility.Summary.XFail
		overall.XPass += utility.Summary.XPass
		overall.Error += utility.Summary.Error
		overall.Unreported += utility.Summary.Unreported
		overall.RunnableTotal += utility.Summary.RunnableTotal
	}
	overall.PassPctSelected = percentage(overall.Pass, overall.SelectedTotal)
	overall.PassPctRunnable = percentage(overall.Pass, overall.RunnableTotal)
	return overall
}

func summarizeUtilityTotals(results []utilityResult) utilityTotals {
	totals := utilityTotals{}
	for i := range results {
		result := &results[i]
		totals.Total++
		if result.Summary.RunnableTotal == 0 {
			totals.NoRunnableTests++
			continue
		}
		if result.Passed {
			totals.Passed++
			continue
		}
		totals.Failed++
	}
	totals.PassPctTotal = percentage(totals.Passed, totals.Total)
	totals.PassPctRunnable = percentage(totals.Passed, totals.Passed+totals.Failed)
	return totals
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func percentage(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return math.Round((float64(numerator)/float64(denominator))*10000) / 100
}

func containsString(items []string, needle string) bool {
	return slices.Contains(items, needle)
}

func buildCoverageArtifacts(selectedTests []string, filteredByPath map[string]string, selectedResults, extraResults []testResult, runs []utilityRun, mf *manifest) (suiteSummary, []categoryResult, []commandCoverage, coverageDebtSummary, []testResult) {
	resultByPath := make(map[string]testResult, len(selectedResults))
	for _, result := range selectedResults {
		resultByPath[result.Name] = cloneTestResult(result)
	}
	commandNames := coverageCommandNames(runs, mf)
	commandSet := make(map[string]struct{}, len(commandNames))
	for _, name := range commandNames {
		commandSet[name] = struct{}{}
	}

	suitePaths := make([]string, 0, len(selectedTests)+len(filteredByPath))
	suitePaths = append(suitePaths, selectedTests...)
	for path := range filteredByPath {
		if containsString(suitePaths, path) {
			continue
		}
		suitePaths = append(suitePaths, path)
	}
	suitePaths = uniqueSortedStrings(suitePaths)

	tests := make([]suiteTest, 0, len(suitePaths))
	for _, path := range suitePaths {
		test := suiteTest{
			Path:         path,
			Category:     testCategory(path),
			Status:       "unreported",
			Attributions: attributeTestPath(path, commandSet, mf),
		}
		if reason, ok := filteredByPath[path]; ok {
			test.Status = "filtered"
			test.Filtered = true
			test.FilterReason = reason
		} else if result, ok := resultByPath[path]; ok {
			test.Status = result.Status
			test.ReportedAs = append([]string(nil), result.ReportedAs...)
			test.ExpectedFailureReason = result.ExpectedFailureReason
		}
		tests = append(tests, test)
	}

	suite := summarizeSuiteTests(tests)
	categories := buildCategoryResults(tests)
	commands := buildCommandCoverage(commandNames, tests)
	coverage := summarizeCoverageDebt(commands, tests, extraResults)
	return suite, categories, commands, coverage, extraResults
}

func combinedSkippedEntriesForRuns(runs []utilityRun) map[string]string {
	out := make(map[string]string)
	for _, run := range runs {
		for _, skipped := range run.Skipped {
			name, reason := splitSkippedEntry(skipped)
			if name == "" {
				continue
			}
			if _, exists := out[name]; exists {
				continue
			}
			out[name] = reason
		}
	}
	return out
}

func parseSkippedEntries(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, reason := splitSkippedEntry(entry)
		if name == "" {
			continue
		}
		out[name] = reason
	}
	return out
}

func splitSkippedEntry(entry string) (name, reason string) {
	name = entry
	if before, after, ok := strings.Cut(entry, ": "); ok {
		name = before
		reason = after
	}
	return name, reason
}

func cloneTestResult(in testResult) testResult {
	out := in
	out.ReportedAs = append([]string(nil), in.ReportedAs...)
	return out
}

func coverageCommandNames(runs []utilityRun, mf *manifest) []string {
	names := make([]string, 0, len(runs))
	seen := make(map[string]struct{})
	for _, run := range runs {
		if run.Utility.Name == "explicit-tests" {
			continue
		}
		if _, ok := seen[run.Utility.Name]; ok {
			continue
		}
		seen[run.Utility.Name] = struct{}{}
		names = append(names, run.Utility.Name)
	}
	for _, rule := range mf.AttributionRules {
		for _, command := range rule.Commands {
			if _, ok := seen[command]; ok {
				continue
			}
			seen[command] = struct{}{}
			names = append(names, command)
		}
	}
	sort.Strings(names)
	return names
}

func attributeTestPath(path string, knownCommands map[string]struct{}, mf *manifest) []testAttribution {
	attributions := make([]testAttribution, 0)
	explicitPrimary := false
	for _, rule := range mf.AttributionRules {
		if !ruleMatchesPath(rule, path) {
			continue
		}
		for _, command := range rule.Commands {
			if _, ok := knownCommands[command]; !ok {
				continue
			}
			kind := strings.TrimSpace(rule.Kind)
			if kind == "" {
				kind = "shared"
			}
			attributions = appendUniqueAttribution(attributions, testAttribution{Command: command, Kind: kind})
			if kind == "direct" {
				explicitPrimary = true
			}
		}
	}
	if !explicitPrimary {
		if owner, ok := defaultPrimaryOwner(path, knownCommands, mf); ok {
			attributions = appendUniqueAttribution(attributions, testAttribution{Command: owner, Kind: "direct"})
		}
	}
	sort.Slice(attributions, func(i, j int) bool {
		if attributions[i].Command == attributions[j].Command {
			return attributions[i].Kind < attributions[j].Kind
		}
		return attributions[i].Command < attributions[j].Command
	})
	return attributions
}

func ruleMatchesPath(rule testAttributionRule, path string) bool {
	for _, pattern := range rule.Patterns {
		if matched, err := filepath.Match(pattern, path); err == nil && matched {
			return true
		}
	}
	return false
}

func appendUniqueAttribution(items []testAttribution, item testAttribution) []testAttribution {
	for _, existing := range items {
		if existing.Command == item.Command && existing.Kind == item.Kind {
			return items
		}
	}
	return append(items, item)
}

func defaultPrimaryOwner(path string, knownCommands map[string]struct{}, mf *manifest) (string, bool) {
	category := testCategory(path)
	switch category {
	case "", "help":
		return "", false
	case "misc":
		if owner, ok := miscPrimaryOwner(path, knownCommands); ok {
			return owner, true
		}
		return "", false
	default:
		aliases := manifestUtilityAliases(mf)
		owner := utilityDisplayName(category, aliases)
		if _, ok := knownCommands[owner]; ok {
			return owner, true
		}
		return "", false
	}
}

func miscPrimaryOwner(path string, knownCommands map[string]struct{}) (string, bool) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	candidates := []string{base}
	if trimmed, ok := strings.CutSuffix(base, "-status"); ok {
		candidates = append(candidates, trimmed)
	}
	for _, candidate := range candidates {
		if _, ok := knownCommands[candidate]; ok {
			return candidate, true
		}
	}
	return "", false
}

func testCategory(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[1]
}

func summarizeSuiteTests(tests []suiteTest) suiteSummary {
	summary := suiteSummary{
		SelectedTotal: len(tests),
		Tests:         append([]suiteTest(nil), tests...),
	}
	for i := range tests {
		test := &tests[i]
		if test.Filtered {
			summary.FilteredTotal++
			continue
		}
		switch test.Status {
		case "pass":
			summary.Pass++
		case "fail":
			summary.Fail++
		case "skip":
			summary.Skip++
		case "xfail":
			summary.XFail++
		case "xpass":
			summary.XPass++
		case "error":
			summary.Error++
		default:
			summary.Unreported++
		}
	}
	summary.RunnableTotal = summary.Pass + summary.Fail + summary.XFail + summary.XPass + summary.Error
	summary.PassPctSelected = percentage(summary.Pass, summary.SelectedTotal)
	summary.PassPctRunnable = percentage(summary.Pass, summary.RunnableTotal)
	return summary
}

func buildCategoryResults(tests []suiteTest) []categoryResult {
	byName := make(map[string][]suiteTest)
	for i := range tests {
		test := &tests[i]
		byName[test.Category] = append(byName[test.Category], *test)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	results := make([]categoryResult, 0, len(names))
	for _, name := range names {
		categoryTests := append([]suiteTest(nil), byName[name]...)
		sort.Slice(categoryTests, func(i, j int) bool { return categoryTests[i].Path < categoryTests[j].Path })
		results = append(results, categoryResult{
			Name:    name,
			Summary: summarizeCoverageBucket(categoryTests),
			Tests:   categoryTests,
		})
	}
	return results
}

func buildCommandCoverage(commandNames []string, tests []suiteTest) []commandCoverage {
	results := make([]commandCoverage, 0, len(commandNames))
	for _, command := range commandNames {
		coverage := commandCoverage{Name: command}
		primaryTests := make([]suiteTest, 0)
		sharedTests := make([]suiteTest, 0)
		for i := range tests {
			test := &tests[i]
			for _, attribution := range test.Attributions {
				if attribution.Command != command {
					continue
				}
				coverage.Tests = append(coverage.Tests, commandTestRef{
					Path:     test.Path,
					Status:   test.Status,
					Kind:     attribution.Kind,
					Filtered: test.Filtered,
				})
				if attribution.Kind == "shared" {
					sharedTests = append(sharedTests, *test)
				} else {
					primaryTests = append(primaryTests, *test)
				}
			}
		}
		sort.Slice(coverage.Tests, func(i, j int) bool {
			if coverage.Tests[i].Path == coverage.Tests[j].Path {
				return coverage.Tests[i].Kind < coverage.Tests[j].Kind
			}
			return coverage.Tests[i].Path < coverage.Tests[j].Path
		})
		coverage.Primary = summarizeCoverageBucket(primaryTests)
		coverage.Shared = summarizeCoverageBucket(sharedTests)
		totalSelected := coverage.Primary.SelectedTotal + coverage.Shared.SelectedTotal
		totalFiltered := coverage.Primary.FilteredTotal + coverage.Shared.FilteredTotal
		switch {
		case totalSelected > 0 && totalSelected == totalFiltered:
			coverage.CoverageState = "filtered-only"
		case coverage.Primary.SelectedTotal > 0:
			coverage.CoverageState = "primary"
		case coverage.Shared.SelectedTotal > 0:
			coverage.CoverageState = "shared-only"
		default:
			coverage.CoverageState = "empty"
		}
		results = append(results, coverage)
	}
	return results
}

func summarizeCoverageBucket(tests []suiteTest) coverageBucket {
	bucket := coverageBucket{
		SelectedTotal: len(tests),
	}
	for i := range tests {
		test := &tests[i]
		if test.Filtered {
			bucket.FilteredTotal++
			continue
		}
		switch test.Status {
		case "pass":
			bucket.Pass++
		case "fail":
			bucket.Fail++
		case "skip":
			bucket.Skip++
		case "xfail":
			bucket.XFail++
		case "xpass":
			bucket.XPass++
		case "error":
			bucket.Error++
		default:
			bucket.Unreported++
		}
	}
	bucket.RunnableTotal = bucket.Pass + bucket.Fail + bucket.XFail + bucket.XPass + bucket.Error
	bucket.PassPctSelected = percentage(bucket.Pass, bucket.SelectedTotal)
	bucket.PassPctRunnable = percentage(bucket.Pass, bucket.RunnableTotal)
	return bucket
}

func summarizeCoverageDebt(commands []commandCoverage, tests []suiteTest, extras []testResult) coverageDebtSummary {
	debt := coverageDebtSummary{
		CommandTotal:       len(commands),
		ExtraReportedTotal: len(extras),
	}
	primaryRunnableCommands := 0
	for i := range commands {
		command := &commands[i]
		switch command.CoverageState {
		case "primary":
			debt.PrimaryCoveredCommands++
			primaryRunnableCommands++
			if command.Primary.RunnableTotal > 0 &&
				command.Primary.Pass == command.Primary.RunnableTotal &&
				command.Primary.Fail == 0 &&
				command.Primary.XPass == 0 &&
				command.Primary.Error == 0 &&
				command.Primary.Unreported == 0 {
				debt.PrimaryPassingCommands++
			}
		case "shared-only":
			debt.SharedOnlyCommands++
		case "filtered-only":
			debt.FilteredOnlyCommands++
			if command.Primary.SelectedTotal > 0 {
				debt.PrimaryCoveredCommands++
			}
		case "empty":
			debt.EmptyCommands++
		}
	}
	debt.PrimaryPassPct = percentage(debt.PrimaryPassingCommands, primaryRunnableCommands)

	for i := range tests {
		test := &tests[i]
		directOwners := 0
		for _, attribution := range test.Attributions {
			if attribution.Kind == "direct" {
				directOwners++
			}
		}
		if directOwners > 1 {
			debt.MultiDirectTests++
		}
		if len(test.Attributions) == 0 {
			debt.UnmappedSelectedTests++
			if !test.Filtered {
				debt.UnmappedRunnableTests++
			}
		}
	}
	return debt
}
