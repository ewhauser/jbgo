package main

import (
	"fmt"
	"runtime/debug"
	"strings"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = ""
	builtBy = ""
)

type buildMetadata struct {
	Version string
	Commit  string
	Date    string
	BuiltBy string
}

func versionText() string {
	meta := currentBuildMetadata()

	lines := []string{fmt.Sprintf("gbash %s", meta.Version)}
	if meta.Commit != "" && meta.Commit != "unknown" {
		lines = append(lines, fmt.Sprintf("commit: %s", meta.Commit))
	}
	if meta.Date != "" {
		lines = append(lines, fmt.Sprintf("built: %s", meta.Date))
	}
	if meta.BuiltBy != "" {
		lines = append(lines, fmt.Sprintf("built-by: %s", meta.BuiltBy))
	}
	return strings.Join(lines, "\n") + "\n"
}

func currentBuildMetadata() buildMetadata {
	meta := buildMetadata{
		Version: normalizeBuildValue(version),
		Commit:  strings.TrimSpace(commit),
		Date:    strings.TrimSpace(date),
		BuiltBy: strings.TrimSpace(builtBy),
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		if meta.Version == "" {
			meta.Version = normalizeBuildValue(info.Main.Version)
		}
		if meta.Commit == "" || meta.Commit == "unknown" {
			meta.Commit = buildInfoSetting(info, "vcs.revision")
		}
		if meta.Date == "" {
			meta.Date = buildInfoSetting(info, "vcs.time")
		}
	}

	if meta.Version == "" {
		meta.Version = "dev"
	}
	if meta.Commit == "" {
		meta.Commit = "unknown"
	}
	return meta
}

func normalizeBuildValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "(devel)" {
		return ""
	}
	return value
}

func buildInfoSetting(info *debug.BuildInfo, key string) string {
	for _, setting := range info.Settings {
		if setting.Key == key {
			return strings.TrimSpace(setting.Value)
		}
	}
	return ""
}
