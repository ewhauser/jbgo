package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func parseOptions() (options, error) {
	var opts options
	fs := flag.NewFlagSet("gbash-gnu", flag.ContinueOnError)
	fs.StringVar(&opts.workDir, "workdir", "", "prepared GNU coreutils workdir used for test discovery")
	fs.StringVar(&opts.utils, "utils", "", "comma or space separated utility list")
	fs.StringVar(&opts.tests, "tests", "", "comma or newline separated explicit GNU test files")
	fs.StringVar(&opts.resultsDir, "results-dir", "", "directory to write summary.json")
	fs.StringVar(&opts.logPath, "log", "", "path to the captured make check log")
	fs.IntVar(&opts.exitCode, "exit-code", 0, "exit code from make check")
	fs.BoolVar(&opts.printTests, "print-tests", false, "print the selected runnable tests and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if strings.TrimSpace(opts.workDir) == "" {
		return options{}, fmt.Errorf("--workdir is required")
	}
	if !opts.printTests && strings.TrimSpace(opts.resultsDir) == "" {
		return options{}, fmt.Errorf("--results-dir is required unless --print-tests is used")
	}
	return opts, nil
}

func parseList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		out = append(out, field)
	}
	return out
}
