package builtins

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
)

type backupMode uint8

const (
	backupNone backupMode = iota
	backupSimple
	backupNumbered
	backupExisting
)

const defaultBackupSuffix = "~"

var backupModeValues = []string{
	"simple",
	"never",
	"numbered",
	"t",
	"existing",
	"nil",
	"none",
	"off",
}

func determineBackupSuffix(inv *Invocation, matches *ParsedCommand) string {
	suffix := ""
	if matches != nil {
		suffix = strings.TrimSpace(matches.Value("suffix"))
	}
	if suffix == "" && inv != nil {
		suffix = strings.TrimSpace(backupEnvValue(inv, "SIMPLE_BACKUP_SUFFIX"))
	}
	if suffix == "" || strings.Contains(suffix, "/") {
		return defaultBackupSuffix
	}
	return suffix
}

func determineBackupMode(inv *Invocation, matches *ParsedCommand, commandName string) (backupMode, error) {
	if matches != nil && matches.Has("backup") {
		if value := strings.TrimSpace(matches.Value("backup")); value != "" {
			return matchBackupMode(commandName, value, "backup type")
		}
		if value := strings.TrimSpace(backupEnvValue(inv, "VERSION_CONTROL")); value != "" {
			return matchBackupMode(commandName, value, "$VERSION_CONTROL")
		}
		return backupExisting, nil
	}
	if matches != nil && matches.Has("backup-short") {
		if value := strings.TrimSpace(backupEnvValue(inv, "VERSION_CONTROL")); value != "" {
			return matchBackupMode(commandName, value, "$VERSION_CONTROL")
		}
		return backupExisting, nil
	}
	if matches != nil && matches.Has("suffix") {
		if value := strings.TrimSpace(backupEnvValue(inv, "VERSION_CONTROL")); value != "" {
			return matchBackupMode(commandName, value, "$VERSION_CONTROL")
		}
		return backupExisting, nil
	}
	return backupNone, nil
}

func matchBackupMode(commandName, method, origin string) (backupMode, error) {
	matches := make([]string, 0, len(backupModeValues))
	for _, candidate := range backupModeValues {
		if strings.HasPrefix(candidate, method) {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		return backupNone, exitf(nil, 1, "%s: invalid argument %s for '%s'\nValid arguments are:\n  - 'none', 'off'\n  - 'simple', 'never'\n  - 'existing', 'nil'\n  - 'numbered', 't'", commandName, quoteGNUOperand(method), origin)
	case 1:
		switch matches[0] {
		case "simple", "never":
			return backupSimple, nil
		case "numbered", "t":
			return backupNumbered, nil
		case "existing", "nil":
			return backupExisting, nil
		default:
			return backupNone, nil
		}
	default:
		return backupNone, exitf(nil, 1, "%s: ambiguous argument %s for '%s'\nValid arguments are:\n  - 'none', 'off'\n  - 'simple', 'never'\n  - 'existing', 'nil'\n  - 'numbered', 't'", commandName, quoteGNUOperand(method), origin)
	}
}

func backupPath(ctx context.Context, inv *Invocation, mode backupMode, destAbs, suffix string) (string, error) {
	switch mode {
	case backupSimple:
		return simpleBackupPath(destAbs, suffix), nil
	case backupNumbered:
		return numberedBackupPath(ctx, inv, destAbs)
	case backupExisting:
		if _, exists, err := highestBackupIndex(ctx, inv, destAbs); err != nil {
			return "", err
		} else if exists {
			return numberedBackupPath(ctx, inv, destAbs)
		}
		return simpleBackupPath(destAbs, suffix), nil
	default:
		return "", nil
	}
}

func simpleBackupPath(destAbs, suffix string) string {
	dir := path.Dir(destAbs)
	name := path.Base(destAbs) + suffix
	if dir == "/" {
		return "/" + name
	}
	return path.Join(dir, name)
}

func numberedBackupPath(ctx context.Context, inv *Invocation, destAbs string) (string, error) {
	maxIndex, exists, err := highestBackupIndex(ctx, inv, destAbs)
	if err != nil {
		return "", err
	}
	next := uint64(1)
	if exists {
		next = maxIndex + 1
	}
	return simpleBackupPath(destAbs, fmt.Sprintf(".~%d~", next)), nil
}

func highestBackupIndex(ctx context.Context, inv *Invocation, destAbs string) (uint64, bool, error) {
	dir := path.Dir(destAbs)
	base := path.Base(destAbs)
	entries, err := readDir(ctx, inv, dir)
	if err != nil {
		if errorsIsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}

	prefix := base + ".~"
	var maxIndex uint64
	found := false
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, "~") {
			continue
		}
		indexText := strings.TrimSuffix(strings.TrimPrefix(name, prefix), "~")
		if indexText == "" {
			continue
		}
		index, err := strconv.ParseUint(indexText, 10, 64)
		if err != nil || index == 0 {
			continue
		}
		if !found || index > maxIndex {
			maxIndex = index
			found = true
		}
	}
	return maxIndex, found, nil
}

func backupEnvValue(inv *Invocation, key string) string {
	if inv == nil || inv.Env == nil {
		return ""
	}
	return inv.Env[key]
}
