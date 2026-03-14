package cli

import (
	"runtime/debug"
	"strings"

	"github.com/ewhauser/gbash/commands"
)

func versionText(cfg Config) string {
	cfg = normalizeConfig(cfg)
	meta := currentBuildInfo(cfg.Build)
	var b strings.Builder
	_ = commands.RenderDetailedVersion(&b, &commands.VersionInfo{
		Name:    cfg.Name,
		Version: meta.Version,
		Commit:  meta.Commit,
		Date:    meta.Date,
		BuiltBy: meta.BuiltBy,
	})
	return b.String()
}

func currentBuildInfo(build *BuildInfo) BuildInfo {
	meta := BuildInfo{}
	if build != nil {
		meta = BuildInfo{
			Version: normalizeBuildValue(build.Version),
			Commit:  strings.TrimSpace(build.Commit),
			Date:    strings.TrimSpace(build.Date),
			BuiltBy: strings.TrimSpace(build.BuiltBy),
		}
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
