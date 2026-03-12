package main

import (
	"runtime/debug"
	"strings"

	"github.com/ewhauser/gbash/commands"
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
	var b strings.Builder
	_ = commands.RenderDetailedVersion(&b, commands.VersionInfo{
		Name:    "gbash",
		Version: meta.Version,
		Commit:  meta.Commit,
		Date:    meta.Date,
		BuiltBy: meta.BuiltBy,
	})
	return b.String()
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
