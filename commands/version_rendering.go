package commands

import (
	"fmt"
	"io"
	"strings"
)

type VersionInfo struct {
	Name    string
	Version string
	Commit  string
	Date    string
	BuiltBy string
}

func RenderSimpleVersion(w io.Writer, name string) error {
	_, err := fmt.Fprintf(w, "%s (gbash)\n", strings.TrimSpace(name))
	return err
}

func RenderDetailedVersion(w io.Writer, info VersionInfo) error {
	version := strings.TrimSpace(info.Version)
	if version == "" {
		version = "dev"
	}
	lines := []string{fmt.Sprintf("%s %s", strings.TrimSpace(info.Name), version)}
	if commit := strings.TrimSpace(info.Commit); commit != "" && commit != "unknown" {
		lines = append(lines, fmt.Sprintf("commit: %s", commit))
	}
	if date := strings.TrimSpace(info.Date); date != "" {
		lines = append(lines, fmt.Sprintf("built: %s", date))
	}
	if builtBy := strings.TrimSpace(info.BuiltBy); builtBy != "" {
		lines = append(lines, fmt.Sprintf("built-by: %s", builtBy))
	}
	_, err := io.WriteString(w, strings.Join(lines, "\n")+"\n")
	return err
}
